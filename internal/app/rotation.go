package app

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DonScott603/gogoclaw/internal/storage"
)

// Staging file names used during key rotation.
const (
	stagingKeyFile  = ".encryption_key.new"
	stagingSaltFile = ".encryption_salt.new"
	keyFile         = ".encryption_key"
	saltFile        = ".encryption_salt"
)

func dataDir(configDir string) string {
	return filepath.Join(configDir, "data")
}

// ResolveNewEncryptor resolves or creates the new encryptor for rotation.
// If a staging file exists (.encryption_key.new or .encryption_salt.new),
// it is loaded and reused. Otherwise a new key is generated and staged.
//
// If dryRun is true, no staging files are created — key material is
// generated in-memory only. Existing staging files are still detected
// and reported but not modified.
//
// Returns: encryptor, source description, error.
func ResolveNewEncryptor(configDir string, newPassphrase string, dryRun bool) (*storage.Encryptor, string, error) {
	dir := dataDir(configDir)

	keyPath := filepath.Join(dir, stagingKeyFile)
	saltPath := filepath.Join(dir, stagingSaltFile)

	keyExists := fileExists(keyPath)
	saltExists := fileExists(saltPath)

	// Detect conflicting staging files.
	if keyExists && saltExists {
		return nil, "", fmt.Errorf("rotation: conflicting staging files found — both %s and %s exist. Remove one manually before re-running", stagingKeyFile, stagingSaltFile)
	}

	// Check for existing staging file.
	if keyExists {
		key, err := loadBase64Key(keyPath)
		if err != nil {
			return nil, "", fmt.Errorf("rotation: load staged key: %w", err)
		}
		enc, err := storage.NewEncryptorFromKey(key)
		if err != nil {
			return nil, "", fmt.Errorf("rotation: staged key: %w", err)
		}
		return enc, "auto-key (staged)", nil
	}

	if saltExists {
		if newPassphrase == "" {
			return nil, "", fmt.Errorf("rotation: staged salt file found but no --new-passphrase provided — this is likely a re-run; provide the same passphrase used in the previous attempt")
		}
		salt, err := os.ReadFile(saltPath)
		if err != nil {
			return nil, "", fmt.Errorf("rotation: load staged salt: %w", err)
		}
		enc, err := storage.NewEncryptorFromPassphrase(newPassphrase, salt)
		if err != nil {
			return nil, "", fmt.Errorf("rotation: staged salt: %w", err)
		}
		return enc, "passphrase (staged)", nil
	}

	// No staging file found — generate fresh.
	if newPassphrase != "" {
		salt, err := storage.GenerateSalt()
		if err != nil {
			return nil, "", fmt.Errorf("rotation: generate salt: %w", err)
		}
		if !dryRun {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, "", fmt.Errorf("rotation: create data dir: %w", err)
			}
			if err := os.WriteFile(saltPath, salt, 0o600); err != nil {
				return nil, "", fmt.Errorf("rotation: write staging salt: %w", err)
			}
		}
		enc, err := storage.NewEncryptorFromPassphrase(newPassphrase, salt)
		if err != nil {
			return nil, "", fmt.Errorf("rotation: derive key: %w", err)
		}
		source := "passphrase"
		if dryRun {
			source = "passphrase (dry-run)"
		}
		return enc, source, nil
	}

	// Generate new auto-key.
	key, err := storage.GenerateKey()
	if err != nil {
		return nil, "", fmt.Errorf("rotation: generate key: %w", err)
	}
	if !dryRun {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, "", fmt.Errorf("rotation: create data dir: %w", err)
		}
		encoded := base64.StdEncoding.EncodeToString(key)
		if err := os.WriteFile(keyPath, []byte(encoded), 0o600); err != nil {
			return nil, "", fmt.Errorf("rotation: write staging key: %w", err)
		}
	}
	enc, err := storage.NewEncryptorFromKey(key)
	if err != nil {
		return nil, "", fmt.Errorf("rotation: new key: %w", err)
	}
	source := "auto-key"
	if dryRun {
		source = "auto-key (dry-run)"
	}
	return enc, source, nil
}

// PromoteKeyFiles promotes staging files to final and backs up old files.
// Called only after RotateKeys succeeds.
func PromoteKeyFiles(configDir string, oldSource string, newSource string) error {
	dir := dataDir(configDir)

	keyPath := filepath.Join(dir, keyFile)
	saltPath := filepath.Join(dir, saltFile)
	keyNewPath := filepath.Join(dir, stagingKeyFile)
	saltNewPath := filepath.Join(dir, stagingSaltFile)

	switch {
	case oldSource == "auto-key" && newSource == "auto-key":
		if err := backupFile(keyPath, keyPath+".bak"); err != nil {
			return err
		}
		if err := os.Rename(keyNewPath, keyPath); err != nil {
			return fmt.Errorf("rotation: promote staging key: %w", err)
		}

	case oldSource == "auto-key" && newSource == "passphrase":
		if err := backupFile(keyPath, keyPath+".bak"); err != nil {
			return err
		}
		if err := os.Rename(saltNewPath, saltPath); err != nil {
			return fmt.Errorf("rotation: promote staging salt: %w", err)
		}
		os.Remove(keyPath)

	case oldSource == "passphrase" && newSource == "passphrase":
		if err := backupFile(saltPath, saltPath+".bak"); err != nil {
			return err
		}
		if err := os.Rename(saltNewPath, saltPath); err != nil {
			return fmt.Errorf("rotation: promote staging salt: %w", err)
		}

	case oldSource == "passphrase" && newSource == "auto-key":
		if err := backupFile(saltPath, saltPath+".bak"); err != nil {
			return err
		}
		if err := os.Rename(keyNewPath, keyPath); err != nil {
			return fmt.Errorf("rotation: promote staging key: %w", err)
		}
		os.Remove(saltPath)
	}

	return nil
}

// CleanupStagingFiles removes any leftover .new staging files.
func CleanupStagingFiles(configDir string) {
	dir := dataDir(configDir)
	os.Remove(filepath.Join(dir, stagingKeyFile))
	os.Remove(filepath.Join(dir, stagingSaltFile))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func loadBase64Key(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("key file contains %d bytes, want 32", len(key))
	}
	return key, nil
}

func backupFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to back up
		}
		return fmt.Errorf("rotation: backup %s: %w", filepath.Base(src), err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return fmt.Errorf("rotation: write backup %s: %w", filepath.Base(dst), err)
	}
	return nil
}
