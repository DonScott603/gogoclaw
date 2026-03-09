// Package health provides health checking and status aggregation
// for providers, channels, and the memory system.
package health

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Status represents the health status of a component.
type Status string

const (
	StatusHealthy     Status = "healthy"
	StatusUnhealthy   Status = "unhealthy"
	StatusDegraded    Status = "degraded"
	StatusUnknown     Status = "unknown"
)

// ComponentStatus holds the status of a single component.
type ComponentStatus struct {
	Name      string
	Status    Status
	Details   string
	LastCheck time.Time
}

// HealthChecker is implemented by components that can report health.
type HealthChecker interface {
	Healthy(ctx context.Context) bool
	Name() string
}

// Monitor periodically checks component health.
type Monitor struct {
	mu         sync.RWMutex
	components []ComponentStatus
	checkers   []HealthChecker
	piiMode    string
	interval   time.Duration
	cancel     context.CancelFunc
}

// MonitorConfig configures the health monitor.
type MonitorConfig struct {
	Interval time.Duration
	PIIMode  string
}

// NewMonitor creates a health monitor.
func NewMonitor(cfg MonitorConfig) *Monitor {
	interval := cfg.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Monitor{
		interval: interval,
		piiMode:  cfg.PIIMode,
	}
}

// Register adds a health checker to the monitor.
func (m *Monitor) Register(checker HealthChecker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkers = append(m.checkers, checker)
	m.components = append(m.components, ComponentStatus{
		Name:   checker.Name(),
		Status: StatusUnknown,
	})
}

// SetPIIMode updates the displayed PII mode.
func (m *Monitor) SetPIIMode(mode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.piiMode = mode
}

// Start begins periodic health checking.
func (m *Monitor) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go m.run(ctx)
}

// Stop stops periodic health checking.
func (m *Monitor) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

// CheckNow runs an immediate health check on all components.
func (m *Monitor) CheckNow() {
	m.mu.Lock()
	checkers := make([]HealthChecker, len(m.checkers))
	copy(checkers, m.checkers)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for i, checker := range checkers {
		status := StatusUnhealthy
		details := "unreachable"
		if checker.Healthy(ctx) {
			status = StatusHealthy
			details = "ok"
		}

		m.mu.Lock()
		if i < len(m.components) {
			m.components[i].Status = status
			m.components[i].Details = details
			m.components[i].LastCheck = time.Now()
		}
		m.mu.Unlock()
	}
}

// Status returns the current status of all components.
func (m *Monitor) Status() []ComponentStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ComponentStatus, len(m.components))
	copy(out, m.components)
	return out
}

// PIIMode returns the current PII mode.
func (m *Monitor) PIIMode() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.piiMode
}

// Summary returns a text summary of all component health.
func (m *Monitor) Summary() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var b strings.Builder
	b.WriteString("Health Status\n")
	b.WriteString(strings.Repeat("─", 40) + "\n")

	for _, c := range m.components {
		indicator := "?"
		switch c.Status {
		case StatusHealthy:
			indicator = "OK"
		case StatusUnhealthy:
			indicator = "FAIL"
		case StatusDegraded:
			indicator = "WARN"
		}
		ago := ""
		if !c.LastCheck.IsZero() {
			ago = fmt.Sprintf(" (%s ago)", time.Since(c.LastCheck).Truncate(time.Second))
		}
		b.WriteString(fmt.Sprintf("[%s] %s: %s%s\n", indicator, c.Name, c.Details, ago))
	}

	b.WriteString(fmt.Sprintf("\nPII Mode: %s\n", m.piiMode))
	return b.String()
}

func (m *Monitor) run(ctx context.Context) {
	// Initial check.
	m.CheckNow()

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.CheckNow()
		}
	}
}
