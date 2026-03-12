package app

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/DonScott603/gogoclaw/internal/agent"
	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/internal/health"
	"github.com/DonScott603/gogoclaw/internal/tools"
)

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

	if skillDeps.Dispatcher != nil { // no-op path has nil Dispatcher, only real skills register
		if err := skillDeps.Dispatcher.RegisterSkillTools(context.Background(), dispatcher); err != nil {
			log.Printf("skills: failed to register skill tools: %v", err)
		}
	}

	systemPrompt := loadSystemPrompt(configDir, cfg)
	systemPrompt = resolvePromptVars(configDir, cfg, systemPrompt)
	log.Printf("DEBUG system prompt (first 200): %.200s", systemPrompt)

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

	topK := 5
	if agent, ok := cfg.Agents["base"]; ok && agent.MemoryConfig.TopK > 0 {
		topK = agent.MemoryConfig.TopK
	}
	eng.Assembler().SetMemoryStore(memDeps.Store, topK, memDeps.SearchOpts)

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

// resolvePromptVars applies template variable resolution to the system prompt.
func resolvePromptVars(configDir string, cfg *config.Config, prompt string) string {
	vars := make(map[string]string)

	if ac, ok := cfg.Agents["base"]; ok && ac.Name != "" {
		vars["agent_name"] = ac.Name
	}

	// Read user name from user.md if it exists.
	userPath := filepath.Join(configDir, "agents", "user.md")
	if data, err := os.ReadFile(userPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "Name: ") {
				vars["user_name"] = strings.TrimPrefix(line, "Name: ")
				break
			}
		}
	}

	return agent.ResolveTemplateVars(prompt, vars)
}

func loadSystemPrompt(configDir string, cfg *config.Config) string {
	if agent, ok := cfg.Agents["base"]; ok && agent.SystemPromptFile != "" {
		path := filepath.Join(configDir, "agents", agent.SystemPromptFile)
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data)
		}
	}
	return `You are GoGoClaw, a helpful AI assistant. You have access to tools for file operations, shell commands, web fetching, and memory. Use them when appropriate.

## Channel Behavior
- When the user's message starts with [Channel: Telegram] or [Channel: REST API], always respond with inline text directly in your response. Do NOT use file_write to create files for your response content. The user cannot easily access files from these channels. Only use file_write if the user explicitly asks you to save something to a file.
- When no channel prefix is present, you are in the TUI and may use file_write for long content if appropriate.`
}
