package security

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPathValidatorAllowsWithinRoot(t *testing.T) {
	root := t.TempDir()
	pv, err := NewPathValidator([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	validPath := filepath.Join(root, "subdir", "file.txt")
	result, err := pv.Validate(validPath)
	if err != nil {
		t.Fatalf("Validate(%q) error: %v", validPath, err)
	}
	if result != filepath.Clean(validPath) {
		t.Errorf("Validate result = %q, want %q", result, filepath.Clean(validPath))
	}
}

func TestPathValidatorRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	pv, err := NewPathValidator([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	outsidePath := "/etc/passwd"
	if runtime.GOOS == "windows" {
		outsidePath = "C:\\Windows\\System32\\config"
	}

	_, err = pv.Validate(outsidePath)
	if err == nil {
		t.Errorf("expected error for path outside root: %s", outsidePath)
	}
}

func TestPathValidatorRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	pv, err := NewPathValidator([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	traversalPath := filepath.Join(root, "..", "..", "etc", "passwd")
	_, err = pv.Validate(traversalPath)
	if err == nil {
		t.Errorf("expected error for traversal path: %s", traversalPath)
	}
}

func TestPathValidatorMultipleRoots(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	pv, err := NewPathValidator([]string{root1, root2})
	if err != nil {
		t.Fatal(err)
	}

	// Both should be allowed.
	tests := []struct {
		path    string
		wantErr bool
	}{
		{filepath.Join(root1, "file.txt"), false},
		{filepath.Join(root2, "file.txt"), false},
	}

	for _, tt := range tests {
		_, err := pv.Validate(tt.path)
		if (err != nil) != tt.wantErr {
			t.Errorf("Validate(%q) error = %v, wantErr = %v", tt.path, err, tt.wantErr)
		}
	}
}

func TestValidateRelative(t *testing.T) {
	root := t.TempDir()
	pv, err := NewPathValidator([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	result, err := pv.ValidateRelative(root, "subdir/file.txt")
	if err != nil {
		t.Fatalf("ValidateRelative error: %v", err)
	}
	expected := filepath.Join(root, "subdir", "file.txt")
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}

	// Traversal via relative path should fail.
	_, err = pv.ValidateRelative(root, "../../etc/passwd")
	if err == nil {
		t.Error("expected error for relative traversal")
	}
}

func TestPathValidatorRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}

	workspace := t.TempDir()
	outside := t.TempDir()

	// Create a file outside the workspace.
	outsideFile := filepath.Join(outside, "secret.txt")
	os.WriteFile(outsideFile, []byte("secret"), 0644)

	// Create a symlink inside the workspace pointing outside.
	symlink := filepath.Join(workspace, "escape")
	if err := os.Symlink(outside, symlink); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	pv, err := NewPathValidator([]string{workspace})
	if err != nil {
		t.Fatal(err)
	}

	// Symlink target is outside workspace — should be rejected.
	_, err = pv.Validate(filepath.Join(symlink, "secret.txt"))
	if err == nil {
		t.Error("expected error for symlink escape path")
	}
}
