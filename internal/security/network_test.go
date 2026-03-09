package security

import (
	"testing"
	"time"
)

func TestNetworkGuardExactMatch(t *testing.T) {
	g := NewNetworkGuard(NetworkGuardConfig{
		Allowlist:     []string{"api.example.com", "localhost"},
		DenyAllOthers: true,
	})

	tests := []struct {
		domain string
		want   bool
	}{
		{"api.example.com", true},
		{"localhost", true},
		{"evil.com", false},
		{"api.example.com:8080", true}, // with port
	}
	for _, tt := range tests {
		got := g.IsAllowed(tt.domain)
		if got != tt.want {
			t.Errorf("IsAllowed(%q) = %v, want %v", tt.domain, got, tt.want)
		}
	}
}

func TestNetworkGuardWildcard(t *testing.T) {
	g := NewNetworkGuard(NetworkGuardConfig{
		Allowlist:     []string{".schwab.com", "api.openai.com"},
		DenyAllOthers: true,
	})

	tests := []struct {
		domain string
		want   bool
	}{
		{"www.schwab.com", true},
		{"api.schwab.com", true},
		{"schwab.com", true},
		{"api.openai.com", true},
		{"evil.com", false},
		{"notschwab.com", false},
	}
	for _, tt := range tests {
		got := g.IsAllowed(tt.domain)
		if got != tt.want {
			t.Errorf("IsAllowed(%q) = %v, want %v", tt.domain, got, tt.want)
		}
	}
}

func TestNetworkGuardDenyAllFalse(t *testing.T) {
	g := NewNetworkGuard(NetworkGuardConfig{
		Allowlist:     []string{"api.example.com"},
		DenyAllOthers: false,
	})

	// When deny_all_others is false, unlisted domains are allowed.
	if !g.IsAllowed("anything.com") {
		t.Error("with deny_all_others=false, unlisted domains should be allowed")
	}
}

func TestNetworkGuardAgentAllowlist(t *testing.T) {
	g := NewNetworkGuard(NetworkGuardConfig{
		Allowlist:     []string{"api.example.com"},
		DenyAllOthers: true,
	})

	g.AddAgentAllowlist([]string{".fidelity.com", "api.schwab.com"})

	tests := []struct {
		domain string
		want   bool
	}{
		{"api.example.com", true},
		{"www.fidelity.com", true},
		{"api.schwab.com", true},
		{"evil.com", false},
	}
	for _, tt := range tests {
		got := g.IsAllowed(tt.domain)
		if got != tt.want {
			t.Errorf("IsAllowed(%q) = %v, want %v", tt.domain, got, tt.want)
		}
	}
}

func TestNetworkGuardCheckAndLog(t *testing.T) {
	blocked := false
	var blockedDomain, blockedRequester string

	g := NewNetworkGuard(NetworkGuardConfig{
		Allowlist:     []string{"api.example.com"},
		DenyAllOthers: true,
		LogBlocked:    true,
		OnBlocked: func(domain, requester string, ts time.Time) {
			blocked = true
			blockedDomain = domain
			blockedRequester = requester
		},
	})

	if !g.CheckAndLog("api.example.com", "core") {
		t.Error("allowed domain should pass")
	}
	if blocked {
		t.Error("should not call OnBlocked for allowed domain")
	}

	if g.CheckAndLog("evil.com", "skill:csv") {
		t.Error("blocked domain should fail")
	}
	if !blocked {
		t.Error("should call OnBlocked for blocked domain")
	}
	if blockedDomain != "evil.com" {
		t.Errorf("blocked domain = %q, want %q", blockedDomain, "evil.com")
	}
	if blockedRequester != "skill:csv" {
		t.Errorf("blocked requester = %q, want %q", blockedRequester, "skill:csv")
	}
}

func TestNetworkGuardCaseInsensitive(t *testing.T) {
	g := NewNetworkGuard(NetworkGuardConfig{
		Allowlist:     []string{"API.Example.COM"},
		DenyAllOthers: true,
	})

	if !g.IsAllowed("api.example.com") {
		t.Error("domain matching should be case-insensitive")
	}
}
