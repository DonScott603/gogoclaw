// Package util provides small shared helpers used across internal packages.
package util

import (
	"os"
	"path/filepath"
)

// ExpandHome replaces a leading ~ with the user's home directory.
func ExpandHome(path string) string {
	if len(path) < 2 {
		return path
	}
	if path[0] == '~' && (path[1] == '/' || path[1] == '\\') {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
