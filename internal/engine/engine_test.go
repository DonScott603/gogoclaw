package engine

import (
	"context"
	"testing"

	"github.com/DonScott603/gogoclaw/internal/provider"
)

// mockProvider is a test double for Provider.
type mockProvider struct {
	name     string
	response string
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{
		Content: m.response,
		Usage:   provider.TokenUsage{TotalTokens: 10},
	}, nil
}

func (m *mockProvider) ChatStream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: m.response}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch, nil
}

func (m *mockProvider) CountTokens(content string) (int, error) { return len(content) / 4, nil }
func (m *mockProvider) Healthy(_ context.Context) bool          { return true }

func TestEngineSend(t *testing.T) {
	mock := &mockProvider{name: "mock", response: "Hello from mock!"}
	eng := New(mock, "You are a test assistant.")

	resp, err := eng.Send(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	if resp != "Hello from mock!" {
		t.Errorf("Send() = %q, want %q", resp, "Hello from mock!")
	}
}

func TestEngineSendStream(t *testing.T) {
	mock := &mockProvider{name: "mock", response: "Streamed!"}
	eng := New(mock, "")

	ch, err := eng.SendStream(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("SendStream() error: %v", err)
	}

	var content string
	for chunk := range ch {
		content += chunk.Content
	}
	if content != "Streamed!" {
		t.Errorf("SendStream() collected = %q, want %q", content, "Streamed!")
	}
}

func TestEngineHistoryAccumulates(t *testing.T) {
	mock := &mockProvider{name: "mock", response: "reply"}
	eng := New(mock, "system prompt")

	eng.Send(context.Background(), "msg1")
	eng.Send(context.Background(), "msg2")

	eng.mu.Lock()
	defer eng.mu.Unlock()
	// history should have: user msg1, assistant reply, user msg2, assistant reply
	if len(eng.history) != 4 {
		t.Errorf("history length = %d, want 4", len(eng.history))
	}
}
