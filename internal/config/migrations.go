package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ConfigMigration describes a single config format migration.
type ConfigMigration struct {
	Version     int
	Description string
	Migrate     func(raw map[string]interface{}) error
}

// configMigrations returns the ordered list of config migrations.
func configMigrations() []ConfigMigration {
	return []ConfigMigration{
		{
			Version:     1,
			Description: "Stamp config_version field",
			Migrate: func(raw map[string]interface{}) error {
				raw["config_version"] = 1
				return nil
			},
		},
		{
			Version:     2,
			Description: "Stamp version for encryption support",
			Migrate: func(raw map[string]interface{}) error {
				raw["config_version"] = 2
				return nil
			},
		},
	}
}

// migrateConfig checks the config file version and runs any outstanding
// migrations. Before migrating, it backs up to config.yaml.bak.v{N}.
func migrateConfig(configDir string) error {
	configPath := filepath.Join(configDir, "config.yaml")

	data, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return nil // no config file yet — nothing to migrate
	}
	if err != nil {
		return fmt.Errorf("config: migration: read config: %w", err)
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("config: migration: parse config: %w", err)
	}
	if raw == nil {
		raw = make(map[string]interface{})
	}

	// Determine current version.
	currentVersion := 0
	if v, ok := raw["config_version"]; ok {
		switch val := v.(type) {
		case int:
			currentVersion = val
		case float64:
			currentVersion = int(val)
		}
	}

	migrations := configMigrations()
	needsMigration := false
	for _, m := range migrations {
		if m.Version > currentVersion {
			needsMigration = true
			break
		}
	}

	if !needsMigration {
		return nil
	}

	// Backup before migrating.
	backupPath := fmt.Sprintf("%s.bak.v%d", configPath, currentVersion)
	if err := os.WriteFile(backupPath, data, 0o644); err != nil {
		return fmt.Errorf("config: migration: backup to %s: %w", backupPath, err)
	}
	log.Printf("config: backed up to %s", backupPath)

	// Run migrations.
	for _, m := range migrations {
		if m.Version <= currentVersion {
			continue
		}
		log.Printf("config: running migration %d: %s", m.Version, m.Description)
		if err := m.Migrate(raw); err != nil {
			return fmt.Errorf("config: migration %d (%s) failed: %w", m.Version, m.Description, err)
		}
		currentVersion = m.Version
	}

	// Write the migrated config back.
	out, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("config: migration: marshal: %w", err)
	}
	if err := os.WriteFile(configPath, out, 0o644); err != nil {
		return fmt.Errorf("config: migration: write config: %w", err)
	}
	log.Printf("config: migrated to version %d", currentVersion)

	return nil
}
