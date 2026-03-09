package memory

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"testing"
	"time"

	chromem "github.com/philippgille/chromem-go"
)

// testEmbeddingFunc creates a deterministic embedding function for tests.
// It produces consistent embeddings based on content hashing (no external API).
func testEmbeddingFunc() chromem.EmbeddingFunc {
	return func(_ context.Context, text string) ([]float32, error) {
		h := fnv.New128()
		h.Write([]byte(text))
		sum := h.Sum(nil)
		// Generate a 384-dim vector from the hash.
		vec := make([]float32, 384)
		for i := range vec {
			idx := i % len(sum)
			vec[i] = float32(sum[idx])/255.0 - 0.5
		}
		// Normalize.
		var norm float64
		for _, v := range vec {
			norm += float64(v) * float64(v)
		}
		norm = math.Sqrt(norm)
		if norm > 0 {
			for i := range vec {
				vec[i] = float32(float64(vec[i]) / norm)
			}
		}
		return vec, nil
	}
}

func newTestStore(t *testing.T) *ChromemStore {
	t.Helper()
	s, err := NewChromemStore(ChromemConfig{
		CollectionName: fmt.Sprintf("test-%d", time.Now().UnixNano()),
		EmbeddingFunc:  testEmbeddingFunc(),
	})
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestChromemStoreStoreAndSearch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	docs := []MemoryDocument{
		{ID: "1", Content: "The user prefers dark mode for all applications", Tags: []string{"preference"}, Timestamp: time.Now(), Source: "conv-1"},
		{ID: "2", Content: "The project uses Go 1.22 with no CGo dependencies", Tags: []string{"technical"}, Timestamp: time.Now(), Source: "conv-1"},
		{ID: "3", Content: "Weekly team standup is every Monday at 10am", Tags: []string{"schedule"}, Timestamp: time.Now().Add(-48 * time.Hour), Source: "conv-2"},
	}
	for _, doc := range docs {
		if err := s.Store(ctx, doc); err != nil {
			t.Fatalf("Store(%s): %v", doc.ID, err)
		}
	}

	results, err := s.Search(ctx, "what programming language does the project use", 2, SearchOptions{
		MinSimilarity: 0.0,
		RecencyWeight: 0.1,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if len(results) > 2 {
		t.Errorf("expected at most 2 results, got %d", len(results))
	}

	// Verify results have scores.
	for _, r := range results {
		if r.Score <= 0 {
			t.Errorf("result %s: expected positive score, got %f", r.Document.ID, r.Score)
		}
		if r.Document.Content == "" {
			t.Errorf("result %s: empty content", r.Document.ID)
		}
	}
}

func TestChromemStoreDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	doc := MemoryDocument{
		ID:        "del-1",
		Content:   "This memory should be deleted",
		Timestamp: time.Now(),
		Source:    "test",
	}
	if err := s.Store(ctx, doc); err != nil {
		t.Fatalf("Store: %v", err)
	}

	if err := s.Delete(ctx, "del-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	results, err := s.Search(ctx, "deleted memory", 5, SearchOptions{MinSimilarity: 0.0})
	if err != nil {
		t.Fatalf("Search after delete: %v", err)
	}
	for _, r := range results {
		if r.Document.ID == "del-1" {
			t.Error("deleted document still appears in search results")
		}
	}
}

func TestChromemStoreEmptySearch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	results, err := s.Search(ctx, "anything", 5, SearchOptions{MinSimilarity: 0.0})
	if err != nil {
		t.Fatalf("Search on empty store: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results from empty store, got %d", len(results))
	}
}

func TestChromemStoreTagFiltering(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	docs := []MemoryDocument{
		{ID: "t1", Content: "User likes coffee in the morning", Tags: []string{"preference", "food"}, Timestamp: time.Now(), Source: "conv-1"},
		{ID: "t2", Content: "Server runs on port 8080", Tags: []string{"technical"}, Timestamp: time.Now(), Source: "conv-1"},
	}
	for _, doc := range docs {
		if err := s.Store(ctx, doc); err != nil {
			t.Fatalf("Store(%s): %v", doc.ID, err)
		}
	}

	results, err := s.Search(ctx, "coffee preferences", 5, SearchOptions{
		MinSimilarity: 0.0,
		Tags:          []string{"food"},
	})
	if err != nil {
		t.Fatalf("Search with tags: %v", err)
	}
	for _, r := range results {
		if !hasAnyTag(r.Document.Tags, []string{"food"}) {
			t.Errorf("result %s should have 'food' tag, has %v", r.Document.ID, r.Document.Tags)
		}
	}
}

func TestComputeRecency(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name   string
		ts     time.Time
		wantGt float64
		wantLt float64
	}{
		{"just now", now, 0.99, 1.01},
		{"one week ago", now.Add(-168 * time.Hour), 0.3, 0.6},
		{"one month ago", now.Add(-720 * time.Hour), 0.0, 0.02},
		{"zero time", time.Time{}, -0.01, 0.01},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := computeRecency(tt.ts, now)
			if score <= tt.wantGt || score >= tt.wantLt {
				// Allow some flexibility.
				if score < tt.wantGt-0.1 || score > tt.wantLt+0.1 {
					t.Errorf("computeRecency(%v) = %f, want in (%f, %f)", tt.ts, score, tt.wantGt, tt.wantLt)
				}
			}
		})
	}
}

func TestHasAnyTag(t *testing.T) {
	tests := []struct {
		docTags    []string
		filterTags []string
		want       bool
	}{
		{[]string{"a", "b"}, []string{"b"}, true},
		{[]string{"a", "b"}, []string{"c"}, false},
		{nil, []string{"a"}, false},
		{[]string{"a"}, nil, false},
	}

	for i, tt := range tests {
		got := hasAnyTag(tt.docTags, tt.filterTags)
		if got != tt.want {
			t.Errorf("case %d: hasAnyTag(%v, %v) = %v, want %v", i, tt.docTags, tt.filterTags, got, tt.want)
		}
	}
}
