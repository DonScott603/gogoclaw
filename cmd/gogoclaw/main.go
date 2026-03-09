package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/internal/memory"
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

		// Log the resolved path and whether existing data was found.
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

	// Create confirm gate — the program reference is set after construction.
	gate, confirmFn := tui.NewConfirmGate()

	// Build tool dispatcher with all core tools.
	dispatcher := tools.NewCoreDispatcher(ws.Validator, ws.Base, confirmFn, memStore, searchOpts)

	// Load system prompt.
	systemPrompt := loadSystemPrompt(configDir, cfg)

	// Determine max context tokens.
	maxCtx := 8192
	if agent, ok := cfg.Agents["base"]; ok && agent.Context.MaxHistoryTokens > 0 {
		maxCtx = agent.Context.MaxHistoryTokens
	}

	// Set up summarizer if enabled.
	if agent, ok := cfg.Agents["base"]; ok && agent.Context.Summarization.Enabled {
		summarizer = memory.NewSummarizer(p, agent.Context.Summarization.ThresholdTokens, memStore)
	}

	// Create engine.
	eng := engine.New(engine.Config{
		Provider:     p,
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

	// Create the TUI program once with the real engine.
	program := tui.New(eng)
	gate.SetProgram(program)

	// Wire tool call/result observers to the TUI.
	onCall, onResult := tui.ToolCallObserver(program)
	dispatcher.SetCallbacks(onCall, onResult)

	_ = store // store ready for TUI conversation persistence integration

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
