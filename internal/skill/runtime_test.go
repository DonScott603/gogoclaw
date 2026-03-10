package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testdataPath(t *testing.T, name string) string {
	t.Helper()
	// Find the project root by walking up from cwd looking for go.mod.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "skills", "testdata", name)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}

func echoEntry(t *testing.T) *SkillEntry {
	t.Helper()
	dir := testdataPath(t, "echo")
	return &SkillEntry{
		Manifest: &Manifest{
			Name:        "echo",
			Version:     "1.0.0",
			Description: "Echo test skill",
			Tools: []ToolSpec{
				{Name: "echo", Description: "Echo back input"},
			},
			Permissions: Permissions{
				MaxExecTime: 10,
			},
		},
		Dir:      dir,
		WasmPath: filepath.Join(dir, "echo.wasm"),
	}
}

func slowEntry(t *testing.T) *SkillEntry {
	t.Helper()
	dir := testdataPath(t, "slow")
	return &SkillEntry{
		Manifest: &Manifest{
			Name:        "slow",
			Version:     "1.0.0",
			Description: "Slow test skill",
			Permissions: Permissions{
				MaxExecTime: 1, // 1 second timeout
			},
		},
		Dir:      dir,
		WasmPath: filepath.Join(dir, "slow.wasm"),
	}
}

func TestRuntimeLoadExecuteUnload(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close(ctx)

	entry := echoEntry(t)

	// Load.
	if err := rt.LoadSkill(ctx, entry); err != nil {
		t.Fatalf("LoadSkill: %v", err)
	}

	// Execute.
	args, _ := json.Marshal(map[string]string{"message": "hello world"})
	result, err := rt.Execute(ctx, "echo", args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var out struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("unmarshal result: %v (raw: %s)", err, string(result))
	}
	if out.Result != "echo: hello world" {
		t.Errorf("result = %q, want %q", out.Result, "echo: hello world")
	}

	// Unload.
	if err := rt.UnloadSkill(ctx, "echo"); err != nil {
		t.Fatalf("UnloadSkill: %v", err)
	}

	// Execute after unload should fail.
	_, err = rt.Execute(ctx, "echo", args)
	if err == nil {
		t.Fatal("expected error after unload")
	}
}

func TestRuntimeDoubleLoad(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close(ctx)

	entry := echoEntry(t)
	if err := rt.LoadSkill(ctx, entry); err != nil {
		t.Fatalf("first load: %v", err)
	}
	if err := rt.LoadSkill(ctx, entry); err == nil {
		t.Fatal("expected error on double load")
	}
}

func TestRuntimeUnloadNotLoaded(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close(ctx)

	if err := rt.UnloadSkill(ctx, "nonexistent"); err == nil {
		t.Fatal("expected error for unloading nonexistent skill")
	}
}

func TestRuntimeTimeout(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close(ctx)

	entry := slowEntry(t)
	if err := rt.LoadSkill(ctx, entry); err != nil {
		t.Fatalf("LoadSkill: %v", err)
	}

	start := time.Now()
	_, err = rt.Execute(ctx, "slow", []byte("{}"))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 5*time.Second {
		t.Errorf("timeout took %v, expected ~1s", elapsed)
	}
}

func TestRuntimeMultipleExecutions(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close(ctx)

	entry := echoEntry(t)
	if err := rt.LoadSkill(ctx, entry); err != nil {
		t.Fatalf("LoadSkill: %v", err)
	}

	// Execute the same skill multiple times.
	for i := 0; i < 3; i++ {
		args, _ := json.Marshal(map[string]string{"message": "test"})
		result, err := rt.Execute(ctx, "echo", args)
		if err != nil {
			t.Fatalf("Execute[%d]: %v", i, err)
		}
		var out struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal(result, &out); err != nil {
			t.Fatalf("unmarshal[%d]: %v", i, err)
		}
		if out.Result != "echo: test" {
			t.Errorf("Execute[%d] = %q, want %q", i, out.Result, "echo: test")
		}
	}
}
