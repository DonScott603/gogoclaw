package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/DonScott603/gogoclaw/internal/audit"
	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/internal/health"
	"github.com/DonScott603/gogoclaw/internal/memory"
	"github.com/DonScott603/gogoclaw/internal/pii"
	"github.com/DonScott603/gogoclaw/internal/provider"
	"github.com/DonScott603/gogoclaw/internal/security"
	"github.com/DonScott603/gogoclaw/internal/skill"
	"github.com/DonScott603/gogoclaw/internal/storage"
	"github.com/DonScott603/gogoclaw/internal/tools"
	"github.com/DonScott603/gogoclaw/internal/tui"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("gogoclaw %s\n", version)
		return
	}

	// Temporary: manual WASM skill test.
	if len(os.Args) > 1 && os.Args[1] == "skill-test" {
		runSkillTest()
		return
	}

	// Determine config directory.
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("cannot determine home directory: %v", err)
	}
	configDir := filepath.Join(home, ".gogoclaw")

	// Load config. If no config dir exists, use defaults.
	loader := config.NewLoader(configDir)
	cfg, err := loader.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Initialize audit logger.
	auditPath := expandHome(cfg.Logging.Audit.Path)
	if auditPath == "" {
		auditPath = filepath.Join(configDir, "audit", "gogoclaw.jsonl")
	}
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{
		Enabled: cfg.Logging.Audit.Enabled,
		Path:    auditPath,
	})
	if err != nil {
		log.Printf("audit: failed to initialize: %v (continuing without audit)", err)
		auditLogger, _ = audit.NewLogger(audit.LoggerConfig{Enabled: false})
	}
	defer auditLogger.Close()

	// Initialize network guard.
	netGuard := security.NewNetworkGuard(security.NetworkGuardConfig{
		Allowlist:     cfg.Network.Allowlist,
		DenyAllOthers: cfg.Network.DenyAllOthers,
		LogBlocked:    cfg.Network.LogBlocked,
		OnBlocked: func(domain, requester string, ts time.Time) {
			auditLogger.LogNetworkBlocked(domain, requester, "not_in_allowlist")
		},
	})
	if agent, ok := cfg.Agents["base"]; ok {
		netGuard.AddAgentAllowlist(agent.Network.AdditionalAllowlist)
	}
	netTransport := netGuard.Transport("web_fetch")

	// Initialize secret scrubber.
	scrubber := security.NewSecretScrubber()

	// Attach scrubber to audit logger so field values are redacted.
	auditLogger.SetScrubber(scrubber)

	// Build provider from config.
	p, err := buildProvider(cfg)
	if err != nil {
		log.Fatalf("provider: %v", err)
	}

	// Determine PII mode and wrap provider with PII gate.
	piiMode := pii.ModeDisabled
	isLocal := false
	if agent, ok := cfg.Agents["base"]; ok && agent.PII.Mode != "" {
		piiMode = pii.Mode(agent.PII.Mode)
	}
	if pc := firstProvider(cfg); pc != nil && pc.Type == "ollama" {
		isLocal = true
	}

	piiGate := pii.NewGate(p, pii.GateConfig{
		Mode:    piiMode,
		IsLocal: isLocal,
		AuditFn: func(patterns []string, mode pii.Mode, action string) {
			auditLogger.LogPIIDetected(patterns, string(mode), action)
		},
	})
	// Use the PII gate as the provider going forward.
	var activeProvider provider.Provider = piiGate

	// Initialize workspace.
	ws, err := engine.NewWorkspace(cfg.Workspace)
	if err != nil {
		log.Fatalf("workspace: %v", err)
	}

	// Open conversation store.
	dbPath := expandHome(cfg.Storage.Conversations.Path)
	if dbPath == "" {
		dbPath = filepath.Join(configDir, "data", "conversations.db")
	}
	os.MkdirAll(filepath.Dir(dbPath), 0o755)
	store, err := storage.NewStore(dbPath)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer store.Close()

	// Initialize memory system.
	var memStore memory.VectorStore
	var summarizer *memory.Summarizer
	searchOpts := memory.SearchOptions{
		MinSimilarity: 0.5,
		RecencyWeight: 0.2,
	}

	if cfg.Memory.Enabled {
		vecPath := expandHome(cfg.Memory.Storage.Path)
		if vecPath == "" {
			vecPath = filepath.Join(configDir, "data", "vectors")
		}
		os.MkdirAll(vecPath, 0o755)

		if entries, err := os.ReadDir(vecPath); err == nil {
			log.Printf("memory: vector store path: %s (%d existing entries)", vecPath, len(entries))
		} else {
			log.Printf("memory: vector store path: %s (new directory)", vecPath)
		}

		embFn := memory.NewEmbeddingFunc(cfg.Memory, cfg.Providers)
		cs, err := memory.NewChromemStore(memory.ChromemConfig{
			Path:          vecPath,
			Compress:      true,
			EmbeddingFunc: embFn,
		})
		if err != nil {
			log.Printf("memory: failed to initialize vector store: %v (continuing without memory)", err)
		} else {
			memStore = cs
			log.Printf("memory: vector store initialized successfully (persistent=%v)", vecPath != "")
			defer cs.Close()
		}

		if cfg.Memory.Retrieval.RelevanceThreshold > 0 {
			searchOpts.MinSimilarity = cfg.Memory.Retrieval.RelevanceThreshold
		}
		if cfg.Memory.Retrieval.RecencyWeight > 0 {
			searchOpts.RecencyWeight = cfg.Memory.Retrieval.RecencyWeight
		}
	} else {
		log.Printf("memory: disabled (set memory.enabled=true in config to enable)")
	}

	// Initialize skill registry — scan user skills and built-in skills.
	skillsDir := filepath.Join(configDir, "skills.d")
	skillReg, err := skill.NewRegistry(skillsDir)
	if err != nil {
		log.Printf("skills: user skills scan failed: %v", err)
		skillReg, _ = skill.NewRegistry(os.TempDir()) // empty fallback
	}

	// Also scan built-in skills from the binary's directory.
	exePath, _ := os.Executable()
	builtinDir := filepath.Join(filepath.Dir(exePath), "skills", "builtin")
	if builtinReg, err := skill.NewRegistry(builtinDir); err == nil {
		for _, s := range builtinReg.ListSkills() {
			skillReg.AddSkill(s)
		}
	}

	// Initialize WASM runtime and skill dispatcher.
	ctx := context.Background()
	skillRT, err := skill.NewRuntime(ctx)
	if err != nil {
		log.Printf("skills: runtime init failed: %v (continuing without skills)", err)
	}
	var skillDisp *skill.SkillDispatcher
	var skillLister tools.SkillLister
	if skillRT != nil {
		defer skillRT.Close(ctx)
		skillDisp = skill.NewSkillDispatcher(skillReg, skillRT)
		skillLister = skillDisp
	}

	allSkills := skillReg.ListSkills()
	log.Printf("skills: found %d skill(s)", len(allSkills))
	for _, s := range allSkills {
		log.Printf("skills:   %s v%s (%d tools)", s.Manifest.Name, s.Manifest.Version, len(s.Manifest.Tools))
	}

	// Create confirm gate — the program reference is set after construction.
	gate, confirmFn := tui.NewConfirmGate()

	// Secret scrub notification callback — logs an audit event when scrubbing occurs.
	onScrub := func(component, ctxStr string) {
		auditLogger.LogSecretScrubbed(component, ctxStr)
	}

	// Attach scrubber to conversation store.
	store.SetScrubber(scrubber, onScrub)

	// Build tool dispatcher with all core tools.
	dispatcher := tools.NewCoreDispatcher(ws.Validator, ws.Base, confirmFn, memStore, searchOpts, netTransport, scrubber, onScrub, skillLister)

	// Register skill tools on the dispatcher.
	if skillDisp != nil {
		if err := skillDisp.RegisterSkillTools(ctx, dispatcher); err != nil {
			log.Printf("skills: failed to register skill tools: %v", err)
		}
	}

	// Load system prompt.
	systemPrompt := loadSystemPrompt(configDir, cfg)

	// Determine max context tokens.
	maxCtx := 8192
	if agent, ok := cfg.Agents["base"]; ok && agent.Context.MaxHistoryTokens > 0 {
		maxCtx = agent.Context.MaxHistoryTokens
	}

	// Set up summarizer if enabled.
	if agent, ok := cfg.Agents["base"]; ok && agent.Context.Summarization.Enabled {
		summarizer = memory.NewSummarizer(activeProvider, agent.Context.Summarization.ThresholdTokens, memStore)
	}

	// Create engine with PII-gated provider.
	eng := engine.New(engine.Config{
		Provider:     activeProvider,
		Dispatcher:   dispatcher,
		SystemPrompt: systemPrompt,
		MaxContext:   maxCtx,
		Summarizer:   summarizer,
	})

	// Configure memory retrieval on the context assembler.
	if memStore != nil {
		topK := 5
		if agent, ok := cfg.Agents["base"]; ok && agent.MemoryConfig.TopK > 0 {
			topK = agent.MemoryConfig.TopK
		}
		eng.Assembler().SetMemoryStore(memStore, topK, searchOpts)
	}

	// Initialize health monitor.
	monitor := health.NewMonitor(health.MonitorConfig{
		PIIMode: string(piiMode),
	})
	monitor.Register(p) // register the raw provider (not the gate)
	monitor.Start()
	defer monitor.Stop()

	// Create the TUI program once with the real engine.
	program := tui.New(eng, tui.WithHealthMonitor(monitor))
	gate.SetProgram(program)

	// Wire tool call/result observers to the TUI.
	onCall, onResult := tui.ToolCallObserver(program)
	dispatcher.SetCallbacks(onCall, onResult)

	// Wire PII warn-mode notifications to the TUI.
	piiWarnSend := tui.PIIWarnFunc(program)
	piiGate.SetWarnFn(func(patterns []string, mode pii.Mode) {
		piiWarnSend(patterns)
	})

	if _, err := program.Run(); err != nil {
		log.Fatalf("tui: %v", err)
	}
}

func buildProvider(cfg *config.Config) (provider.Provider, error) {
	if len(cfg.Providers) == 0 {
		return nil, fmt.Errorf("no providers configured; add a YAML file to ~/.gogoclaw/providers/")
	}

	var providers []provider.Provider
	var timeouts []time.Duration
	var retries []int

	if agent, ok := cfg.Agents["base"]; ok && len(agent.ProviderRouting.ProviderChain) > 0 {
		for _, entry := range agent.ProviderRouting.ProviderChain {
			pc, ok := cfg.Providers[entry.Provider]
			if !ok {
				continue
			}
			providers = append(providers, makeProvider(pc))
			timeouts = append(timeouts, entry.Timeout)
			retries = append(retries, entry.Retry)
		}
	}

	if len(providers) == 0 {
		for _, pc := range cfg.Providers {
			providers = append(providers, makeProvider(pc))
			timeouts = append(timeouts, pc.Timeout)
			retries = append(retries, pc.Retry)
		}
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("no usable providers found in config")
	}
	if len(providers) == 1 {
		return providers[0], nil
	}
	return provider.NewRouter(providers, timeouts, retries), nil
}

func makeProvider(pc config.ProviderConfig) provider.Provider {
	switch pc.Type {
	case "ollama":
		return provider.NewOllama(provider.OllamaConfig{
			Name:         pc.Name,
			BaseURL:      pc.BaseURL,
			DefaultModel: pc.DefaultModel,
			Timeout:      pc.Timeout,
		})
	default:
		return provider.NewOpenAICompat(provider.OpenAICompatConfig{
			Name:             pc.Name,
			BaseURL:          pc.BaseURL,
			APIKey:           pc.APIKey,
			DefaultModel:     pc.DefaultModel,
			MaxContextTokens: pc.MaxContextTokens,
			Timeout:          pc.Timeout,
		})
	}
}

// firstProvider returns the first configured provider config, or nil.
func firstProvider(cfg *config.Config) *config.ProviderConfig {
	if agent, ok := cfg.Agents["base"]; ok && len(agent.ProviderRouting.ProviderChain) > 0 {
		name := agent.ProviderRouting.ProviderChain[0].Provider
		if pc, ok := cfg.Providers[name]; ok {
			return &pc
		}
	}
	for _, pc := range cfg.Providers {
		return &pc
	}
	return nil
}

func loadSystemPrompt(configDir string, cfg *config.Config) string {
	if agent, ok := cfg.Agents["base"]; ok && agent.SystemPromptFile != "" {
		path := filepath.Join(configDir, "agents", agent.SystemPromptFile)
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data)
		}
	}
	return "You are GoGoClaw, a helpful AI assistant. You have access to tools for file operations, shell commands, web fetching, and memory. Use them when appropriate."
}

// runSkillTest is a temporary subcommand for manual WASM skill verification.
func runSkillTest() {
	ctx := context.Background()

	// Find the echo.wasm testdata relative to the executable or cwd.
	wasmPath := filepath.Join("skills", "testdata", "echo", "echo.wasm")
	if _, err := os.Stat(wasmPath); err != nil {
		log.Fatalf("skill-test: cannot find %s: %v", wasmPath, err)
	}

	rt, err := skill.NewRuntime(ctx)
	if err != nil {
		log.Fatalf("skill-test: runtime init: %v", err)
	}
	defer rt.Close(ctx)

	entry := &skill.SkillEntry{
		Manifest: &skill.Manifest{
			Name:        "echo",
			Version:     "1.0.0",
			Description: "Echo test skill",
			Tools: []skill.ToolSpec{
				{Name: "echo", Description: "Echo back input"},
			},
			Permissions: skill.Permissions{MaxExecTime: 10},
		},
		Dir:      filepath.Dir(wasmPath),
		WasmPath: wasmPath,
	}

	fmt.Println("Loading echo skill...")
	if err := rt.LoadSkill(ctx, entry); err != nil {
		log.Fatalf("skill-test: load: %v", err)
	}

	args, _ := json.Marshal(map[string]string{"message": "hello from manual test"})
	fmt.Printf("Executing with: %s\n", string(args))

	result, err := rt.Execute(ctx, "echo", args)
	if err != nil {
		log.Fatalf("skill-test: execute: %v", err)
	}

	fmt.Printf("Result: %s\n", string(result))

	if err := rt.UnloadSkill(ctx, "echo"); err != nil {
		log.Fatalf("skill-test: unload: %v", err)
	}
	fmt.Println("Skill unloaded. Done.")
}

func expandHome(path string) string {
	if len(path) < 2 {
		return path
	}
	// Handle both ~/path and ~\path (Windows).
	if path[0] == '~' && (path[1] == '/' || path[1] == '\\') {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
