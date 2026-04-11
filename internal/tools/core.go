package tools

import (
	"net/http"
	"time"

	"github.com/DonScott603/gogoclaw/internal/memory"
	"github.com/DonScott603/gogoclaw/internal/security"
)

// RegisterAll registers all core tools on the dispatcher.
func RegisterAll(d *Dispatcher, pv *security.PathValidator, workspaceBase string, confirmShell ConfirmFunc, shellTimeout time.Duration, store memory.VectorStore, searchOpts memory.SearchOptions, netTransport http.RoundTripper, scrubber SecretScrubber, onScrub ScrubNotifyFn, skillLister SkillLister) {
	RegisterFileTools(d, pv, workspaceBase)
	RegisterShellTool(d, confirmShell, shellTimeout)
	RegisterWebFetchTool(d, netTransport)
	RegisterThinkTool(d)
	RegisterMemoryTools(d, store, searchOpts, scrubber, onScrub)
	RegisterDiscoverTool(d, skillLister)
}

// NewCoreDispatcher creates a Dispatcher with all core tools registered.
func NewCoreDispatcher(pv *security.PathValidator, workspaceBase string, confirmShell ConfirmFunc, shellTimeout time.Duration, store memory.VectorStore, searchOpts memory.SearchOptions, netTransport http.RoundTripper, scrubber SecretScrubber, onScrub ScrubNotifyFn, skillLister SkillLister) *Dispatcher {
	d := NewDispatcher(30 * time.Second)
	RegisterAll(d, pv, workspaceBase, confirmShell, shellTimeout, store, searchOpts, netTransport, scrubber, onScrub, skillLister)
	return d
}
