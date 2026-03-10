package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// CapabilityBroker validates that skills only access resources
// permitted by their manifest. All I/O is brokered through this
// layer — skills never get raw filesystem or network access.
type CapabilityBroker struct {
	mu     sync.RWMutex
	skills map[string]*skillCaps
	logFn  func(skill, message string) // optional log callback
}

type skillCaps struct {
	entry *SkillEntry
}

// NewCapabilityBroker creates a broker with no registered skills.
func NewCapabilityBroker() *CapabilityBroker {
	return &CapabilityBroker{
		skills: make(map[string]*skillCaps),
	}
}

// SetLogFunc sets a callback for skill log messages.
func (b *CapabilityBroker) SetLogFunc(fn func(skill, message string)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.logFn = fn
}

// RegisterSkill adds a skill's permissions to the broker.
func (b *CapabilityBroker) RegisterSkill(entry *SkillEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.skills[entry.Manifest.Name] = &skillCaps{entry: entry}
}

// UnregisterSkill removes a skill from the broker.
func (b *CapabilityBroker) UnregisterSkill(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.skills, name)
}

// FileRead reads a file on behalf of a skill, checking read permissions first.
func (b *CapabilityBroker) FileRead(skillName, path string) ([]byte, error) {
	if err := b.checkReadPath(skillName, path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("skill: %s: file read: %w", skillName, err)
	}
	return data, nil
}

// FileWrite writes a file on behalf of a skill, checking write permissions first.
func (b *CapabilityBroker) FileWrite(skillName, path string, data []byte) error {
	if err := b.checkWritePath(skillName, path); err != nil {
		return err
	}

	// Enforce max_file_size if configured.
	caps, err := b.getCaps(skillName)
	if err != nil {
		return err
	}
	maxSize := caps.entry.Manifest.Permissions.MaxFileSize
	if maxSize > 0 && int64(len(data)) > maxSize {
		return fmt.Errorf("skill: %s: file write denied: data size %d exceeds max_file_size %d",
			skillName, len(data), maxSize)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("skill: %s: file write: %w", skillName, err)
	}
	return nil
}

// Log logs a message on behalf of a skill.
func (b *CapabilityBroker) Log(skillName, message string) {
	b.mu.RLock()
	fn := b.logFn
	b.mu.RUnlock()
	if fn != nil {
		fn(skillName, message)
	}
}

// CheckNetwork returns nil if the skill is allowed network access to the given domain.
func (b *CapabilityBroker) CheckNetwork(skillName, domain string) error {
	caps, err := b.getCaps(skillName)
	if err != nil {
		return err
	}
	perms := caps.entry.Manifest.Permissions.Network
	if !perms.Allowed {
		return fmt.Errorf("skill: %s: network access denied (not permitted)", skillName)
	}
	if len(perms.Domains) > 0 {
		for _, d := range perms.Domains {
			if d == domain {
				return nil
			}
		}
		return fmt.Errorf("skill: %s: network access denied for domain %q", skillName, domain)
	}
	return nil
}

// CheckEnvVar returns nil if the skill is allowed to read the given env var.
func (b *CapabilityBroker) CheckEnvVar(skillName, varName string) error {
	caps, err := b.getCaps(skillName)
	if err != nil {
		return err
	}
	for _, allowed := range caps.entry.Manifest.Permissions.EnvVars {
		if allowed == varName {
			return nil
		}
	}
	return fmt.Errorf("skill: %s: env var %q access denied", skillName, varName)
}

func (b *CapabilityBroker) getCaps(skillName string) (*skillCaps, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	caps, ok := b.skills[skillName]
	if !ok {
		return nil, fmt.Errorf("skill: %s: not registered with broker", skillName)
	}
	return caps, nil
}

func (b *CapabilityBroker) checkReadPath(skillName, path string) error {
	caps, err := b.getCaps(skillName)
	if err != nil {
		return err
	}
	readPaths := caps.entry.Manifest.Permissions.Filesystem.ReadPaths
	if len(readPaths) == 0 {
		return fmt.Errorf("skill: %s: file read denied: no read_paths configured", skillName)
	}
	return checkPathAllowed(skillName, path, readPaths, "read")
}

func (b *CapabilityBroker) checkWritePath(skillName, path string) error {
	caps, err := b.getCaps(skillName)
	if err != nil {
		return err
	}
	writePaths := caps.entry.Manifest.Permissions.Filesystem.WritePaths
	if len(writePaths) == 0 {
		return fmt.Errorf("skill: %s: file write denied: no write_paths configured", skillName)
	}
	return checkPathAllowed(skillName, path, writePaths, "write")
}

// checkPathAllowed validates that path is under one of the allowed directories.
func checkPathAllowed(skillName, path string, allowed []string, op string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("skill: %s: resolve path: %w", skillName, err)
	}
	// Normalize to forward slashes for consistent comparison.
	absPath = filepath.ToSlash(absPath)

	for _, a := range allowed {
		absAllowed, err := filepath.Abs(a)
		if err != nil {
			continue
		}
		absAllowed = filepath.ToSlash(absAllowed)
		if absPath == absAllowed || strings.HasPrefix(absPath, absAllowed+"/") {
			return nil
		}
	}
	return fmt.Errorf("skill: %s: file %s denied: %q not under any allowed path", skillName, op, path)
}
