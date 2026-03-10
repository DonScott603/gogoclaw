package memory

import "context"

// NoOpVectorStore is a VectorStore that does nothing. Use it as a safe
// default when the memory system is disabled, eliminating nil checks.
type NoOpVectorStore struct{}

func (NoOpVectorStore) Store(context.Context, MemoryDocument) error { return nil }
func (NoOpVectorStore) Search(_ context.Context, _ string, _ int, _ SearchOptions) ([]MemoryResult, error) {
	return nil, nil
}
func (NoOpVectorStore) Delete(context.Context, string) error { return nil }
func (NoOpVectorStore) Close() error                         { return nil }
