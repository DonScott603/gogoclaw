package app

import (
	"fmt"
	"net/http"
	"time"

	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/pii"
	"github.com/DonScott603/gogoclaw/internal/provider"
	"github.com/DonScott603/gogoclaw/internal/security"
)

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
