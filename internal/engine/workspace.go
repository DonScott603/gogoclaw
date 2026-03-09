package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/security"
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
		Base:      expandHome(cfg.Base),
		Inbox:     expandHome(cfg.Inbox),
		Outbox:    expandHome(cfg.Outbox),
		Scratch:   expandHome(cfg.Scratch),
		Documents: expandHome(cfg.Documents),
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

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}
