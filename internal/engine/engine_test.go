package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DonScott603/gogoclaw/internal/provider"
	"github.com/DonScott603/gogoclaw/internal/tools"
)

// mockProvider is a test double for Provider.
type mockProvider struct {
	name      string
	response  string
	toolCalls []provider.ToolCall
	callCount int
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	m.callCount++
	// On first call, return tool calls if configured. On subsequent calls, return text.
	if m.callCount == 1 && len(m.toolCalls) > 0 {
		return &provider.ChatResponse{
			ToolCalls: m.toolCalls,
			Usage:     provider.TokenUsage{TotalTokens: 10},
		}, nil
	}
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

func newTestEngine(p provider.Provider, prompt string) *Engine {
	return New(Config{
		Provider:     p,
		SystemPrompt: prompt,
		MaxContext:   8192,
	})
}

func newTestSession(channel, convID string) *Session {
	return &Session{
		ID:             channel + ":" + convID,
		ConversationID: convID,
		Channel:        channel,
	}
}

func TestEngineSend(t *testing.T) {
	mock := &mockProvider{name: "mock", response: "Hello from mock!"}
	eng := newTestEngine(mock, "You are a test assistant.")
	session := newTestSession("tui", "test")

	resp, err := eng.Send(context.Background(), session, "Hi")
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	if resp != "Hello from mock!" {
		t.Errorf("Send() = %q, want %q", resp, "Hello from mock!")
	}
}

func TestEngineSendStream(t *testing.T) {
	mock := &mockProvider{name: "mock", response: "Streamed!"}
	eng := newTestEngine(mock, "")
	session := newTestSession("tui", "test")

	ch, err := eng.SendStream(context.Background(), session, "Hi")
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
	eng := newTestEngine(mock, "system prompt")
	session := newTestSession("tui", "test")

	eng.Send(context.Background(), session, "msg1")
	eng.Send(context.Background(), session, "msg2")

	h := session.GetHistory()
	// history should have: user msg1, assistant reply, user msg2, assistant reply
	if len(h) != 4 {
		t.Errorf("history length = %d, want 4", len(h))
	}
}

func TestEngineToolCallLoop(t *testing.T) {
	mock := &mockProvider{
		name:     "mock",
		response: "Final answer after tool use.",
		toolCalls: []provider.ToolCall{
			{
				ID:        "call_1",
				Name:      "think",
				Arguments: json.RawMessage(`{"thought":"analyzing..."}`),
			},
		},
	}

	d := tools.NewDispatcher(0)
	d.Register(tools.ToolDef{
		Name:        "think",
		Description: "test think",
		Parameters:  json.RawMessage(`{}`),
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return "thought recorded", nil
		},
	})

	eng := New(Config{
		Provider:     mock,
		Dispatcher:   d,
		SystemPrompt: "",
		MaxContext:    8192,
	})
	session := newTestSession("tui", "test")

	resp, err := eng.Send(context.Background(), session, "use a tool")
	if err != nil {
		t.Fatalf("Send() with tool calls error: %v", err)
	}
	if resp != "Final answer after tool use." {
		t.Errorf("Send() = %q, want %q", resp, "Final answer after tool use.")
	}

	// History should contain: user, assistant(toolcall), tool(result), assistant(final)
	h := session.GetHistory()
	if len(h) != 4 {
		t.Errorf("history length = %d, want 4", len(h))
		for i, m := range h {
			t.Logf("  [%d] role=%s content=%q", i, m.Role, m.Content)
		}
	}
}

func TestEngineIsolatedSessions(t *testing.T) {
	mock := &mockProvider{name: "mock", response: "reply"}
	eng := newTestEngine(mock, "")

	s1 := newTestSession("tui", "conv1")
	s2 := newTestSession("tui", "conv2")

	eng.Send(context.Background(), s1, "message for conv1")
	eng.Send(context.Background(), s2, "message for conv2")

	h1 := s1.GetHistory()
	h2 := s2.GetHistory()

	if len(h1) != 2 {
		t.Errorf("s1 history length = %d, want 2", len(h1))
	}
	if len(h2) != 2 {
		t.Errorf("s2 history length = %d, want 2", len(h2))
	}

	if h1[0].Content != "message for conv1" {
		t.Errorf("s1 first message = %q, want %q", h1[0].Content, "message for conv1")
	}
	if h2[0].Content != "message for conv2" {
		t.Errorf("s2 first message = %q, want %q", h2[0].Content, "message for conv2")
	}
}

func TestEngineSendUserPersistenceFailureNoAppend(t *testing.T) {
	mock := &mockProvider{name: "mock", response: "reply"}
	eng := New(Config{
		Provider:    mock,
		MaxContext:  8192,
		Persistence: &failingPersistence{failOn: "user"},
	})
	session := newTestSession("tui", "test")

	_, err := eng.Send(context.Background(), session, "Hi")
	if err == nil {
		t.Fatal("Send() should return error when user persistence fails")
	}
	if !strings.Contains(err.Error(), "persist user message") {
		t.Errorf("error = %q, want to contain 'persist user message'", err.Error())
	}
	// Session must NOT contain the user message.
	h := session.GetHistory()
	if len(h) != 0 {
		t.Errorf("session history length = %d, want 0 (user message should not be appended on failure)", len(h))
	}
}

func TestEngineSendAssistantPersistenceFailureNoAppend(t *testing.T) {
	mock := &mockProvider{name: "mock", response: "reply"}
	eng := New(Config{
		Provider:    mock,
		MaxContext:  8192,
		Persistence: &failingPersistence{failOn: "assistant"},
	})
	session := newTestSession("tui", "test")

	_, err := eng.Send(context.Background(), session, "Hi")
	if err == nil {
		t.Fatal("Send() should return error when assistant persistence fails")
	}
	if !strings.Contains(err.Error(), "persist assistant message") {
		t.Errorf("error = %q, want to contain 'persist assistant message'", err.Error())
	}
	// Session should contain the user message (persisted OK) but NOT the assistant.
	h := session.GetHistory()
	if len(h) != 1 {
		t.Errorf("session history length = %d, want 1 (only user msg)", len(h))
	}
	if len(h) > 0 && h[0].Role != "user" {
		t.Errorf("h[0].Role = %q, want user", h[0].Role)
	}
}

func TestEngineSendToolPersistenceFailureNoAppend(t *testing.T) {
	mock := &mockProvider{
		name:     "mock",
		response: "Final.",
		toolCalls: []provider.ToolCall{
			{ID: "call_1", Name: "think", Arguments: json.RawMessage(`{}`)},
		},
	}
	d := tools.NewDispatcher(0)
	d.Register(tools.ToolDef{
		Name: "think", Description: "test", Parameters: json.RawMessage(`{}`),
		Fn: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "ok", nil
		},
	})

	eng := New(Config{
		Provider:    mock,
		Dispatcher:  d,
		MaxContext:  8192,
		Persistence: &failingPersistence{failOn: "tool"},
	})
	session := newTestSession("tui", "test")

	_, err := eng.Send(context.Background(), session, "use tool")
	if err == nil {
		t.Fatal("Send() should return error when tool persistence fails")
	}
	if !strings.Contains(err.Error(), "persist tool message") {
		t.Errorf("error = %q, want to contain 'persist tool message'", err.Error())
	}
	// Session should have user + assistant(toolcall) but NOT the tool result.
	h := session.GetHistory()
	if len(h) != 2 {
		t.Errorf("session history length = %d, want 2 (user + assistant with toolcall)", len(h))
		for i, m := range h {
			t.Logf("  [%d] role=%s", i, m.Role)
		}
	}
}

func TestEngineSendStreamUserPersistenceFailureNoAppend(t *testing.T) {
	mock := &mockProvider{name: "mock", response: "reply"}
	eng := New(Config{
		Provider:    mock,
		MaxContext:  8192,
		Persistence: &failingPersistence{failOn: "user"},
	})
	session := newTestSession("tui", "test")

	_, err := eng.SendStream(context.Background(), session, "Hi")
	if err == nil {
		t.Fatal("SendStream() should return error when user persistence fails before streaming")
	}
	if !strings.Contains(err.Error(), "persist user message") {
		t.Errorf("error = %q, want to contain 'persist user message'", err.Error())
	}
	// Session must NOT contain the user message.
	h := session.GetHistory()
	if len(h) != 0 {
		t.Errorf("session history length = %d, want 0", len(h))
	}
}

func TestEngineSendStreamAssistantPersistenceFailureNoAppend(t *testing.T) {
	mock := &mockProvider{name: "mock", response: "reply"}
	eng := New(Config{
		Provider:    mock,
		MaxContext:  8192,
		Persistence: &failingPersistence{failOn: "assistant"},
	})
	session := newTestSession("tui", "test")

	ch, err := eng.SendStream(context.Background(), session, "Hi")
	if err != nil {
		t.Fatalf("SendStream() should not fail upfront: %v", err)
	}

	var lastChunk provider.StreamChunk
	for chunk := range ch {
		lastChunk = chunk
	}
	if lastChunk.Error == nil {
		t.Fatal("expected terminal error chunk when assistant persistence fails after streaming")
	}
	if !strings.Contains(lastChunk.Error.Error(), "persist assistant message") {
		t.Errorf("error = %q, want to contain 'persist assistant message'", lastChunk.Error.Error())
	}
	// Session should have user message but NOT the assistant.
	h := session.GetHistory()
	if len(h) != 1 {
		t.Errorf("session history length = %d, want 1 (only user msg)", len(h))
	}
	if len(h) > 0 && h[0].Role != "user" {
		t.Errorf("h[0].Role = %q, want user", h[0].Role)
	}
}
