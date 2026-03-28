package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// dispatchWebFetch is a test helper that dispatches a single web_fetch call.
func dispatchWebFetch(t *testing.T, d *Dispatcher, url string) ToolCallResult {
	t.Helper()
	args, _ := json.Marshal(webFetchArgs{URL: url})
	results := d.Dispatch(context.Background(), []ToolCallRequest{
		{ID: "1", Name: "web_fetch", Arguments: args},
	})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	return results[0]
}

func TestWebFetchRejectsNonTextContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("\x89PNG fake image data"))
	}))
	defer srv.Close()

	d := NewDispatcher(30 * time.Second)
	RegisterWebFetchTool(d, nil)

	result := dispatchWebFetch(t, d, srv.URL)
	if !result.IsError {
		t.Fatalf("expected error for image/png content type, got: %s", result.Content)
	}
}

func TestWebFetchAllowsTextContentType(t *testing.T) {
	tests := []struct {
		contentType string
	}{
		{"text/html; charset=utf-8"},
		{"text/plain"},
		{"application/json"},
		{"application/xml"},
		{"application/javascript"},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				fmt.Fprint(w, "ok")
			}))
			defer srv.Close()

			d := NewDispatcher(30 * time.Second)
			RegisterWebFetchTool(d, nil)

			result := dispatchWebFetch(t, d, srv.URL)
			if result.IsError {
				t.Fatalf("unexpected error for %s: %s", tt.contentType, result.Content)
			}
			if result.Content == "" {
				t.Error("expected non-empty result")
			}
		})
	}
}

func TestIsTextContentType(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"text/html", true},
		{"text/plain; charset=utf-8", true},
		{"application/json", true},
		{"application/xml", true},
		{"application/javascript", true},
		{"image/png", false},
		{"application/octet-stream", false},
		{"video/mp4", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isTextContentType(tt.ct)
		if got != tt.want {
			t.Errorf("isTextContentType(%q) = %v, want %v", tt.ct, got, tt.want)
		}
	}
}
