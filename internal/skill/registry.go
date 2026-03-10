package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// SkillEntry is a validated, loaded skill ready for use.
type SkillEntry struct {
	Manifest *Manifest
	Dir      string // directory containing the skill
	WasmPath string // path to the .wasm binary
}

// Registry scans and manages installed skills.
type Registry struct {
	baseDir string
	skills  map[string]*SkillEntry
}

// NewRegistry creates a registry that scans skillsDir for installed skills.
// Each subdirectory should contain a manifest.yaml and a .wasm file.
func NewRegistry(skillsDir string) (*Registry, error) {
	r := &Registry{
		baseDir: skillsDir,
		skills:  make(map[string]*SkillEntry),
	}
	if err := r.scan(); err != nil {
		return nil, err
	}
	return r, nil
}

// ListSkills returns all successfully loaded skills.
func (r *Registry) ListSkills() []*SkillEntry {
	entries := make([]*SkillEntry, 0, len(r.skills))
	for _, e := range r.skills {
		entries = append(entries, e)
	}
	return entries
}

// GetSkill returns a skill by name, or nil if not found.
func (r *Registry) GetSkill(name string) *SkillEntry {
	return r.skills[name]
}

func (r *Registry) scan() error {
	entries, err := os.ReadDir(r.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no skills directory is fine
		}
		return fmt.Errorf("skill: scan skills dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(r.baseDir, entry.Name())
		se, err := r.loadSkill(dir)
		if err != nil {
			log.Printf("skill: skipping %s: %v", entry.Name(), err)
			continue
		}
		r.skills[se.Manifest.Name] = se
	}
	return nil
}

func (r *Registry) loadSkill(dir string) (*SkillEntry, error) {
	manifestPath := filepath.Join(dir, "manifest.yaml")
	m, err := ParseManifest(manifestPath)
	if err != nil {
		return nil, err
	}

	wasmPath, err := findWasm(dir)
	if err != nil {
		return nil, err
	}

	if m.Hash != "" {
		if err := verifyHash(wasmPath, m.Hash); err != nil {
			return nil, err
		}
	}

	return &SkillEntry{
		Manifest: m,
		Dir:      dir,
		WasmPath: wasmPath,
	}, nil
}

// findWasm locates the single .wasm file in a skill directory.
func findWasm(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("skill: read dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".wasm") {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("skill: no .wasm file found in %s", dir)
}

// verifyHash checks a file's SHA-256 against an expected hex digest.
func verifyHash(path, expected string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("skill: read wasm for hash: %w", err)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != expected {
		return fmt.Errorf("skill: hash mismatch for %s: expected %s, got %s", filepath.Base(path), expected, got)
	}
	return nil
}
