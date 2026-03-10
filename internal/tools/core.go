package tools

import (
	"net/http"
	"time"

	"github.com/DonScott603/gogoclaw/internal/memory"
	"github.com/DonScott603/gogoclaw/internal/security"
)

// RegisterAll registers all core tools on the dispatcher.
func RegisterAll(d *Dispatcher, pv *security.PathValidator, workspaceBase string, confirmShell ConfirmFunc, store memory.VectorStore, searchOpts memory.SearchOptions, netTransport http.RoundTripper, scrubber SecretScrubber, onScrub ScrubNotifyFn) {
	RegisterFileTools(d, pv, workspaceBase)
	RegisterShellTool(d, confirmShell)
	RegisterWebFetchTool(d, netTransport)
	RegisterThinkTool(d)
	RegisterMemoryTools(d, store, searchOpts, scrubber, onScrub)
	RegisterDiscoverTool(d)
}

// NewCoreDispatcher creates a Dispatcher with all core tools registered.
func NewCoreDispatcher(pv *security.PathValidator, workspaceBase string, confirmShell ConfirmFunc, store memory.VectorStore, searchOpts memory.SearchOptions, netTransport http.RoundTripper, scrubber SecretScrubber, onScrub ScrubNotifyFn) *Dispatcher {
	d := NewDispatcher(30 * time.Second)
	RegisterAll(d, pv, workspaceBase, confirmShell, store, searchOpts, netTransport, scrubber, onScrub)
	return d
}
