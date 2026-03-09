package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DonScott603/gogoclaw/internal/security"
)

// RegisterFileTools registers file_read, file_write, file_list, and file_search.
func RegisterFileTools(d *Dispatcher, pathValidator *security.PathValidator, workspaceBase string) {
	d.Register(ToolDef{
		Name:        "file_read",
		Description: "Read the contents of a file within the workspace.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path relative to workspace"}
			},
			"required": ["path"]
		}`),
		Fn: fileReadFn(pathValidator, workspaceBase),
	})

	d.Register(ToolDef{
		Name:        "file_write",
		Description: "Write content to a file within the workspace.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path relative to workspace"},
				"content": {"type": "string", "description": "Content to write"}
			},
			"required": ["path", "content"]
		}`),
		Fn: fileWriteFn(pathValidator, workspaceBase),
	})

	d.Register(ToolDef{
		Name:        "file_list",
		Description: "List files and directories within a workspace path.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Directory path relative to workspace (default: root)"}
			}
		}`),
		Fn: fileListFn(pathValidator, workspaceBase),
	})

	d.Register(ToolDef{
		Name:        "file_search",
		Description: "Search for files matching a pattern within the workspace.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {"type": "string", "description": "Glob pattern to match (e.g., '*.csv')"},
				"path": {"type": "string", "description": "Directory to search in (default: workspace root)"}
			},
			"required": ["pattern"]
		}`),
		Fn: fileSearchFn(pathValidator, workspaceBase),
	})
}

type fileReadArgs struct {
	Path string `json:"path"`
}

func fileReadFn(pv *security.PathValidator, base string) ToolFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a fileReadArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("file_read: parse args: %w", err)
		}
		absPath, err := pv.ValidateRelative(base, a.Path)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			return "", fmt.Errorf("file_read: %w", err)
		}
		return string(data), nil
	}
}

type fileWriteArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func fileWriteFn(pv *security.PathValidator, base string) ToolFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a fileWriteArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("file_write: parse args: %w", err)
		}
		absPath, err := pv.ValidateRelative(base, a.Path)
		if err != nil {
			return "", err
		}
		dir := filepath.Dir(absPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("file_write: create dir: %w", err)
		}
		if err := os.WriteFile(absPath, []byte(a.Content), 0o644); err != nil {
			return "", fmt.Errorf("file_write: %w", err)
		}
		return fmt.Sprintf("Wrote %d bytes to %s", len(a.Content), a.Path), nil
	}
}

type fileListArgs struct {
	Path string `json:"path"`
}

func fileListFn(pv *security.PathValidator, base string) ToolFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a fileListArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("file_list: parse args: %w", err)
		}
		dir := a.Path
		if dir == "" {
			dir = "."
		}
		absPath, err := pv.ValidateRelative(base, dir)
		if err != nil {
			return "", err
		}
		entries, err := os.ReadDir(absPath)
		if err != nil {
			return "", fmt.Errorf("file_list: %w", err)
		}
		var b strings.Builder
		for _, e := range entries {
			suffix := ""
			if e.IsDir() {
				suffix = "/"
			}
			b.WriteString(e.Name() + suffix + "\n")
		}
		return b.String(), nil
	}
}

type fileSearchArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

func fileSearchFn(pv *security.PathValidator, base string) ToolFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a fileSearchArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("file_search: parse args: %w", err)
		}
		dir := a.Path
		if dir == "" {
			dir = "."
		}
		absDir, err := pv.ValidateRelative(base, dir)
		if err != nil {
			return "", err
		}
		var matches []string
		err = filepath.WalkDir(absDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip errors
			}
			if d.IsDir() {
				return nil
			}
			matched, _ := filepath.Match(a.Pattern, d.Name())
			if matched {
				rel, _ := filepath.Rel(base, path)
				matches = append(matches, rel)
			}
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("file_search: %w", err)
		}
		if len(matches) == 0 {
			return "No files found matching pattern.", nil
		}
		return strings.Join(matches, "\n"), nil
	}
}
