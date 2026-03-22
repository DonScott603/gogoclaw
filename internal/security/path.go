package security

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PathValidator enforces that file operations stay within allowed directories.
type PathValidator struct {
	allowedRoots []string
}

// NewPathValidator creates a validator that permits access only within the given root directories.
// All roots are cleaned and converted to absolute paths.
func NewPathValidator(roots []string) (*PathValidator, error) {
	cleaned := make([]string, 0, len(roots))
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("security: path: resolve root %q: %w", root, err)
		}
		cleaned = append(cleaned, filepath.Clean(abs))
	}
	return &PathValidator{allowedRoots: cleaned}, nil
}

// Validate checks that the given path resolves to a location within one of
// the allowed roots. It rejects path traversal attempts (e.g., "../../../etc/passwd").
func (v *PathValidator) Validate(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("security: path: resolve %q: %w", path, err)
	}
	cleaned := filepath.Clean(abs)

	// Resolve symlinks for existing paths to prevent symlink escapes.
	if _, err := os.Lstat(cleaned); err == nil {
		resolved, err := filepath.EvalSymlinks(cleaned)
		if err != nil {
			return "", fmt.Errorf("security: resolve symlinks: %w", err)
		}
		cleaned = resolved
	} else {
		// Path doesn't exist yet — resolve parent directory.
		parent := filepath.Dir(cleaned)
		if _, err := os.Lstat(parent); err == nil {
			resolvedParent, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return "", fmt.Errorf("security: resolve parent symlinks: %w", err)
			}
			cleaned = filepath.Join(resolvedParent, filepath.Base(cleaned))
		}
	}

	for _, root := range v.allowedRoots {
		if isWithin(cleaned, root) {
			return cleaned, nil
		}
	}
	return "", fmt.Errorf("security: path: %q is outside allowed directories", path)
}

// ValidateRelative resolves a relative path against a base directory and validates it.
func (v *PathValidator) ValidateRelative(base, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return v.Validate(rel)
	}
	return v.Validate(filepath.Join(base, rel))
}

// isWithin reports whether child is within parent (or equals parent).
func isWithin(child, parent string) bool {
	// Ensure parent ends with separator for prefix check to avoid
	// /workspace-evil matching /workspace.
	parentSlash := parent + string(filepath.Separator)
	return child == parent || strings.HasPrefix(child, parentSlash)
}
