package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatchUnknownTool(t *testing.T) {
	d := NewDispatcher(5 * time.Second)
	results := d.Dispatch(context.Background(), []ToolCallRequest{
		{ID: "1", Name: "nonexistent", Arguments: json.RawMessage(`{}`)},
	})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].IsError {
		t.Error("expected error for unknown tool")
	}
}

func TestDispatchSingleTool(t *testing.T) {
	d := NewDispatcher(5 * time.Second)
	d.Register(ToolDef{
		Name:        "echo",
		Description: "echoes input",
		Parameters:  json.RawMessage(`{}`),
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct{ Text string }
			json.Unmarshal(args, &a)
			return a.Text, nil
		},
	})

	results := d.Dispatch(context.Background(), []ToolCallRequest{
		{ID: "1", Name: "echo", Arguments: json.RawMessage(`{"Text":"hello"}`)},
	})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Content != "hello" {
		t.Errorf("got %q, want %q", results[0].Content, "hello")
	}
	if results[0].IsError {
		t.Error("unexpected error")
	}
}

func TestDispatchParallel(t *testing.T) {
	d := NewDispatcher(5 * time.Second)
	var counter int64

	d.Register(ToolDef{
		Name:        "count",
		Description: "increments counter",
		Parameters:  json.RawMessage(`{}`),
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			atomic.AddInt64(&counter, 1)
			time.Sleep(50 * time.Millisecond)
			return "ok", nil
		},
	})

	calls := make([]ToolCallRequest, 5)
	for i := range calls {
		calls[i] = ToolCallRequest{
			ID:        fmt.Sprintf("%d", i),
			Name:      "count",
			Arguments: json.RawMessage(`{}`),
		}
	}

	start := time.Now()
	results := d.Dispatch(context.Background(), calls)
	elapsed := time.Since(start)

	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}
	if atomic.LoadInt64(&counter) != 5 {
		t.Errorf("counter = %d, want 5", counter)
	}
	// Parallel execution: should take ~50ms, not ~250ms.
	if elapsed > 200*time.Millisecond {
		t.Errorf("took %v, expected parallel execution under 200ms", elapsed)
	}
}

func TestDispatchTimeout(t *testing.T) {
	d := NewDispatcher(50 * time.Millisecond)
	d.Register(ToolDef{
		Name:        "slow",
		Description: "slow tool",
		Parameters:  json.RawMessage(`{}`),
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(5 * time.Second):
				return "done", nil
			}
		},
	})

	results := d.Dispatch(context.Background(), []ToolCallRequest{
		{ID: "1", Name: "slow", Arguments: json.RawMessage(`{}`)},
	})
	if !results[0].IsError {
		t.Error("expected timeout error")
	}
}

func TestDispatchCallbacks(t *testing.T) {
	d := NewDispatcher(5 * time.Second)
	d.Register(ToolDef{
		Name:        "test",
		Description: "test",
		Parameters:  json.RawMessage(`{}`),
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return "result", nil
		},
	})

	var calledName string
	var resultContent string
	d.SetCallbacks(
		func(name string, args json.RawMessage) { calledName = name },
		func(name, callID, result string, isErr bool) { resultContent = result },
	)

	d.Dispatch(context.Background(), []ToolCallRequest{
		{ID: "1", Name: "test", Arguments: json.RawMessage(`{}`)},
	})

	if calledName != "test" {
		t.Errorf("onCall name = %q, want %q", calledName, "test")
	}
	if resultContent != "result" {
		t.Errorf("onResult content = %q, want %q", resultContent, "result")
	}
}
