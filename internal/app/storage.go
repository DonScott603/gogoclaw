package app

import (
	"context"
	"fmt"
	"log"
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
	Encryptor *storage.Encryptor
}

// InitStorage sets up the workspace and conversation store.
// If encryption is enabled in config, it derives/loads a master key and
// migrates any existing plaintext messages.
func InitStorage(ctx context.Context, cfg *config.Config, configDir string, secDeps SecurityDeps, auditDeps AuditDeps) (StorageDeps, error) {
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

	var enc *storage.Encryptor

	if cfg.Storage.Conversations.Encrypt {
		var source string

		passphraseEnv := cfg.Storage.Conversations.PassphraseEnv
		if passphraseEnv == "" {
			passphraseEnv = "GOGOCLAW_DB_PASSPHRASE"
		}

		if passphrase := os.Getenv(passphraseEnv); passphrase != "" {
			saltPath := filepath.Join(configDir, "data", ".encryption_salt")
			salt, err := storage.LoadOrCreateSalt(saltPath)
			if err != nil {
				return StorageDeps{}, fmt.Errorf("storage: encryption salt: %w", err)
			}
			enc, err = storage.NewEncryptorFromPassphrase(passphrase, salt)
			if err != nil {
				return StorageDeps{}, fmt.Errorf("storage: encryption key derivation: %w", err)
			}
			source = "passphrase"
		} else {
			keyPath := filepath.Join(configDir, "data", ".encryption_key")
			key, err := storage.LoadOrCreateKey(keyPath)
			if err != nil {
				return StorageDeps{}, fmt.Errorf("storage: encryption key: %w", err)
			}
			enc, err = storage.NewEncryptorFromKey(key)
			if err != nil {
				return StorageDeps{}, fmt.Errorf("storage: encryptor: %w", err)
			}
			source = "auto-key"
			log.Printf("WARNING: using auto-generated encryption key at %s", keyPath)
			log.Printf("WARNING: losing this file will make encrypted conversations and audit logs unrecoverable")
		}

		store.SetEncryptor(enc)
		log.Printf("storage: encryption enabled (source: %s)", source)

		// Migrate existing plaintext messages.
		if err := store.MigrateToEncrypted(ctx); err != nil {
			return StorageDeps{}, fmt.Errorf("storage: encryption migration: %w", err)
		}
	}

	return StorageDeps{Workspace: ws, Store: store, Encryptor: enc}, nil
}
