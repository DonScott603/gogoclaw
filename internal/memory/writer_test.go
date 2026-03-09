package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DonScott603/gogoclaw/internal/provider"
)

func TestExtractFacts(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "I prefer using vim for editing."},
		{Role: "assistant", Content: "I'll remember that you prefer using vim for editing. Your preference has been noted."},
		{Role: "user", Content: "What's 2+2?"},
		{Role: "assistant", Content: "4"},
		{Role: "assistant", Content: "The user wants to use dark mode for everything in the application."},
	}

	facts := ExtractFacts(messages)
	if len(facts) == 0 {
		t.Fatal("expected at least 1 extracted fact")
	}

	// Check that we got relevant facts, not noise.
	found := false
	for _, f := range facts {
		lower := strings.ToLower(f)
		if strings.Contains(lower, "prefer") || strings.Contains(lower, "user wants") || strings.Contains(lower, "remember") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected factual content in extracted facts, got: %v", facts)
	}
}

func TestExtractFactsEmpty(t *testing.T) {
	facts := ExtractFacts(nil)
	if len(facts) != 0 {
		t.Errorf("expected 0 facts from nil messages, got %d", len(facts))
	}
}

func TestExtractFactsNoAssistant(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "Hello there"},
		{Role: "user", Content: "How are you?"},
	}
	facts := ExtractFacts(messages)
	if len(facts) != 0 {
		t.Errorf("expected 0 facts from user-only messages, got %d", len(facts))
	}
}

func TestWriterDailyFile(t *testing.T) {
	store := &mockVectorStore{}
	tmpDir := t.TempDir()
	dailyDir := filepath.Join(tmpDir, "daily")

	w := NewWriter(store, dailyDir)

	messages := []provider.Message{
		{Role: "assistant", Content: "I'll remember that you prefer Go over Python for backend development."},
	}

	err := w.ExtractAndSave(context.Background(), "conv-test", messages)
	if err != nil {
		t.Fatalf("ExtractAndSave: %v", err)
	}

	// Check that a daily file was created.
	entries, err := os.ReadDir(dailyDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected daily memory file to be created")
	}

	data, err := os.ReadFile(filepath.Join(dailyDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "GoGoClaw Memory") {
		t.Error("daily file missing header")
	}
	if !strings.Contains(content, "conv-test") {
		t.Error("daily file missing conversation ID")
	}
}

// mockVectorStore is a simple in-memory VectorStore for testing.
type mockVectorStore struct {
	docs []MemoryDocument
}

func (m *mockVectorStore) Store(_ context.Context, doc MemoryDocument) error {
	m.docs = append(m.docs, doc)
	return nil
}

func (m *mockVectorStore) Search(_ context.Context, _ string, topK int, _ SearchOptions) ([]MemoryResult, error) {
	var results []MemoryResult
	for i, doc := range m.docs {
		if i >= topK {
			break
		}
		results = append(results, MemoryResult{Document: doc, Similarity: 0.9, Score: 0.9})
	}
	return results, nil
}

func (m *mockVectorStore) Delete(_ context.Context, id string) error {
	for i, doc := range m.docs {
		if doc.ID == id {
			m.docs = append(m.docs[:i], m.docs[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *mockVectorStore) Close() error { return nil }
