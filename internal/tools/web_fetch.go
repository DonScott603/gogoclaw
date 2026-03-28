package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

		ct := resp.Header.Get("Content-Type")
		if ct != "" && !isTextContentType(ct) {
			return "", fmt.Errorf("web_fetch: unsupported content type: %s (only text/* and application/json accepted)", ct)
		}

		// Limit read to 1MB.
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return "", fmt.Errorf("web_fetch: read body: %w", err)
		}

		return fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(body)), nil
	}
}

// isTextContentType returns true for text-based content types that are safe
// to read as string content.
func isTextContentType(ct string) bool {
	// Strip parameters (e.g., "; charset=utf-8").
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = ct[:idx]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	switch ct {
	case "application/json", "application/xml", "application/javascript":
		return true
	}
	return false
}
