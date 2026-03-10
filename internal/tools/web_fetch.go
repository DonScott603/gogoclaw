package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RegisterWebFetchTool registers web_fetch with network allowlist enforcement.
// If transport is non-nil it is used as the HTTP client's RoundTripper so that
// all requests are validated against the NetworkGuard allowlist.
func RegisterWebFetchTool(d *Dispatcher, transport http.RoundTripper) {
	d.Register(ToolDef{
		Name:        "web_fetch",
		Description: "Fetch the content of a URL. Subject to network allowlist restrictions.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"url": {"type": "string", "description": "The URL to fetch"}
			},
			"required": ["url"],
			"additionalProperties": false
		}`),
		Fn: webFetchFn(transport),
	})
}

type webFetchArgs struct {
	URL string `json:"url"`
}

func webFetchFn(transport http.RoundTripper) ToolFunc {
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport, // nil falls back to http.DefaultTransport
	}
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a webFetchArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("web_fetch: parse args: %w", err)
		}
		if a.URL == "" {
			return "", fmt.Errorf("web_fetch: empty URL")
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
		if err != nil {
			return "", fmt.Errorf("web_fetch: create request: %w", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("web_fetch: %w", err)
		}
		defer resp.Body.Close()

		// Limit read to 1MB.
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return "", fmt.Errorf("web_fetch: read body: %w", err)
		}

		return fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(body)), nil
	}
}
