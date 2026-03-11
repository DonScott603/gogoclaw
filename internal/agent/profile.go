// Package agent manages agent profile loading, system prompt assembly,
// base inheritance, and the first-run bootstrap ritual.
package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DonScott603/gogoclaw/internal/config"
)

// LoadProfile loads a named agent profile, resolving inheritance.
// If the profile's Inherits field is set, the parent is loaded first
// and the child's non-zero fields are merged on top.
func LoadProfile(agents map[string]config.AgentConfig, name string) (*config.AgentConfig, error) {
	return loadProfileChain(agents, name, nil)
}

func loadProfileChain(agents map[string]config.AgentConfig, name string, visited []string) (*config.AgentConfig, error) {
	for _, v := range visited {
		if v == name {
			return nil, fmt.Errorf("agent: circular inheritance detected: %v -> %s", visited, name)
		}
	}

	ac, ok := agents[name]
	if !ok {
		return nil, fmt.Errorf("agent: profile %q not found", name)
	}

	if ac.Inherits == "" {
		result := ac
		return &result, nil
	}

	parent, err := loadProfileChain(agents, ac.Inherits, append(visited, name))
	if err != nil {
		return nil, err
	}

	merged := mergeAgentConfig(*parent, ac)
	return &merged, nil
}

// mergeAgentConfig deep-merges child on top of parent.
// Non-zero fields in child override parent. Slices in child replace parent slices.
func mergeAgentConfig(parent, child config.AgentConfig) config.AgentConfig {
	result := parent

	if child.Name != "" {
		result.Name = child.Name
	}
	// Inherits is consumed during resolution, not carried forward.
	result.Inherits = ""

	if child.SystemPromptFile != "" {
		result.SystemPromptFile = child.SystemPromptFile
	}

	// ProviderRouting
	if child.ProviderRouting.Mode != "" {
		result.ProviderRouting.Mode = child.ProviderRouting.Mode
	}
	if len(child.ProviderRouting.ProviderChain) > 0 {
		result.ProviderRouting.ProviderChain = child.ProviderRouting.ProviderChain
	}

	// PII
	if child.PII.Mode != "" {
		result.PII.Mode = child.PII.Mode
	}

	// Skills
	if child.Skills.AlwaysAvailable {
		result.Skills.AlwaysAvailable = true
	}
	if child.Skills.AutoDiscover {
		result.Skills.AutoDiscover = true
	}
	if len(child.Skills.Allowed) > 0 {
		result.Skills.Allowed = child.Skills.Allowed
	}

	// Context
	if child.Context.MaxHistoryTokens > 0 {
		result.Context.MaxHistoryTokens = child.Context.MaxHistoryTokens
	}
	if child.Context.Summarization.Enabled {
		result.Context.Summarization.Enabled = true
	}
	if child.Context.Summarization.Provider != "" {
		result.Context.Summarization.Provider = child.Context.Summarization.Provider
	}
	if child.Context.Summarization.ThresholdTokens > 0 {
		result.Context.Summarization.ThresholdTokens = child.Context.Summarization.ThresholdTokens
	}

	// Memory
	if child.MemoryConfig.Enabled {
		result.MemoryConfig.Enabled = true
	}
	if child.MemoryConfig.TopK > 0 {
		result.MemoryConfig.TopK = child.MemoryConfig.TopK
	}
	if child.MemoryConfig.RelevanceThreshold > 0 {
		result.MemoryConfig.RelevanceThreshold = child.MemoryConfig.RelevanceThreshold
	}
	if child.MemoryConfig.RecencyWeight > 0 {
		result.MemoryConfig.RecencyWeight = child.MemoryConfig.RecencyWeight
	}

	// Shell
	if child.Shell.Confirmation != "" {
		result.Shell.Confirmation = child.Shell.Confirmation
	}

	// Network
	if len(child.Network.AdditionalAllowlist) > 0 {
		result.Network.AdditionalAllowlist = child.Network.AdditionalAllowlist
	}

	return result
}

// ResolveSystemPrompt reads the markdown file referenced by the profile's
// SystemPromptFile from the agents config directory. Returns empty string
// if the file doesn't exist (the engine has a hardcoded fallback).
func ResolveSystemPrompt(agentDir string, profile config.AgentConfig) (string, error) {
	if profile.SystemPromptFile == "" {
		return "", nil
	}
	path := filepath.Join(agentDir, profile.SystemPromptFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("agent: read system prompt %q: %w", path, err)
	}
	return string(data), nil
}
