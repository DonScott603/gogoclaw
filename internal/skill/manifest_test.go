package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseManifestValid(t *testing.T) {
	data := []byte(`
name: greeter
version: "1.0.0"
description: A friendly greeter skill
author: tester
tools:
  - name: greet
    description: Greet a user by name
    parameters: '{"type":"object","properties":{"name":{"type":"string"}}}'
permissions:
  filesystem:
    read_paths: ["/tmp"]
  network:
    allowed: false
  max_execution_time: 30
`)
	m, err := ParseManifestBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "greeter" {
		t.Errorf("name = %q, want %q", m.Name, "greeter")
	}
	if m.Version != "1.0.0" {
		t.Errorf("version = %q, want %q", m.Version, "1.0.0")
	}
	if m.Author != "tester" {
		t.Errorf("author = %q, want %q", m.Author, "tester")
	}
	if len(m.Tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(m.Tools))
	}
	if m.Tools[0].Name != "greet" {
		t.Errorf("tools[0].name = %q, want %q", m.Tools[0].Name, "greet")
	}
	if m.Permissions.MaxExecTime != 30 {
		t.Errorf("max_execution_time = %d, want 30", m.Permissions.MaxExecTime)
	}
	if len(m.Permissions.Filesystem.ReadPaths) != 1 {
		t.Errorf("read_paths count = %d, want 1", len(m.Permissions.Filesystem.ReadPaths))
	}
}

func TestParseManifestMissingName(t *testing.T) {
	data := []byte(`
version: "1.0.0"
description: No name skill
`)
	_, err := ParseManifestBytes(data)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestParseManifestMissingVersion(t *testing.T) {
	data := []byte(`
name: foo
description: No version
`)
	_, err := ParseManifestBytes(data)
	if err == nil {
		t.Fatal("expected error for missing version")
	}
}

func TestParseManifestMissingDescription(t *testing.T) {
	data := []byte(`
name: foo
version: "1.0.0"
`)
	_, err := ParseManifestBytes(data)
	if err == nil {
		t.Fatal("expected error for missing description")
	}
}

func TestParseManifestToolMissingName(t *testing.T) {
	data := []byte(`
name: foo
version: "1.0.0"
description: A skill
tools:
  - description: tool without name
`)
	_, err := ParseManifestBytes(data)
	if err == nil {
		t.Fatal("expected error for tool missing name")
	}
}

func TestParseManifestInvalidYAML(t *testing.T) {
	data := []byte(`{{{not yaml`)
	_, err := ParseManifestBytes(data)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParseManifestFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	content := []byte(`
name: file-skill
version: "0.1.0"
description: Loaded from file
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ParseManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "file-skill" {
		t.Errorf("name = %q, want %q", m.Name, "file-skill")
	}
}

func TestParseManifestFileNotFound(t *testing.T) {
	_, err := ParseManifest("/nonexistent/manifest.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
