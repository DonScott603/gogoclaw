package app

import (
	"log"
	"os"
	"path/filepath"

	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/internal/memory"
	"github.com/DonScott603/gogoclaw/internal/provider"
	"github.com/DonScott603/gogoclaw/internal/util"
)

// MemoryDeps holds the vector store, search options, and summarizer.
type MemoryDeps struct {
	Store      memory.VectorStore
	SearchOpts memory.SearchOptions
	Summarizer engine.Summarizer
	closeFn    func() // internal; called by Close
}

// InitMemory sets up the vector-backed memory system.
func InitMemory(cfg *config.Config, configDir string, activeProvider provider.Provider) MemoryDeps {
	deps := MemoryDeps{
		Store: memory.NoOpVectorStore{},
		SearchOpts: memory.SearchOptions{
			MinSimilarity: 0.3,
			RecencyWeight: 0.2,
		},
		Summarizer: engine.NoOpSummarizer{},
	}

	if !cfg.Memory.Enabled {
		log.Printf("memory: disabled (set memory.enabled=true in config to enable)")
		return deps
	}

	vecPath := util.ExpandHome(cfg.Memory.Storage.Path)
	if vecPath == "" {
		vecPath = filepath.Join(configDir, "data", "vectors")
	}
	os.MkdirAll(vecPath, 0o755)

	if entries, err := os.ReadDir(vecPath); err == nil {
		log.Printf("memory: vector store path: %s (%d existing entries)", vecPath, len(entries))
	} else {
		log.Printf("memory: vector store path: %s (new directory)", vecPath)
	}

	embFn := memory.NewEmbeddingFunc(cfg.Memory, cfg.Providers)
	cs, err := memory.NewChromemStore(memory.ChromemConfig{
		Path:          vecPath,
		Compress:      true,
		EmbeddingFunc: embFn,
	})
	if err != nil {
		log.Printf("memory: failed to initialize vector store: %v (continuing without memory)", err)
		return deps
	}

	deps.Store = cs
	deps.closeFn = func() { cs.Close() }
	log.Printf("memory: vector store initialized successfully (persistent=%v)", vecPath != "")

	if cfg.Memory.Retrieval.RelevanceThreshold > 0 {
		deps.SearchOpts.MinSimilarity = cfg.Memory.Retrieval.RelevanceThreshold
	}
	if cfg.Memory.Retrieval.RecencyWeight > 0 {
		deps.SearchOpts.RecencyWeight = cfg.Memory.Retrieval.RecencyWeight
	}

	if agent, ok := cfg.Agents["base"]; ok && agent.Context.Summarization.Enabled {
		deps.Summarizer = memory.NewSummarizer(activeProvider, agent.Context.Summarization.ThresholdTokens, deps.Store)
	}

	return deps
}

// Close releases memory resources. Safe to call even when memory is disabled
// (closeFn defaults to nil, which is a no-op).
func (d *MemoryDeps) Close() {
	if d.closeFn != nil {
		d.closeFn()
	}
}
