// Package memory implements the vector-backed memory system
// using chromem-go for embedding storage and retrieval.
package memory

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	chromem "github.com/philippgille/chromem-go"
)

// VectorStore abstracts vector storage for memory documents.
type VectorStore interface {
	Store(ctx context.Context, doc MemoryDocument) error
	Search(ctx context.Context, query string, topK int, opts SearchOptions) ([]MemoryResult, error)
	Delete(ctx context.Context, id string) error
	Close() error
}

// MemoryDocument is a fact or piece of information to persist.
type MemoryDocument struct {
	ID        string
	Content   string
	Tags      []string
	Timestamp time.Time
	Source    string // conversation ID that produced this memory
}

// MemoryResult is a search result with similarity and blended score.
type MemoryResult struct {
	Document   MemoryDocument
	Similarity float64
	Score      float64 // blended score (similarity + recency)
}

// SearchOptions controls search behavior.
type SearchOptions struct {
	MinSimilarity float64
	RecencyWeight float64
	Tags          []string // optional tag filter
}

// ChromemStore implements VectorStore backed by chromem-go.
type ChromemStore struct {
	db         *chromem.DB
	collection *chromem.Collection
	persistent bool
}

// ChromemConfig holds configuration for creating a ChromemStore.
type ChromemConfig struct {
	Path          string                // empty for in-memory
	Compress      bool
	CollectionName string
	EmbeddingFunc chromem.EmbeddingFunc
}

// NewChromemStore creates a new ChromemStore.
func NewChromemStore(cfg ChromemConfig) (*ChromemStore, error) {
	collName := cfg.CollectionName
	if collName == "" {
		collName = "memories"
	}

	var db *chromem.DB
	var err error
	persistent := cfg.Path != ""

	if persistent {
		db, err = chromem.NewPersistentDB(cfg.Path, cfg.Compress)
		if err != nil {
			return nil, fmt.Errorf("memory: create persistent db: %w", err)
		}
	} else {
		db = chromem.NewDB()
	}

	embFn := cfg.EmbeddingFunc
	if embFn == nil {
		embFn = chromem.NewEmbeddingFuncDefault()
	}

	coll, err := db.GetOrCreateCollection(collName, nil, embFn)
	if err != nil {
		return nil, fmt.Errorf("memory: create collection: %w", err)
	}

	return &ChromemStore{
		db:         db,
		collection: coll,
		persistent: persistent,
	}, nil
}

// Store embeds and stores a memory document.
func (s *ChromemStore) Store(ctx context.Context, doc MemoryDocument) error {
	metadata := map[string]string{
		"timestamp": doc.Timestamp.Format(time.RFC3339),
		"source":    doc.Source,
	}
	for i, tag := range doc.Tags {
		metadata[fmt.Sprintf("tag_%d", i)] = tag
	}
	metadata["tag_count"] = fmt.Sprintf("%d", len(doc.Tags))

	chromDoc := chromem.Document{
		ID:       doc.ID,
		Content:  doc.Content,
		Metadata: metadata,
	}
	return s.collection.AddDocument(ctx, chromDoc)
}

// Search returns the top-K most similar documents, blended with recency.
func (s *ChromemStore) Search(ctx context.Context, query string, topK int, opts SearchOptions) ([]MemoryResult, error) {
	if s.collection.Count() == 0 {
		return nil, nil
	}

	// Query more than topK to allow post-filtering.
	nResults := topK * 3
	if nResults < 10 {
		nResults = 10
	}
	if nResults > s.collection.Count() {
		nResults = s.collection.Count()
	}

	results, err := s.collection.Query(ctx, query, nResults, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("memory: search: %w", err)
	}

	now := time.Now()
	var memResults []MemoryResult
	for _, r := range results {
		sim := float64(r.Similarity)
		if sim < opts.MinSimilarity {
			continue
		}

		doc := MemoryDocument{
			ID:      r.ID,
			Content: r.Content,
			Source:  r.Metadata["source"],
		}
		if ts, err := time.Parse(time.RFC3339, r.Metadata["timestamp"]); err == nil {
			doc.Timestamp = ts
		}
		// Reconstruct tags.
		tagCountStr := r.Metadata["tag_count"]
		var tagCount int
		fmt.Sscanf(tagCountStr, "%d", &tagCount)
		for i := 0; i < tagCount; i++ {
			if tag, ok := r.Metadata[fmt.Sprintf("tag_%d", i)]; ok {
				doc.Tags = append(doc.Tags, tag)
			}
		}

		// Tag filtering.
		if len(opts.Tags) > 0 && !hasAnyTag(doc.Tags, opts.Tags) {
			continue
		}

		// Compute blended score: similarity * (1 - recencyWeight) + recency * recencyWeight.
		recencyScore := computeRecency(doc.Timestamp, now)
		blended := sim*(1-opts.RecencyWeight) + recencyScore*opts.RecencyWeight

		memResults = append(memResults, MemoryResult{
			Document:   doc,
			Similarity: sim,
			Score:      blended,
		})
	}

	// Sort by blended score descending.
	sort.Slice(memResults, func(i, j int) bool {
		return memResults[i].Score > memResults[j].Score
	})

	if len(memResults) > topK {
		memResults = memResults[:topK]
	}
	return memResults, nil
}

// Delete removes a document by ID.
func (s *ChromemStore) Delete(ctx context.Context, id string) error {
	return s.collection.Delete(ctx, nil, nil, id)
}

// Close is a no-op for chromem-go (persistence is automatic for persistent DBs).
func (s *ChromemStore) Close() error {
	return nil
}

// computeRecency returns a score between 0 and 1 based on how recent the timestamp is.
// Documents from the last hour score ~1.0, documents from 30+ days ago score ~0.
func computeRecency(ts, now time.Time) float64 {
	if ts.IsZero() {
		return 0.0
	}
	hours := now.Sub(ts).Hours()
	if hours < 0 {
		hours = 0
	}
	// Exponential decay: half-life of ~7 days (168 hours).
	return math.Exp(-hours / 168.0)
}

func hasAnyTag(docTags, filterTags []string) bool {
	tagSet := make(map[string]struct{}, len(docTags))
	for _, t := range docTags {
		tagSet[t] = struct{}{}
	}
	for _, t := range filterTags {
		if _, ok := tagSet[t]; ok {
			return true
		}
	}
	return false
}
