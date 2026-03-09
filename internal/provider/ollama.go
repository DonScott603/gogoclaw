package provider

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Ollama is a provider for local Ollama instances. It uses the OpenAI-compatible
// endpoint that Ollama exposes at /v1, but adds Ollama-specific health checks.
type Ollama struct {
	compat *OpenAICompat
}

// OllamaConfig holds the parameters for an Ollama provider.
type OllamaConfig struct {
	Name         string
	BaseURL      string
	DefaultModel string
	Timeout      time.Duration
}

// NewOllama creates a new Ollama provider.
func NewOllama(cfg OllamaConfig) *Ollama {
	return &Ollama{
		compat: NewOpenAICompat(OpenAICompatConfig{
			Name:         cfg.Name,
			BaseURL:      cfg.BaseURL,
			DefaultModel: cfg.DefaultModel,
			Timeout:      cfg.Timeout,
		}),
	}
}

func (o *Ollama) Name() string { return o.compat.Name() }

func (o *Ollama) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return o.compat.Chat(ctx, req)
}

func (o *Ollama) ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	return o.compat.ChatStream(ctx, req)
}

func (o *Ollama) CountTokens(content string) (int, error) {
	return o.compat.CountTokens(content)
}

// Healthy checks if Ollama is running by hitting the root endpoint.
func (o *Ollama) Healthy(ctx context.Context) bool {
	// Ollama's native health endpoint is GET / which returns "Ollama is running".
	baseURL := o.compat.baseURL
	// Strip /v1 suffix to hit the root.
	if len(baseURL) > 3 && baseURL[len(baseURL)-3:] == "/v1" {
		baseURL = baseURL[:len(baseURL)-3]
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// EnsureModel pulls the model if it doesn't exist locally. This is a no-op
// if the model is already present.
func (o *Ollama) EnsureModel(ctx context.Context, model string) error {
	if model == "" {
		model = o.compat.model
	}
	// Check via the /api/tags endpoint.
	baseURL := o.compat.baseURL
	if len(baseURL) > 3 && baseURL[len(baseURL)-3:] == "/v1" {
		baseURL = baseURL[:len(baseURL)-3]
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("provider: ollama: check models: %w", err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("provider: ollama: check models: %w", err)
	}
	resp.Body.Close()
	// For now, just verify connectivity. Full model pull logic is post-MVP.
	return nil
}
