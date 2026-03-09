package provider

import (
	"context"
	"fmt"
	"math"
	"time"
)

// Router tries providers in order with exponential backoff and failover.
type Router struct {
	providers []providerEntry
}

type providerEntry struct {
	provider Provider
	timeout  time.Duration
	retries  int
}

// NewRouter creates a Router from an ordered list of providers.
func NewRouter(providers []Provider, timeouts []time.Duration, retries []int) *Router {
	entries := make([]providerEntry, len(providers))
	for i, p := range providers {
		t := 60 * time.Second
		if i < len(timeouts) && timeouts[i] > 0 {
			t = timeouts[i]
		}
		r := 1
		if i < len(retries) && retries[i] > 0 {
			r = retries[i]
		}
		entries[i] = providerEntry{provider: p, timeout: t, retries: r}
	}
	return &Router{providers: entries}
}

func (r *Router) Name() string {
	if len(r.providers) > 0 {
		return r.providers[0].provider.Name()
	}
	return "router"
}

func (r *Router) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	var lastErr error
	for _, entry := range r.providers {
		resp, err := r.tryWithRetries(ctx, entry, func(ctx context.Context) (*ChatResponse, error) {
			return entry.provider.Chat(ctx, req)
		})
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("provider: router: all providers failed: %w", lastErr)
}

func (r *Router) ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	var lastErr error
	for _, entry := range r.providers {
		childCtx, cancel := context.WithTimeout(ctx, entry.timeout)
		ch, err := entry.provider.ChatStream(childCtx, req)
		if err == nil {
			// Wrap the channel to call cancel when done.
			out := make(chan StreamChunk, 64)
			go func() {
				defer cancel()
				defer close(out)
				for chunk := range ch {
					out <- chunk
				}
			}()
			return out, nil
		}
		cancel()
		lastErr = err
	}
	return nil, fmt.Errorf("provider: router: all providers failed (stream): %w", lastErr)
}

func (r *Router) CountTokens(content string) (int, error) {
	if len(r.providers) > 0 {
		return r.providers[0].provider.CountTokens(content)
	}
	return len(content) / 4, nil
}

func (r *Router) Healthy(ctx context.Context) bool {
	for _, entry := range r.providers {
		if entry.provider.Healthy(ctx) {
			return true
		}
	}
	return false
}

func (r *Router) tryWithRetries(
	ctx context.Context,
	entry providerEntry,
	fn func(ctx context.Context) (*ChatResponse, error),
) (*ChatResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= entry.retries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(math.Pow(2, float64(attempt-1))) * 500 * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		childCtx, cancel := context.WithTimeout(ctx, entry.timeout)
		resp, err := fn(childCtx)
		cancel()
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
