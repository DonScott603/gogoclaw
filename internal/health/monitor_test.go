package health

import (
	"context"
	"strings"
	"testing"
	"time"
)

type mockChecker struct {
	name    string
	healthy bool
}

func (m *mockChecker) Healthy(_ context.Context) bool { return m.healthy }
func (m *mockChecker) Name() string                   { return m.name }

func TestMonitorRegisterAndCheck(t *testing.T) {
	m := NewMonitor(MonitorConfig{Interval: time.Hour})

	m.Register(&mockChecker{name: "provider-a", healthy: true})
	m.Register(&mockChecker{name: "provider-b", healthy: false})

	m.CheckNow()

	statuses := m.Status()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}

	if statuses[0].Status != StatusHealthy {
		t.Errorf("provider-a status = %q, want %q", statuses[0].Status, StatusHealthy)
	}
	if statuses[1].Status != StatusUnhealthy {
		t.Errorf("provider-b status = %q, want %q", statuses[1].Status, StatusUnhealthy)
	}
}

func TestMonitorSummary(t *testing.T) {
	m := NewMonitor(MonitorConfig{
		Interval: time.Hour,
		PIIMode:  "strict",
	})

	m.Register(&mockChecker{name: "ollama", healthy: true})
	m.Register(&mockChecker{name: "minimax", healthy: false})

	m.CheckNow()

	summary := m.Summary()
	if !strings.Contains(summary, "ollama") {
		t.Error("summary missing ollama")
	}
	if !strings.Contains(summary, "minimax") {
		t.Error("summary missing minimax")
	}
	if !strings.Contains(summary, "PII Mode: strict") {
		t.Error("summary missing PII mode")
	}
	if !strings.Contains(summary, "[OK]") {
		t.Error("summary missing OK indicator")
	}
	if !strings.Contains(summary, "[FAIL]") {
		t.Error("summary missing FAIL indicator")
	}
}

func TestMonitorSetPIIMode(t *testing.T) {
	m := NewMonitor(MonitorConfig{PIIMode: "disabled"})
	if m.PIIMode() != "disabled" {
		t.Errorf("initial PIIMode = %q, want %q", m.PIIMode(), "disabled")
	}

	m.SetPIIMode("strict")
	if m.PIIMode() != "strict" {
		t.Errorf("updated PIIMode = %q, want %q", m.PIIMode(), "strict")
	}
}

func TestMonitorStartStop(t *testing.T) {
	m := NewMonitor(MonitorConfig{Interval: 50 * time.Millisecond})
	m.Register(&mockChecker{name: "test", healthy: true})

	m.Start()
	time.Sleep(100 * time.Millisecond)
	m.Stop()

	statuses := m.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Status != StatusHealthy {
		t.Errorf("expected healthy after periodic check, got %q", statuses[0].Status)
	}
}

func TestMonitorInitialUnknown(t *testing.T) {
	m := NewMonitor(MonitorConfig{Interval: time.Hour})
	m.Register(&mockChecker{name: "test", healthy: true})

	statuses := m.Status()
	if statuses[0].Status != StatusUnknown {
		t.Errorf("initial status = %q, want %q", statuses[0].Status, StatusUnknown)
	}
}
