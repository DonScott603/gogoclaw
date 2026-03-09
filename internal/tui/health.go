package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/DonScott603/gogoclaw/internal/health"

	"github.com/charmbracelet/lipgloss"
)

// renderHealthPanel renders the health dashboard with color-coded indicators.
func (m model) renderHealthPanel() string {
	var b strings.Builder
	width := m.width
	if width < 40 {
		width = 40
	}

	// Title.
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	b.WriteString(titleStyle.Render("Health Dashboard") + "\n")
	b.WriteString(strings.Repeat("─", min(width-4, 50)) + "\n\n")

	if m.healthMonitor == nil {
		dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		b.WriteString(dimStyle.Render("No health monitor configured.") + "\n")
		b.WriteString("\nPress Ctrl+H to return to chat.\n")
		return b.String()
	}

	// Component statuses.
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	b.WriteString(sectionStyle.Render("Components") + "\n")

	statuses := m.healthMonitor.Status()
	if len(statuses) == 0 {
		dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		b.WriteString(dimStyle.Render("  No components registered.") + "\n")
	}

	for _, cs := range statuses {
		indicator, style := componentIndicator(cs.Status)
		name := lipgloss.NewStyle().Bold(true).Render(cs.Name)

		ago := "never"
		if !cs.LastCheck.IsZero() {
			d := time.Since(cs.LastCheck).Truncate(time.Second)
			ago = fmt.Sprintf("%s ago", d)
		}

		details := cs.Details
		if details == "" {
			details = string(cs.Status)
		}

		b.WriteString(fmt.Sprintf("  %s %s — %s (%s)\n",
			style.Render(indicator), name, details, ago))
	}

	// PII Mode.
	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("PII Mode") + "\n")
	piiMode := m.healthMonitor.PIIMode()
	if piiMode == "" {
		piiMode = "disabled"
	}
	piiIndicator, piiStyle := piiModeIndicator(piiMode)
	b.WriteString(fmt.Sprintf("  %s %s\n", piiStyle.Render(piiIndicator), piiMode))

	// Last check timestamp.
	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("Last Check") + "\n")
	lastCheck := latestCheck(statuses)
	if lastCheck.IsZero() {
		b.WriteString("  No checks performed yet.\n")
	} else {
		b.WriteString(fmt.Sprintf("  %s\n", lastCheck.Format("15:04:05 2006-01-02")))
	}

	b.WriteString("\nPress Ctrl+H to return to chat.\n")
	return b.String()
}

// componentIndicator returns a color-coded indicator for component status.
func componentIndicator(s health.Status) (string, lipgloss.Style) {
	switch s {
	case health.StatusHealthy:
		return "●", lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	case health.StatusUnhealthy:
		return "●", lipgloss.NewStyle().Foreground(lipgloss.Color("9")) // red
	case health.StatusDegraded:
		return "●", lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow
	default: // unknown
		return "●", lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // gray
	}
}

// piiModeIndicator returns a color-coded indicator for PII mode.
func piiModeIndicator(mode string) (string, lipgloss.Style) {
	switch mode {
	case "strict":
		return "■", lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	case "warn":
		return "■", lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow
	case "permissive":
		return "■", lipgloss.NewStyle().Foreground(lipgloss.Color("208")) // orange
	default: // disabled
		return "■", lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // gray
	}
}

// latestCheck returns the most recent LastCheck time from component statuses.
func latestCheck(statuses []health.ComponentStatus) time.Time {
	var latest time.Time
	for _, s := range statuses {
		if s.LastCheck.After(latest) {
			latest = s.LastCheck
		}
	}
	return latest
}
