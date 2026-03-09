package tools

import (
	"time"

	"github.com/DonScott603/gogoclaw/internal/memory"
	"github.com/DonScott603/gogoclaw/internal/security"
)

// RegisterAll registers all core tools on the dispatcher.
func RegisterAll(d *Dispatcher, pv *security.PathValidator, workspaceBase string, confirmShell ConfirmFunc, store memory.VectorStore, searchOpts memory.SearchOptions) {
	RegisterFileTools(d, pv, workspaceBase)
	RegisterShellTool(d, confirmShell)
	RegisterWebFetchTool(d)
	RegisterThinkTool(d)
	RegisterMemoryTools(d, store, searchOpts)
	RegisterDiscoverTool(d)
}

// NewCoreDispatcher creates a Dispatcher with all core tools registered.
func NewCoreDispatcher(pv *security.PathValidator, workspaceBase string, confirmShell ConfirmFunc, store memory.VectorStore, searchOpts memory.SearchOptions) *Dispatcher {
	d := NewDispatcher(30 * time.Second)
	RegisterAll(d, pv, workspaceBase, confirmShell, store, searchOpts)
	return d
}
