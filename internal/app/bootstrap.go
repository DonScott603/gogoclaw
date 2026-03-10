// Package app provides typed dependency structs and constructor functions
// for bootstrapping the GoGoClaw application. Each Init function handles
// its own setup, error handling, and logging.
package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
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
)

// AuditDeps holds the initialized audit logger.
type AuditDeps struct {
	Logger *audit.Logger
}

// InitAudit creates and configures the audit logger.
func InitAudit(cfg *config.Config, configDir string) AuditDeps {
	auditPath := expandHome(cfg.Logging.Audit.Path)
	if auditPath == "" {
		auditPath = filepath.Join(configDir, "audit", "gogoclaw.jsonl")
	}
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Enabled: cfg.Logging.Audit.Enabled,
		Path:    auditPath,
	})
	if err != nil {
		log.Printf("audit: failed to initialize: %v (continuing without audit)", err)
		logger, _ = audit.NewLogger(audit.LoggerConfig{Enabled: false})
	}
	return AuditDeps{Logger: logger}
}

// SecurityDeps holds network guard, transport, scrubber, and PII gate.
type SecurityDeps struct {
	NetTransport   http.RoundTripper
	Scrubber       *security.SecretScrubber
	RawProvider    provider.Provider
	ActiveProvider provider.Provider // PII-gated
	PIIGate        *pii.Gate
	PIIMode        pii.Mode
}

// InitSecurity sets up network guard, secret scrubber, provider, and PII gate.
func InitSecurity(cfg *config.Config, auditDeps AuditDeps) (SecurityDeps, error) {
	netGuard := security.NewNetworkGuard(security.NetworkGuardConfig{
		Allowlist:     cfg.Network.Allowlist,
		DenyAllOthers: cfg.Network.DenyAllOthers,
		LogBlocked:    cfg.Network.LogBlocked,
		OnBlocked: func(domain, requester string, ts time.Time) {
			auditDeps.Logger.LogNetworkBlocked(domain, requester, "not_in_allowlist")
		},
	})
	if agent, ok := cfg.Agents["base"]; ok {
		netGuard.AddAgentAllowlist(agent.Network.AdditionalAllowlist)
	}

	scrubber := security.NewSecretScrubber()
	auditDeps.Logger.SetScrubber(scrubber)

	p, err := buildProvider(cfg)
	if err != nil {
		return SecurityDeps{}, fmt.Errorf("provider: %w", err)
	}

	piiMode := pii.ModeDisabled
	isLocal := false
	if agent, ok := cfg.Agents["base"]; ok && agent.PII.Mode != "" {
		piiMode = pii.Mode(agent.PII.Mode)
	}
	if pc := firstProvider(cfg); pc != nil && pc.Type == "ollama" {
		isLocal = true
	}

	gate := pii.NewGate(p, pii.GateConfig{
		Mode:    piiMode,
		IsLocal: isLocal,
		AuditFn: func(patterns []string, mode pii.Mode, action string) {
			auditDeps.Logger.LogPIIDetected(patterns, string(mode), action)
		},
	})

	return SecurityDeps{
		NetTransport:   netGuard.Transport("web_fetch"),
		Scrubber:       scrubber,
		RawProvider:    p,
		ActiveProvider: gate,
		PIIGate:        gate,
		PIIMode:        piiMode,
	}, nil
}

// StorageDeps holds the workspace and conversation store.
type StorageDeps struct {
	Workspace *engine.Workspace
	Store     *storage.Store
}

// InitStorage sets up the workspace and conversation store.
func InitStorage(cfg *config.Config, configDir string, secDeps SecurityDeps, auditDeps AuditDeps) (StorageDeps, error) {
	ws, err := engine.NewWorkspace(cfg.Workspace)
	if err != nil {
		return StorageDeps{}, fmt.Errorf("workspace: %w", err)
	}

	dbPath := expandHome(cfg.Storage.Conversations.Path)
	if dbPath == "" {
		dbPath = filepath.Join(configDir, "data", "conversations.db")
	}
	os.MkdirAll(filepath.Dir(dbPath), 0o755)
	store, err := storage.NewStore(dbPath)
	if err != nil {
		return StorageDeps{}, fmt.Errorf("storage: %w", err)
	}

	onScrub := func(component, ctxStr string) {
		auditDeps.Logger.LogSecretScrubbed(component, ctxStr)
	}
	store.SetScrubber(secDeps.Scrubber, onScrub)

	return StorageDeps{Workspace: ws, Store: store}, nil
}

// MemoryDeps holds the vector store, search options, and summarizer.
type MemoryDeps struct {
	Store      memory.VectorStore
	SearchOpts memory.SearchOptions
	Summarizer *memory.Summarizer
	closeFn    func() // internal; called by Close
}

// InitMemory sets up the vector-backed memory system.
func InitMemory(cfg *config.Config, configDir string, activeProvider provider.Provider) MemoryDeps {
	deps := MemoryDeps{
		SearchOpts: memory.SearchOptions{
			MinSimilarity: 0.5,
			RecencyWeight: 0.2,
		},
	}

	if !cfg.Memory.Enabled {
		log.Printf("memory: disabled (set memory.enabled=true in config to enable)")
		return deps
	}

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
		return deps
	}

	deps.Store = cs
	deps.closeFn = func() { cs.Close() }
	log.Printf("memory: vector store initialized successfully (persistent=%v)", vecPath != "")

	if cfg.Memory.Retrieval.RelevanceThreshold > 0 {
		deps.SearchOpts.MinSimilarity = cfg.Memory.Retrieval.RelevanceThreshold
	}
	if cfg.Memory.Retrieval.RecencyWeight > 0 {
		deps.SearchOpts.RecencyWeight = cfg.Memory.Retrieval.RecencyWeight
	}

	if agent, ok := cfg.Agents["base"]; ok && agent.Context.Summarization.Enabled {
		deps.Summarizer = memory.NewSummarizer(activeProvider, agent.Context.Summarization.ThresholdTokens, deps.Store)
	}

	return deps
}

// Close releases memory resources.
func (d *MemoryDeps) Close() {
	if d.closeFn != nil {
		d.closeFn()
	}
}

// SkillDeps holds the skill registry, runtime, and dispatcher.
type SkillDeps struct {
	Registry   *skill.Registry
	Runtime    *skill.Runtime
	Dispatcher *skill.SkillDispatcher
	Lister     tools.SkillLister
}

// InitSkills sets up the skill registry, WASM runtime, and skill dispatcher.
func InitSkills(configDir string) SkillDeps {
	skillsDir := filepath.Join(configDir, "skills.d")
	reg, err := skill.NewRegistry(skillsDir)
	if err != nil {
		log.Printf("skills: user skills scan failed: %v", err)
		reg, _ = skill.NewRegistry(os.TempDir())
	}

	builtinDir := resolveBuiltinSkillsDir(configDir)
	if builtinDir != "" {
		log.Printf("skills: loading built-in skills from %s", builtinDir)
		if builtinReg, err := skill.NewRegistry(builtinDir); err == nil {
			for _, s := range builtinReg.ListSkills() {
				reg.AddSkill(s)
			}
		}
	} else {
		log.Printf("skills: no built-in skills directory found")
	}

	ctx := context.Background()
	rt, err := skill.NewRuntime(ctx)
	if err != nil {
		log.Printf("skills: runtime init failed: %v (continuing without skills)", err)
		return SkillDeps{Registry: reg}
	}

	sd := skill.NewSkillDispatcher(reg, rt)

	allSkills := reg.ListSkills()
	log.Printf("skills: found %d skill(s)", len(allSkills))
	for _, s := range allSkills {
		log.Printf("skills:   %s v%s (%d tools)", s.Manifest.Name, s.Manifest.Version, len(s.Manifest.Tools))
	}

	return SkillDeps{
		Registry:   reg,
		Runtime:    rt,
		Dispatcher: sd,
		Lister:     sd,
	}
}

// Close shuts down the WASM runtime.
func (d *SkillDeps) Close() {
	if d.Runtime != nil {
		d.Runtime.Close(context.Background())
	}
}

// EngineDeps holds the engine, tool dispatcher, and health monitor.
type EngineDeps struct {
	Engine     *engine.Engine
	Dispatcher *tools.Dispatcher
	Monitor    *health.Monitor
}

// InitEngine builds the tool dispatcher, engine, and health monitor.
func InitEngine(cfg *config.Config, configDir string, secDeps SecurityDeps, storeDeps StorageDeps, memDeps MemoryDeps, skillDeps SkillDeps, auditDeps AuditDeps, confirmFn tools.ConfirmFunc) EngineDeps {
	onScrub := func(component, ctxStr string) {
		auditDeps.Logger.LogSecretScrubbed(component, ctxStr)
	}

	dispatcher := tools.NewCoreDispatcher(
		storeDeps.Workspace.Validator, storeDeps.Workspace.Base,
		confirmFn, memDeps.Store, memDeps.SearchOpts,
		secDeps.NetTransport, secDeps.Scrubber, onScrub, skillDeps.Lister,
	)

	if skillDeps.Dispatcher != nil {
		ctx := context.Background()
		if err := skillDeps.Dispatcher.RegisterSkillTools(ctx, dispatcher); err != nil {
			log.Printf("skills: failed to register skill tools: %v", err)
		}
	}

	systemPrompt := loadSystemPrompt(configDir, cfg)

	maxCtx := 8192
	if agent, ok := cfg.Agents["base"]; ok && agent.Context.MaxHistoryTokens > 0 {
		maxCtx = agent.Context.MaxHistoryTokens
	}

	eng := engine.New(engine.Config{
		Provider:     secDeps.ActiveProvider,
		Dispatcher:   dispatcher,
		SystemPrompt: systemPrompt,
		MaxContext:   maxCtx,
		Summarizer:   memDeps.Summarizer,
	})

	if memDeps.Store != nil {
		topK := 5
		if agent, ok := cfg.Agents["base"]; ok && agent.MemoryConfig.TopK > 0 {
			topK = agent.MemoryConfig.TopK
		}
		eng.Assembler().SetMemoryStore(memDeps.Store, topK, memDeps.SearchOpts)
	}

	monitor := health.NewMonitor(health.MonitorConfig{
		PIIMode: string(secDeps.PIIMode),
	})
	monitor.Register(secDeps.RawProvider)
	monitor.Start()

	return EngineDeps{
		Engine:     eng,
		Dispatcher: dispatcher,
		Monitor:    monitor,
	}
}

// --- helpers (moved from main) ---

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

func resolveBuiltinSkillsDir(configDir string) string {
	var candidates []string

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "skills", "builtin"),
			filepath.Join(exeDir, "..", "skills", "builtin"),
		)
	}

	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "skills", "builtin"))
	}

	candidates = append(candidates, filepath.Join(configDir, "skills", "builtin"))

	for _, dir := range candidates {
		dir = filepath.Clean(dir)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

func expandHome(path string) string {
	if len(path) < 2 {
		return path
	}
	if path[0] == '~' && (path[1] == '/' || path[1] == '\\') {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
