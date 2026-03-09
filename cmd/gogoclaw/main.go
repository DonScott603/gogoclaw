package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/internal/provider"
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

	// Build provider from config.
	p, err := buildProvider(cfg)
	if err != nil {
		log.Fatalf("provider: %v", err)
	}

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

	// Create confirm gate — the program reference is set after construction.
	gate, confirmFn := tui.NewConfirmGate()

	// Build tool dispatcher with all core tools.
	dispatcher := tools.NewCoreDispatcher(ws.Validator, ws.Base, confirmFn)

	// Load system prompt.
	systemPrompt := loadSystemPrompt(configDir, cfg)

	// Determine max context tokens.
	maxCtx := 8192
	if agent, ok := cfg.Agents["base"]; ok && agent.Context.MaxHistoryTokens > 0 {
		maxCtx = agent.Context.MaxHistoryTokens
	}

	// Create engine.
	eng := engine.New(engine.Config{
		Provider:     p,
		Dispatcher:   dispatcher,
		SystemPrompt: systemPrompt,
		MaxContext:    maxCtx,
	})

	// Create the TUI program once with the real engine.
	program := tui.New(eng)
	gate.SetProgram(program)

	// Wire tool call/result observers to the TUI.
	onCall, onResult := tui.ToolCallObserver(program)
	dispatcher.SetCallbacks(onCall, onResult)

	_ = store // store ready for Phase 2 TUI conversation persistence integration

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

func expandHome(path string) string {
	if len(path) > 1 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
