package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/DonScott603/gogoclaw/internal/provider"
)

// mockProvider implements provider.Provider for testing.
type mockProvider struct {
	chatResponse string
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Chat(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: m.chatResponse}, nil
}
func (m *mockProvider) ChatStream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Content: m.chatResponse, Done: true}
	close(ch)
	return ch, nil
}
func (m *mockProvider) CountTokens(content string) (int, error) { return len(content) / 4, nil }
func (m *mockProvider) Healthy(_ context.Context) bool          { return true }

func TestSummarizerNoSummarizationNeeded(t *testing.T) {
	s := NewSummarizer(&mockProvider{chatResponse: "summary"}, 10000, nil)

	history := []provider.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}

	result, err := s.MaybeSummarize(context.Background(), history, "conv-1")
	if err != nil {
		t.Fatalf("MaybeSummarize: %v", err)
	}
	if result != nil {
		t.Error("expected nil result when history is under threshold")
	}
}

func TestSummarizerTriggered(t *testing.T) {
	p := &mockProvider{chatResponse: "This is a summary of the conversation."}
	// Very low threshold to force summarization.
	s := NewSummarizer(p, 50, &mockVectorStore{})

	var history []provider.Message
	for i := 0; i < 20; i++ {
		history = append(history, provider.Message{
			Role:    "user",
			Content: "This is a reasonably long message that should push us over the token threshold number " + string(rune('A'+i)),
		})
		history = append(history, provider.Message{
			Role:    "assistant",
			Content: "Here is a response with some content that also consumes tokens in the budget counter " + string(rune('A'+i)),
		})
	}

	result, err := s.MaybeSummarize(context.Background(), history, "conv-test")
	if err != nil {
		t.Fatalf("MaybeSummarize: %v", err)
	}
	if result == nil {
		t.Fatal("expected summarization to trigger")
	}
	if result.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(result.RemainingHistory) == 0 {
		t.Error("expected remaining history")
	}
	if len(result.RemainingHistory) >= len(history) {
		t.Errorf("expected fewer messages after summarization: got %d, original %d", len(result.RemainingHistory), len(history))
	}

	// First message should be the summary system message.
	if result.RemainingHistory[0].Role != "system" {
		t.Errorf("first remaining message should be system summary, got role=%s", result.RemainingHistory[0].Role)
	}
}

func TestSummarizerFindSplitPoint(t *testing.T) {
	s := NewSummarizer(nil, 100, nil)

	history := []provider.Message{
		{Role: "user", Content: "First user message"},
		{Role: "assistant", Content: "First response"},
		{Role: "user", Content: "Second user message"},
		{Role: "assistant", Content: "Second response"},
		{Role: "user", Content: "Third user message"},
		{Role: "assistant", Content: "Third response"},
	}

	totalTokens := 0
	for _, msg := range history {
		totalTokens += len(msg.Content) / 4
	}

	splitIdx := s.findSplitPoint(history, totalTokens)
	if splitIdx <= 0 || splitIdx >= len(history) {
		t.Errorf("splitIdx = %d, expected between 1 and %d", splitIdx, len(history)-1)
	}
}

func TestCondensedToolCalls(t *testing.T) {
	tcs := []provider.ToolCall{
		{Name: "file_read", Arguments: json.RawMessage(`{"path":"/etc/config.yaml"}`)},
		{Name: "shell_exec", Arguments: json.RawMessage(`{"command":"go test ./..."}`)},
	}
	result := condensedToolCalls(tcs)
	if !strings.Contains(result, "file_read(/etc/config.yaml)") {
		t.Errorf("expected file_read with path, got: %s", result)
	}
	if !strings.Contains(result, "shell_exec(go test ./...)") {
		t.Errorf("expected shell_exec with command, got: %s", result)
	}
}

func TestCondensedToolCallTruncation(t *testing.T) {
	longArg := strings.Repeat("x", 200)
	tc := provider.ToolCall{Name: "test", Arguments: json.RawMessage(fmt.Sprintf(`{"path":"%s"}`, longArg))}
	result := condenseToolCall(tc)
	if len(result) > 100 {
		t.Errorf("condensed tool call too long (%d chars): %s", len(result), result)
	}
	if !strings.HasSuffix(result, "...)") {
		t.Errorf("expected truncation suffix, got: %s", result)
	}
}

func TestCondensedToolCallEmptyArgs(t *testing.T) {
	tc := provider.ToolCall{Name: "think", Arguments: nil}
	result := condenseToolCall(tc)
	if result != "think()" {
		t.Errorf("expected 'think()', got: %s", result)
	}
}

func TestExtractKeyArgPriorityKeys(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args string
		want string
	}{
		{"file_read path", "file_read", `{"path":"/foo/bar.go","encoding":"utf-8"}`, "/foo/bar.go"},
		{"shell_exec command", "shell_exec", `{"command":"ls -la"}`, "ls -la"},
		{"memory_search query", "memory_search", `{"query":"project setup","top_k":5}`, "project setup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractKeyArg(tt.tool, json.RawMessage(tt.args))
			if got != tt.want {
				t.Errorf("extractKeyArg(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestExtractKeyArgDeterministicFallback(t *testing.T) {
	args := json.RawMessage(`{"url":"https://example.com","data":"test"}`)
	var first string
	for i := 0; i < 10; i++ {
		got := extractKeyArg("custom_tool", args)
		if i == 0 {
			first = got
		}
		if got != first {
			t.Fatalf("non-deterministic result on iteration %d: got %q, first was %q", i, got, first)
		}
	}
	if first != "https://example.com" {
		t.Errorf("expected 'https://example.com', got %q", first)
	}
}

func TestExtractKeyArgNoMatch(t *testing.T) {
	args := json.RawMessage(`{"count":42,"enabled":true}`)
	got := extractKeyArg("custom_tool", args)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestCheckKeyTermOverlap(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "I'm working on the GoGoClaw project using internal/engine/session.go for sessions."},
		{Role: "assistant", Content: "GoGoClaw uses SessionManager in internal/engine/session.go for isolation."},
		{Role: "user", Content: "Let's update SessionManager in GoGoClaw to add boundary support."},
		{Role: "assistant", Content: "Updating SessionManager in GoGoClaw makes sense for the lifecycle changes."},
	}

	// Good summary containing key terms.
	goodSummary := "Discussion about GoGoClaw project, modifying SessionManager in internal/engine/session.go for boundary support."
	missing := checkKeyTermOverlap(messages, goodSummary)
	if len(missing) > 0 {
		t.Errorf("expected no missing terms for good summary, got: %v", missing)
	}

	// Bad summary missing key terms.
	badSummary := "Discussion about a project with plans to update some code."
	missing = checkKeyTermOverlap(messages, badSummary)
	if len(missing) == 0 {
		t.Error("expected missing terms for bad summary")
	}
}

// capturingProvider records the chat request for inspection.
type capturingProvider struct {
	chatResponse string
	onChat       func(provider.ChatRequest)
}

func (c *capturingProvider) Name() string { return "capturing" }
func (c *capturingProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	if c.onChat != nil {
		c.onChat(req)
	}
	return &provider.ChatResponse{Content: c.chatResponse}, nil
}
func (c *capturingProvider) ChatStream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Content: c.chatResponse, Done: true}
	close(ch)
	return ch, nil
}
func (c *capturingProvider) CountTokens(content string) (int, error) { return len(content) / 4, nil }
func (c *capturingProvider) Healthy(_ context.Context) bool          { return true }

func TestSummarizerIncludesToolCalls(t *testing.T) {
	var capturedPrompt string
	p := &capturingProvider{
		chatResponse: "Summary of conversation with tool use.",
		onChat: func(req provider.ChatRequest) {
			if len(req.Messages) > 0 {
				capturedPrompt = req.Messages[0].Content
			}
		},
	}

	s := NewSummarizer(p, 50, nil) // low threshold to force summarization

	var history []provider.Message
	for i := 0; i < 10; i++ {
		history = append(history, provider.Message{
			Role:    "user",
			Content: fmt.Sprintf("User message %d with enough content to push tokens", i),
		})
		if i == 3 {
			history = append(history, provider.Message{
				Role:    "assistant",
				Content: "",
				ToolCalls: []provider.ToolCall{
					{Name: "file_read", Arguments: json.RawMessage(`{"path":"/etc/config.yaml"}`)},
				},
			})
			history = append(history, provider.Message{
				Role:       "tool",
				Content:    "file contents here",
				ToolCallID: "call_1",
			})
		}
		history = append(history, provider.Message{
			Role:    "assistant",
			Content: fmt.Sprintf("Assistant response %d with substantial content here", i),
		})
	}

	result, err := s.MaybeSummarize(context.Background(), history, "test")
	if err != nil {
		t.Fatalf("MaybeSummarize: %v", err)
	}
	if result == nil {
		t.Fatal("expected summarization to trigger")
	}

	if !strings.Contains(capturedPrompt, "file_read") {
		t.Error("expected summarization prompt to include condensed tool call 'file_read'")
	}
	if !strings.Contains(capturedPrompt, "/etc/config.yaml") {
		t.Error("expected summarization prompt to include tool call argument")
	}
}

func TestCondensedToolCallsAllEmpty(t *testing.T) {
	tcs := []provider.ToolCall{
		{Name: "", Arguments: nil},
		{Name: "", Arguments: nil},
	}
	result := condensedToolCalls(tcs)
	if result != "" {
		t.Errorf("expected empty string for all-empty tool calls, got: %q", result)
	}
}

func TestSummarizerSkipsRawToolResultContent(t *testing.T) {
	var capturedPrompt string
	p := &capturingProvider{
		chatResponse: "Summary of conversation.",
		onChat: func(req provider.ChatRequest) {
			if len(req.Messages) > 0 {
				capturedPrompt = req.Messages[0].Content
			}
		},
	}

	s := NewSummarizer(p, 50, nil)

	var history []provider.Message
	for i := 0; i < 10; i++ {
		history = append(history, provider.Message{
			Role:    "user",
			Content: fmt.Sprintf("User message %d with enough content to push tokens", i),
		})
		if i == 3 {
			history = append(history, provider.Message{
				Role:    "assistant",
				Content: "",
				ToolCalls: []provider.ToolCall{
					{Name: "file_read", Arguments: json.RawMessage(`{"path":"/foo"}`)},
				},
			})
			history = append(history, provider.Message{
				Role:       "tool",
				Content:    "UNIQUE_TOOL_OUTPUT_MARKER_12345",
				ToolCallID: "call_1",
			})
		}
		history = append(history, provider.Message{
			Role:    "assistant",
			Content: fmt.Sprintf("Assistant response %d with substantial content here", i),
		})
	}

	result, err := s.MaybeSummarize(context.Background(), history, "test")
	if err != nil {
		t.Fatalf("MaybeSummarize: %v", err)
	}
	if result == nil {
		t.Fatal("expected summarization to trigger")
	}

	if strings.Contains(capturedPrompt, "UNIQUE_TOOL_OUTPUT_MARKER_12345") {
		t.Error("raw tool result content should NOT appear in summarization prompt")
	}
}
