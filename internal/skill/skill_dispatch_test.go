package skill

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/DonScott603/gogoclaw/internal/tools"
)

func textSkillEntry(t *testing.T) *SkillEntry {
	t.Helper()
	dir := builtinSkillPath(t, "text")
	return &SkillEntry{
		Manifest: &Manifest{
			Name:        "text",
			Version:     "1.0.0",
			Description: "Text manipulation utilities",
			Tools: []ToolSpec{
				{Name: "text_uppercase", Description: "Convert text to uppercase",
					Parameters: `{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`},
				{Name: "text_lowercase", Description: "Convert text to lowercase",
					Parameters: `{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`},
				{Name: "text_wordcount", Description: "Count words",
					Parameters: `{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`},
				{Name: "text_reverse", Description: "Reverse text",
					Parameters: `{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`},
			},
			Permissions: Permissions{MaxExecTime: 5},
		},
		Dir:      dir,
		WasmPath: filepath.Join(dir, "text.wasm"),
	}
}

// newTextSkillHarness sets up a WASM runtime, loads the text skill, and
// returns a ready-to-use dispatcher. The runtime is cleaned up via t.Cleanup.
func newTextSkillHarness(t *testing.T) *tools.Dispatcher {
	t.Helper()
	ctx := context.Background()
	rt, err := NewRuntime(ctx)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	t.Cleanup(func() { rt.Close(ctx) })

	entry := textSkillEntry(t)
	reg := &Registry{skills: map[string]*SkillEntry{"text": entry}}
	sd := NewSkillDispatcher(reg, rt)

	d := tools.NewDispatcher(0)
	if err := sd.RegisterSkillTools(ctx, d); err != nil {
		t.Fatalf("RegisterSkillTools: %v", err)
	}
	return d
}

func TestSkillDispatcherRegistersTools(t *testing.T) {
	d := newTextSkillHarness(t)

	defs := d.Definitions()
	if len(defs) != 4 {
		t.Fatalf("expected 4 tool definitions, got %d", len(defs))
	}

	names := make(map[string]bool)
	for _, def := range defs {
		names[def.Function.Name] = true
	}
	for _, expected := range []string{"text_uppercase", "text_lowercase", "text_wordcount", "text_reverse"} {
		if !names[expected] {
			t.Errorf("missing tool: %s", expected)
		}
	}
}

func TestSkillDispatchTextTools(t *testing.T) {
	d := newTextSkillHarness(t)

	tests := []struct {
		name     string
		tool     string
		input    string
		expected string
	}{
		{"uppercase", "text_uppercase", "hello world", "HELLO WORLD"},
		{"lowercase", "text_lowercase", "HELLO WORLD", "hello world"},
		{"wordcount", "text_wordcount", "one two three four five", "5"},
		{"wordcount_single", "text_wordcount", "hello", "1"},
		{"reverse", "text_reverse", "abcdef", "fedcba"},
		{"reverse_palindrome", "text_reverse", "racecar", "racecar"},
		{"uppercase_empty", "text_uppercase", "", ""},
		{"wordcount_empty", "text_wordcount", "", "0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args, _ := json.Marshal(map[string]string{"text": tc.input})
			results := d.Dispatch(context.Background(), []tools.ToolCallRequest{
				{ID: "1", Name: tc.tool, Arguments: args},
			})

			if results[0].IsError {
				t.Fatalf("unexpected error: %s", results[0].Content)
			}
			if results[0].Content != tc.expected {
				t.Errorf("got %q, want %q", results[0].Content, tc.expected)
			}
		})
	}
}

func TestSkillDispatcherListSkillTools(t *testing.T) {
	entry := textSkillEntry(t)
	reg := &Registry{skills: map[string]*SkillEntry{"text": entry}}
	sd := NewSkillDispatcher(reg, nil)

	listed := sd.ListSkillTools()
	if len(listed) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(listed))
	}
	if listed[0].SkillName != "text" {
		t.Errorf("skill name = %q, want %q", listed[0].SkillName, "text")
	}
}
