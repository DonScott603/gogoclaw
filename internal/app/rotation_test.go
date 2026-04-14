package app

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/DonScott603/gogoclaw/internal/storage"
)

func tempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	return dir
}

func writeBase64Key(t *testing.T, path string, key []byte) {
	t.Helper()
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
}

func TestResolveNewEncryptorGeneratesKey(t *testing.T) {
	dir := tempConfigDir(t)

	enc, source, err := ResolveNewEncryptor(dir, "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enc == nil {
		t.Fatal("expected non-nil encryptor")
	}
	if source != "auto-key" {
		t.Fatalf("expected source 'auto-key', got %q", source)
	}

	// Staging file should exist.
	keyPath := filepath.Join(dir, "data", stagingKeyFile)
	if !fileExists(keyPath) {
		t.Fatal("expected staging key file to be created")
	}
}

func TestResolveNewEncryptorGeneratesSalt(t *testing.T) {
	dir := tempConfigDir(t)

	enc, source, err := ResolveNewEncryptor(dir, "my-new-passphrase", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enc == nil {
		t.Fatal("expected non-nil encryptor")
	}
	if source != "passphrase" {
		t.Fatalf("expected source 'passphrase', got %q", source)
	}

	saltPath := filepath.Join(dir, "data", stagingSaltFile)
	if !fileExists(saltPath) {
		t.Fatal("expected staging salt file to be created")
	}

	// Key file should NOT exist.
	keyPath := filepath.Join(dir, "data", stagingKeyFile)
	if fileExists(keyPath) {
		t.Fatal("staging key file should not exist in passphrase mode")
	}
}

func TestResolveNewEncryptorReusesStaged(t *testing.T) {
	dir := tempConfigDir(t)

	// Create a staged key manually.
	key, err := storage.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keyPath := filepath.Join(dir, "data", stagingKeyFile)
	writeBase64Key(t, keyPath, key)

	enc, source, err := ResolveNewEncryptor(dir, "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enc == nil {
		t.Fatal("expected non-nil encryptor")
	}
	if source != "auto-key (staged)" {
		t.Fatalf("expected source 'auto-key (staged)', got %q", source)
	}

	// Verify it loaded the same key by checking the encryptor works with our key.
	expected, _ := storage.NewEncryptorFromKey(key)
	if !enc.KeyEquals(expected) {
		t.Fatal("staged key was not reused — encryptors differ")
	}
}

func TestResolveNewEncryptorReusesStSalt(t *testing.T) {
	dir := tempConfigDir(t)

	// Create a staged salt manually.
	salt, err := storage.GenerateSalt()
	if err != nil {
		t.Fatalf("generate salt: %v", err)
	}
	saltPath := filepath.Join(dir, "data", stagingSaltFile)
	if err := os.WriteFile(saltPath, salt, 0o600); err != nil {
		t.Fatalf("write salt: %v", err)
	}

	passphrase := "test-passphrase"
	enc, source, err := ResolveNewEncryptor(dir, passphrase, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enc == nil {
		t.Fatal("expected non-nil encryptor")
	}
	if source != "passphrase (staged)" {
		t.Fatalf("expected source 'passphrase (staged)', got %q", source)
	}

	// Derive the same key and verify.
	expected, _ := storage.NewEncryptorFromPassphrase(passphrase, salt)
	if !enc.KeyEquals(expected) {
		t.Fatal("staged salt was not reused — encryptors differ")
	}
}

func TestResolveNewEncryptorStagedSaltNoPassphrase(t *testing.T) {
	dir := tempConfigDir(t)

	salt, _ := storage.GenerateSalt()
	saltPath := filepath.Join(dir, "data", stagingSaltFile)
	os.WriteFile(saltPath, salt, 0o600)

	_, _, err := ResolveNewEncryptor(dir, "", false)
	if err == nil {
		t.Fatal("expected error when staged salt exists but no passphrase")
	}
	if got := err.Error(); got != "rotation: staged salt file found but no --new-passphrase provided — this is likely a re-run; provide the same passphrase used in the previous attempt" {
		t.Fatalf("unexpected error message: %s", got)
	}
}

func TestResolveNewEncryptorDualStagingConflict(t *testing.T) {
	dir := tempConfigDir(t)

	// Create both staging files.
	key, _ := storage.GenerateKey()
	writeBase64Key(t, filepath.Join(dir, "data", stagingKeyFile), key)

	salt, _ := storage.GenerateSalt()
	os.WriteFile(filepath.Join(dir, "data", stagingSaltFile), salt, 0o600)

	_, _, err := ResolveNewEncryptor(dir, "", false)
	if err == nil {
		t.Fatal("expected error for dual staging conflict")
	}
	errMsg := err.Error()
	if !contains(errMsg, stagingKeyFile) || !contains(errMsg, stagingSaltFile) {
		t.Fatalf("error should mention both staging files, got: %s", errMsg)
	}
}

func TestResolveNewEncryptorDryRunNoFiles(t *testing.T) {
	dir := tempConfigDir(t)

	enc, source, err := ResolveNewEncryptor(dir, "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enc == nil {
		t.Fatal("expected non-nil encryptor")
	}
	if source != "auto-key (dry-run)" {
		t.Fatalf("expected source 'auto-key (dry-run)', got %q", source)
	}

	// No staging files should be created.
	keyPath := filepath.Join(dir, "data", stagingKeyFile)
	saltPath := filepath.Join(dir, "data", stagingSaltFile)
	if fileExists(keyPath) {
		t.Fatal("dry-run should not create staging key file")
	}
	if fileExists(saltPath) {
		t.Fatal("dry-run should not create staging salt file")
	}
}

func TestResolveNewEncryptorDryRunExistingStaging(t *testing.T) {
	dir := tempConfigDir(t)

	// Create a staged key.
	key, _ := storage.GenerateKey()
	keyPath := filepath.Join(dir, "data", stagingKeyFile)
	writeBase64Key(t, keyPath, key)

	enc, source, err := ResolveNewEncryptor(dir, "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enc == nil {
		t.Fatal("expected non-nil encryptor")
	}
	if source != "auto-key (staged)" {
		t.Fatalf("expected source 'auto-key (staged)', got %q", source)
	}

	// Verify file was not modified (same content).
	expected, _ := storage.NewEncryptorFromKey(key)
	if !enc.KeyEquals(expected) {
		t.Fatal("dry-run should not modify staged key")
	}
}

func TestPromoteKeyFilesAutoToAuto(t *testing.T) {
	dir := tempConfigDir(t)
	d := filepath.Join(dir, "data")

	// Create old key and staging key.
	oldKey, _ := storage.GenerateKey()
	writeBase64Key(t, filepath.Join(d, keyFile), oldKey)

	newKey, _ := storage.GenerateKey()
	writeBase64Key(t, filepath.Join(d, stagingKeyFile), newKey)

	if err := PromoteKeyFiles(dir, "auto-key", "auto-key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Backup should exist.
	if !fileExists(filepath.Join(d, keyFile+".bak")) {
		t.Fatal("expected backup file")
	}

	// Staging should be gone, final should have new key.
	if fileExists(filepath.Join(d, stagingKeyFile)) {
		t.Fatal("staging file should be gone after promotion")
	}

	finalKey, err := loadBase64Key(filepath.Join(d, keyFile))
	if err != nil {
		t.Fatalf("read final key: %v", err)
	}
	finalEnc, _ := storage.NewEncryptorFromKey(finalKey)
	newEnc, _ := storage.NewEncryptorFromKey(newKey)
	if !finalEnc.KeyEquals(newEnc) {
		t.Fatal("final key does not match new key")
	}
}

func TestPromoteKeyFilesAutoToPassphrase(t *testing.T) {
	dir := tempConfigDir(t)
	d := filepath.Join(dir, "data")

	// Create old key and staging salt.
	oldKey, _ := storage.GenerateKey()
	writeBase64Key(t, filepath.Join(d, keyFile), oldKey)

	salt, _ := storage.GenerateSalt()
	os.WriteFile(filepath.Join(d, stagingSaltFile), salt, 0o600)

	if err := PromoteKeyFiles(dir, "auto-key", "passphrase"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !fileExists(filepath.Join(d, keyFile+".bak")) {
		t.Fatal("expected key backup")
	}
	if fileExists(filepath.Join(d, keyFile)) {
		t.Fatal("old key file should be deleted")
	}
	if !fileExists(filepath.Join(d, saltFile)) {
		t.Fatal("salt should be promoted to final")
	}
	if fileExists(filepath.Join(d, stagingSaltFile)) {
		t.Fatal("staging salt should be gone")
	}
}

func TestPromoteKeyFilesOverwritesBak(t *testing.T) {
	dir := tempConfigDir(t)
	d := filepath.Join(dir, "data")

	// Create old key, staging key, and pre-existing backup.
	oldKey, _ := storage.GenerateKey()
	writeBase64Key(t, filepath.Join(d, keyFile), oldKey)
	os.WriteFile(filepath.Join(d, keyFile+".bak"), []byte("old-backup"), 0o600)

	newKey, _ := storage.GenerateKey()
	writeBase64Key(t, filepath.Join(d, stagingKeyFile), newKey)

	if err := PromoteKeyFiles(dir, "auto-key", "auto-key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Backup should be overwritten with old key, not "old-backup".
	bakData, _ := os.ReadFile(filepath.Join(d, keyFile+".bak"))
	if string(bakData) == "old-backup" {
		t.Fatal("backup should be overwritten with old key data")
	}
}

func TestCleanupStagingFiles(t *testing.T) {
	dir := tempConfigDir(t)
	d := filepath.Join(dir, "data")

	// Create both staging files.
	os.WriteFile(filepath.Join(d, stagingKeyFile), []byte("key"), 0o600)
	os.WriteFile(filepath.Join(d, stagingSaltFile), []byte("salt"), 0o600)

	CleanupStagingFiles(dir)

	if fileExists(filepath.Join(d, stagingKeyFile)) {
		t.Fatal("staging key file should be cleaned up")
	}
	if fileExists(filepath.Join(d, stagingSaltFile)) {
		t.Fatal("staging salt file should be cleaned up")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
