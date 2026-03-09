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

	// Load system prompt.
	systemPrompt := loadSystemPrompt(configDir, cfg)

	// Create engine and TUI.
	eng := engine.New(p, systemPrompt)
	program := tui.New(eng)

	if _, err := program.Run(); err != nil {
		log.Fatalf("tui: %v", err)
	}
}

func buildProvider(cfg *config.Config) (provider.Provider, error) {
	if len(cfg.Providers) == 0 {
		return nil, fmt.Errorf("no providers configured; add a YAML file to ~/.gogoclaw/providers/")
	}

	// If there's an agent config with a provider chain, use the router.
	// Otherwise use the first available provider.
	var providers []provider.Provider
	var timeouts []time.Duration
	var retries []int

	// Check for a base agent with routing config.
	if agent, ok := cfg.Agents["base"]; ok && len(agent.ProviderRouting.ProviderChain) > 0 {
		for _, entry := range agent.ProviderRouting.ProviderChain {
			pc, ok := cfg.Providers[entry.Provider]
			if !ok {
				continue
			}
			p := makeProvider(pc)
			providers = append(providers, p)
			timeouts = append(timeouts, entry.Timeout)
			retries = append(retries, entry.Retry)
		}
	}

	// Fallback: use all configured providers in map iteration order.
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
	default: // "openai_compatible" and anything else
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
	// Try loading from agent profile's system_prompt_file.
	if agent, ok := cfg.Agents["base"]; ok && agent.SystemPromptFile != "" {
		path := filepath.Join(configDir, "agents", agent.SystemPromptFile)
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data)
		}
	}
	return "You are GoGoClaw, a helpful AI assistant."
}
