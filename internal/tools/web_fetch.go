package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RegisterWebFetchTool registers web_fetch with a network allowlist check stub.
func RegisterWebFetchTool(d *Dispatcher) {
	d.Register(ToolDef{
		Name:        "web_fetch",
		Description: "Fetch the content of a URL. Subject to network allowlist restrictions.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"url": {"type": "string", "description": "The URL to fetch"}
			},
			"required": ["url"]
		}`),
		Fn: webFetchFn(),
	})
}

type webFetchArgs struct {
	URL string `json:"url"`
}

func webFetchFn() ToolFunc {
	client := &http.Client{Timeout: 30 * time.Second}
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a webFetchArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("web_fetch: parse args: %w", err)
		}
		if a.URL == "" {
			return "", fmt.Errorf("web_fetch: empty URL")
		}

		// TODO(phase4): enforce network allowlist via security/network.go

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
