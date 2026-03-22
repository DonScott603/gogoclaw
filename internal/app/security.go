package app

import (
	"fmt"
	"net/http"
	"sort"
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

	providerTransport := netGuard.Transport("provider")
	p, err := buildProvider(cfg, providerTransport)
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

func buildProvider(cfg *config.Config, transport http.RoundTripper) (provider.Provider, error) {
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
			providers = append(providers, makeProvider(pc, transport))
			timeouts = append(timeouts, entry.Timeout)
			retries = append(retries, entry.Retry)
		}
	}

	if len(providers) == 0 {
		keys := make([]string, 0, len(cfg.Providers))
		for k := range cfg.Providers {
			if k == "example" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			pc := cfg.Providers[k]
			providers = append(providers, makeProvider(pc, transport))
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

func makeProvider(pc config.ProviderConfig, transport http.RoundTripper) provider.Provider {
	switch pc.Type {
	case "ollama":
		return provider.NewOllama(provider.OllamaConfig{
			Name:         pc.Name,
			BaseURL:      pc.BaseURL,
			DefaultModel: pc.DefaultModel,
			Timeout:      pc.Timeout,
			Transport:    transport,
		})
	default:
		return provider.NewOpenAICompat(provider.OpenAICompatConfig{
			Name:             pc.Name,
			BaseURL:          pc.BaseURL,
			APIKey:           pc.APIKey,
			DefaultModel:     pc.DefaultModel,
			MaxContextTokens: pc.MaxContextTokens,
			Timeout:          pc.Timeout,
			Transport:        transport,
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
	keys := make([]string, 0, len(cfg.Providers))
	for k := range cfg.Providers {
		if k == "example" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		pc := cfg.Providers[k]
		return &pc
	}
	return nil
}
