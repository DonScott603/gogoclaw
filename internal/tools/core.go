package tools

import (
	"time"

	"github.com/DonScott603/gogoclaw/internal/security"
)

// RegisterAll registers all core tools on the dispatcher.
func RegisterAll(d *Dispatcher, pv *security.PathValidator, workspaceBase string, confirmShell ConfirmFunc) {
	RegisterFileTools(d, pv, workspaceBase)
	RegisterShellTool(d, confirmShell)
	RegisterWebFetchTool(d)
	RegisterThinkTool(d)
	RegisterMemoryTools(d)
	RegisterDiscoverTool(d)
}

// NewCoreDispatcher creates a Dispatcher with all core tools registered.
func NewCoreDispatcher(pv *security.PathValidator, workspaceBase string, confirmShell ConfirmFunc) *Dispatcher {
	d := NewDispatcher(30 * time.Second)
	RegisterAll(d, pv, workspaceBase, confirmShell)
	return d
}
