package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfigMigrationFreshNoConfig(t *testing.T) {
	dir := t.TempDir()

	// No config.yaml exists — should succeed without error.
	if err := migrateConfig(dir); err != nil {
		t.Fatalf("migrateConfig on empty dir: %v", err)
	}
}

func TestConfigMigrationStampsVersion(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Write a v0 config (no config_version field).
	data := []byte("logging:\n  level: info\n")
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := migrateConfig(dir); err != nil {
		t.Fatalf("migrateConfig: %v", err)
	}

	// Read back and verify config_version is stamped.
	migrated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(migrated, &raw); err != nil {
		t.Fatalf("parse migrated config: %v", err)
	}

	v, ok := raw["config_version"]
	if !ok {
		t.Fatal("config_version not found in migrated config")
	}
	if version, ok := v.(int); !ok || version != 2 {
		t.Errorf("config_version = %v, want 2", v)
	}

	// Verify backup was created.
	backupPath := configPath + ".bak.v0"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Error("backup file not created")
	}
}

func TestConfigMigrationAlreadyCurrent(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Write a v2 config.
	data := []byte("config_version: 2\nlogging:\n  level: debug\n")
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := migrateConfig(dir); err != nil {
		t.Fatalf("migrateConfig: %v", err)
	}

	// No backup should be created for already-current config.
	backupPath := configPath + ".bak.v2"
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Error("backup should not be created when config is already current")
	}
}

func TestConfigMigrationPreservesExistingFields(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	data := []byte("logging:\n  level: debug\nworkspace:\n  base: /tmp/test\n")
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := migrateConfig(dir); err != nil {
		t.Fatalf("migrateConfig: %v", err)
	}

	migrated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var raw map[string]interface{}
	yaml.Unmarshal(migrated, &raw)

	logging, ok := raw["logging"].(map[string]interface{})
	if !ok {
		t.Fatal("logging section missing after migration")
	}
	if logging["level"] != "debug" {
		t.Errorf("logging.level = %v, want debug", logging["level"])
	}
}
