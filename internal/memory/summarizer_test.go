package memory

import (
	"context"
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
