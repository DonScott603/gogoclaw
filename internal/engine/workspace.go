package engine

import (
	"fmt"
	"os"

	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/security"
	"github.com/DonScott603/gogoclaw/internal/util"
)

// Workspace manages workspace directories and provides a path validator
// scoped to the allowed workspace roots.
type Workspace struct {
	Base      string
	Inbox     string
	Outbox    string
	Scratch   string
	Documents string
	Validator *security.PathValidator
}

// NewWorkspace creates and validates workspace directories from config.
// It expands ~ to the user's home directory and creates missing directories.
func NewWorkspace(cfg config.WorkspaceConfig) (*Workspace, error) {
	w := &Workspace{
		Base:      util.ExpandHome(cfg.Base),
		Inbox:     util.ExpandHome(cfg.Inbox),
		Outbox:    util.ExpandHome(cfg.Outbox),
		Scratch:   util.ExpandHome(cfg.Scratch),
		Documents: util.ExpandHome(cfg.Documents),
	}

	// Create all directories.
	dirs := []string{w.Base, w.Inbox, w.Outbox, w.Scratch, w.Documents}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("workspace: create %q: %w", d, err)
		}
	}

	// Build path validator scoped to these directories.
	pv, err := security.NewPathValidator(dirs)
	if err != nil {
		return nil, fmt.Errorf("workspace: path validator: %w", err)
	}
	w.Validator = pv

	return w, nil
}

