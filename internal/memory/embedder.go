package memory

import (
	chromem "github.com/philippgille/chromem-go"

	"github.com/DonScott603/gogoclaw/internal/config"
)

// NewEmbeddingFunc creates a chromem-go EmbeddingFunc from config.
// It supports Ollama and OpenAI-compatible providers.
func NewEmbeddingFunc(cfg config.MemoryStoreConfig, providers map[string]config.ProviderConfig) chromem.EmbeddingFunc {
	primary := buildEmbeddingFunc(cfg.Embedding.Provider, cfg.Embedding.Model, providers)
	if primary != nil {
		return primary
	}

	// Try fallback provider.
	if cfg.Embedding.FallbackProvider != "" {
		fallback := buildEmbeddingFunc(cfg.Embedding.FallbackProvider, cfg.Embedding.Model, providers)
		if fallback != nil {
			return fallback
		}
	}

	// Last resort: chromem-go's built-in default.
	return chromem.NewEmbeddingFuncDefault()
}

func buildEmbeddingFunc(providerName, model string, providers map[string]config.ProviderConfig) chromem.EmbeddingFunc {
	pc, ok := providers[providerName]
	if !ok {
		return nil
	}

	embModel := model
	if embModel == "" {
		embModel = "nomic-embed-text"
	}

	switch pc.Type {
	case "ollama":
		baseURL := pc.BaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		return chromem.NewEmbeddingFuncOllama(embModel, baseURL)
	default:
		// OpenAI-compatible.
		baseURL := pc.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		return chromem.NewEmbeddingFuncOpenAICompat(baseURL, pc.APIKey, embModel, nil)
	}
}
