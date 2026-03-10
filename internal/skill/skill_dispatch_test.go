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

func TestSkillDispatcherRegistersTools(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close(ctx)

	entry := textSkillEntry(t)
	reg := &Registry{skills: map[string]*SkillEntry{"text": entry}}
	sd := NewSkillDispatcher(reg, rt)

	d := tools.NewDispatcher(0)
	if err := sd.RegisterSkillTools(ctx, d); err != nil {
		t.Fatalf("RegisterSkillTools: %v", err)
	}

	defs := d.Definitions()
	if len(defs) != 4 {
		t.Fatalf("expected 4 tool definitions, got %d", len(defs))
	}

	// Verify tool names are registered.
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

func TestSkillDispatchUppercase(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close(ctx)

	entry := textSkillEntry(t)
	reg := &Registry{skills: map[string]*SkillEntry{"text": entry}}
	sd := NewSkillDispatcher(reg, rt)

	d := tools.NewDispatcher(0)
	if err := sd.RegisterSkillTools(ctx, d); err != nil {
		t.Fatalf("RegisterSkillTools: %v", err)
	}

	args, _ := json.Marshal(map[string]string{"text": "hello world"})
	results := d.Dispatch(ctx, []tools.ToolCallRequest{
		{ID: "1", Name: "text_uppercase", Arguments: args},
	})

	if results[0].IsError {
		t.Fatalf("unexpected error: %s", results[0].Content)
	}
	if results[0].Content != "HELLO WORLD" {
		t.Errorf("result = %q, want %q", results[0].Content, "HELLO WORLD")
	}
}

func TestSkillDispatchWordcount(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close(ctx)

	entry := textSkillEntry(t)
	reg := &Registry{skills: map[string]*SkillEntry{"text": entry}}
	sd := NewSkillDispatcher(reg, rt)

	d := tools.NewDispatcher(0)
	if err := sd.RegisterSkillTools(ctx, d); err != nil {
		t.Fatalf("RegisterSkillTools: %v", err)
	}

	args, _ := json.Marshal(map[string]string{"text": "one two three four five"})
	results := d.Dispatch(ctx, []tools.ToolCallRequest{
		{ID: "1", Name: "text_wordcount", Arguments: args},
	})

	if results[0].IsError {
		t.Fatalf("unexpected error: %s", results[0].Content)
	}
	if results[0].Content != "5" {
		t.Errorf("result = %q, want %q", results[0].Content, "5")
	}
}

func TestSkillDispatchReverse(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close(ctx)

	entry := textSkillEntry(t)
	reg := &Registry{skills: map[string]*SkillEntry{"text": entry}}
	sd := NewSkillDispatcher(reg, rt)

	d := tools.NewDispatcher(0)
	if err := sd.RegisterSkillTools(ctx, d); err != nil {
		t.Fatalf("RegisterSkillTools: %v", err)
	}

	args, _ := json.Marshal(map[string]string{"text": "abcdef"})
	results := d.Dispatch(ctx, []tools.ToolCallRequest{
		{ID: "1", Name: "text_reverse", Arguments: args},
	})

	if results[0].IsError {
		t.Fatalf("unexpected error: %s", results[0].Content)
	}
	if results[0].Content != "fedcba" {
		t.Errorf("result = %q, want %q", results[0].Content, "fedcba")
	}
}

func TestSkillDispatcherListSkillTools(t *testing.T) {
	entry := textSkillEntry(t)
	reg := &Registry{skills: map[string]*SkillEntry{"text": entry}}
	sd := NewSkillDispatcher(reg, nil) // runtime not needed for listing

	listed := sd.ListSkillTools()
	if len(listed) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(listed))
	}
	if listed[0].SkillName != "text" {
		t.Errorf("skill name = %q, want %q", listed[0].SkillName, "text")
	}
}
