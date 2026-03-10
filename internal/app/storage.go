package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/internal/storage"
	"github.com/DonScott603/gogoclaw/internal/util"
)

// StorageDeps holds the workspace and conversation store.
type StorageDeps struct {
	Workspace *engine.Workspace
	Store     *storage.Store
}

// InitStorage sets up the workspace and conversation store.
func InitStorage(cfg *config.Config, configDir string, secDeps SecurityDeps, auditDeps AuditDeps) (StorageDeps, error) {
	ws, err := engine.NewWorkspace(cfg.Workspace)
	if err != nil {
		return StorageDeps{}, fmt.Errorf("workspace: %w", err)
	}

	dbPath := util.ExpandHome(cfg.Storage.Conversations.Path)
	if dbPath == "" {
		dbPath = filepath.Join(configDir, "data", "conversations.db")
	}
	os.MkdirAll(filepath.Dir(dbPath), 0o755)
	store, err := storage.NewStore(dbPath)
	if err != nil {
		return StorageDeps{}, fmt.Errorf("storage: %w", err)
	}

	onScrub := func(component, ctxStr string) {
		auditDeps.Logger.LogSecretScrubbed(component, ctxStr)
	}
	store.SetScrubber(secDeps.Scrubber, onScrub)

	return StorageDeps{Workspace: ws, Store: store}, nil
}
