package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func setupSkillDir(t *testing.T, base, name, manifest string, wasmContent []byte) {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if wasmContent != nil {
		if err := os.WriteFile(filepath.Join(dir, name+".wasm"), wasmContent, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRegistryFindsSkills(t *testing.T) {
	base := t.TempDir()
	setupSkillDir(t, base, "greeter", `
name: greeter
version: "1.0.0"
description: Greets users
tools:
  - name: greet
    description: Say hello
`, []byte("fake wasm"))

	setupSkillDir(t, base, "calculator", `
name: calculator
version: "2.0.0"
description: Does math
tools:
  - name: add
    description: Add numbers
`, []byte("fake wasm 2"))

	r, err := NewRegistry(base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	skills := r.ListSkills()
	if len(skills) != 2 {
		t.Fatalf("skills count = %d, want 2", len(skills))
	}

	g := r.GetSkill("greeter")
	if g == nil {
		t.Fatal("greeter not found")
	}
	if g.Manifest.Version != "1.0.0" {
		t.Errorf("greeter version = %q, want %q", g.Manifest.Version, "1.0.0")
	}

	c := r.GetSkill("calculator")
	if c == nil {
		t.Fatal("calculator not found")
	}
}

func TestRegistryIgnoresBadManifest(t *testing.T) {
	base := t.TempDir()

	// Good skill.
	setupSkillDir(t, base, "good", `
name: good
version: "1.0.0"
description: Works fine
`, []byte("wasm"))

	// Bad skill — missing required fields.
	setupSkillDir(t, base, "bad", `
version: "1.0.0"
`, []byte("wasm"))

	r, err := NewRegistry(base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.ListSkills()) != 1 {
		t.Errorf("skills count = %d, want 1 (bad should be skipped)", len(r.ListSkills()))
	}
	if r.GetSkill("good") == nil {
		t.Error("good skill not found")
	}
}

func TestRegistryIgnoresMissingWasm(t *testing.T) {
	base := t.TempDir()
	// Skill directory with manifest but no .wasm file.
	setupSkillDir(t, base, "nowasm", `
name: nowasm
version: "1.0.0"
description: No wasm binary
`, nil)

	r, err := NewRegistry(base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.ListSkills()) != 0 {
		t.Errorf("skills count = %d, want 0", len(r.ListSkills()))
	}
}

func TestRegistryHashVerification(t *testing.T) {
	base := t.TempDir()
	wasmBytes := []byte("real wasm content")
	sum := sha256.Sum256(wasmBytes)
	hash := hex.EncodeToString(sum[:])

	setupSkillDir(t, base, "hashed", `
name: hashed
version: "1.0.0"
description: Has hash
hash: "`+hash+`"
`, wasmBytes)

	r, err := NewRegistry(base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.GetSkill("hashed") == nil {
		t.Error("hashed skill not found — hash should match")
	}
}

func TestRegistryHashMismatch(t *testing.T) {
	base := t.TempDir()
	setupSkillDir(t, base, "badhash", `
name: badhash
version: "1.0.0"
description: Bad hash
hash: "0000000000000000000000000000000000000000000000000000000000000000"
`, []byte("some content"))

	r, err := NewRegistry(base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.GetSkill("badhash") != nil {
		t.Error("badhash should be rejected due to hash mismatch")
	}
}

func TestRegistryNonexistentDir(t *testing.T) {
	r, err := NewRegistry("/nonexistent/skills")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.ListSkills()) != 0 {
		t.Errorf("skills count = %d, want 0", len(r.ListSkills()))
	}
}

func TestRegistryGetSkillNotFound(t *testing.T) {
	base := t.TempDir()
	r, err := NewRegistry(base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.GetSkill("nonexistent") != nil {
		t.Error("expected nil for nonexistent skill")
	}
}

func TestRegistryIgnoresFiles(t *testing.T) {
	base := t.TempDir()
	// Create a regular file (not a directory) in the skills dir.
	if err := os.WriteFile(filepath.Join(base, "readme.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := NewRegistry(base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.ListSkills()) != 0 {
		t.Errorf("skills count = %d, want 0", len(r.ListSkills()))
	}
}
