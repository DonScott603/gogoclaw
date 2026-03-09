// Package security implements domain allowlist enforcement, path traversal
// prevention, skill signing verification, and secret management.
package security

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// NetworkGuard enforces domain-level allowlisting for all outbound HTTP.
type NetworkGuard struct {
	mu             sync.RWMutex
	globalAllow    map[string]bool
	wildcardAllow  []string // domains prefixed with "." for wildcard matching
	agentAllow     map[string]bool
	agentWildcards []string
	denyAll        bool
	logBlocked     bool
	onBlocked      func(domain, requester string, ts time.Time)
}

// NetworkGuardConfig configures the network guard.
type NetworkGuardConfig struct {
	Allowlist     []string
	DenyAllOthers bool
	LogBlocked    bool
	OnBlocked     func(domain, requester string, ts time.Time)
}

// NewNetworkGuard creates a NetworkGuard from config.
func NewNetworkGuard(cfg NetworkGuardConfig) *NetworkGuard {
	g := &NetworkGuard{
		globalAllow: make(map[string]bool),
		agentAllow:  make(map[string]bool),
		denyAll:     cfg.DenyAllOthers,
		logBlocked:  cfg.LogBlocked,
		onBlocked:   cfg.OnBlocked,
	}
	for _, domain := range cfg.Allowlist {
		g.addDomain(domain, true)
	}
	return g
}

// AddAgentAllowlist adds per-agent additional allowed domains.
func (g *NetworkGuard) AddAgentAllowlist(domains []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, domain := range domains {
		d := strings.ToLower(strings.TrimSpace(domain))
		if strings.HasPrefix(d, ".") {
			g.agentWildcards = append(g.agentWildcards, d)
		} else {
			g.agentAllow[d] = true
		}
	}
}

func (g *NetworkGuard) addDomain(domain string, global bool) {
	d := strings.ToLower(strings.TrimSpace(domain))
	if d == "" {
		return
	}
	if strings.HasPrefix(d, ".") {
		g.wildcardAllow = append(g.wildcardAllow, d)
	} else {
		g.globalAllow[d] = true
	}
}

// IsAllowed checks if a domain is permitted by the allowlist.
func (g *NetworkGuard) IsAllowed(domain string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	d := strings.ToLower(strings.TrimSpace(domain))
	// Strip port if present.
	if host, _, err := net.SplitHostPort(d); err == nil {
		d = host
	}

	// Check exact match in global allowlist.
	if g.globalAllow[d] {
		return true
	}
	// Check exact match in agent allowlist.
	if g.agentAllow[d] {
		return true
	}

	// Check wildcard matches (e.g., ".schwab.com" matches "www.schwab.com").
	for _, w := range g.wildcardAllow {
		if strings.HasSuffix(d, w) || d == w[1:] {
			return true
		}
	}
	for _, w := range g.agentWildcards {
		if strings.HasSuffix(d, w) || d == w[1:] {
			return true
		}
	}

	return !g.denyAll
}

// CheckAndLog checks if a domain is allowed and logs if blocked.
func (g *NetworkGuard) CheckAndLog(domain, requester string) bool {
	if g.IsAllowed(domain) {
		return true
	}
	if g.logBlocked && g.onBlocked != nil {
		g.onBlocked(domain, requester, time.Now())
	}
	return false
}

// Transport returns an http.RoundTripper that enforces the allowlist.
func (g *NetworkGuard) Transport(requester string) http.RoundTripper {
	return &guardedTransport{
		guard:     g,
		inner:     http.DefaultTransport,
		requester: requester,
	}
}

type guardedTransport struct {
	guard     *NetworkGuard
	inner     http.RoundTripper
	requester string
}

func (t *guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	domain := req.URL.Hostname()
	if !t.guard.CheckAndLog(domain, t.requester) {
		return nil, fmt.Errorf("network: request to %q blocked by allowlist (requester: %s)", domain, t.requester)
	}
	return t.inner.RoundTrip(req)
}
