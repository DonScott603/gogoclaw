package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestShellExecTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	d := NewDispatcher(0)
	RegisterShellTool(d, nil, 100*time.Millisecond) // very short timeout

	args, _ := json.Marshal(shellExecArgs{Command: "sleep 5"})
	results := d.Dispatch(context.Background(), []ToolCallRequest{
		{ID: "call_1", Name: "shell_exec", Arguments: args},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	result := results[0]
	if !strings.Contains(result.Content, "timed out") {
		t.Errorf("expected timeout message, got: %q", result.Content)
	}
}

func TestShellExecNormalCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows")
	}

	d := NewDispatcher(0)
	RegisterShellTool(d, nil, 10*time.Second) // generous timeout

	args, _ := json.Marshal(shellExecArgs{Command: "echo hello"})
	results := d.Dispatch(context.Background(), []ToolCallRequest{
		{ID: "call_1", Name: "shell_exec", Arguments: args},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	result := results[0]
	if result.IsError {
		t.Errorf("expected no error, got: %q", result.Content)
	}
	if !strings.Contains(result.Content, "hello") {
		t.Errorf("expected output to contain 'hello', got: %q", result.Content)
	}
}

func TestShellExecConfirmDenied(t *testing.T) {
	d := NewDispatcher(0)
	deny := func(cmd string) bool { return false }
	RegisterShellTool(d, deny, 30*time.Second)

	args, _ := json.Marshal(shellExecArgs{Command: "echo denied"})
	results := d.Dispatch(context.Background(), []ToolCallRequest{
		{ID: "call_1", Name: "shell_exec", Arguments: args},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !strings.Contains(results[0].Content, "denied") {
		t.Errorf("expected denial message, got: %q", results[0].Content)
	}
}

func TestShellExecEmptyCommand(t *testing.T) {
	d := NewDispatcher(0)
	RegisterShellTool(d, nil, 30*time.Second)

	args, _ := json.Marshal(shellExecArgs{Command: ""})
	results := d.Dispatch(context.Background(), []ToolCallRequest{
		{ID: "call_1", Name: "shell_exec", Arguments: args},
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].IsError {
		t.Error("expected error for empty command")
	}
}
