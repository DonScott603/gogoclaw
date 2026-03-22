package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DonScott603/gogoclaw/internal/audit"
	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/internal/health"
	"github.com/DonScott603/gogoclaw/internal/provider"
	"github.com/DonScott603/gogoclaw/internal/storage"
)

// mockProvider is a minimal test double for the LLM provider.
type mockProvider struct {
	response string
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Chat(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: m.response, Usage: provider.TokenUsage{TotalTokens: 5}}, nil
}
func (m *mockProvider) ChatStream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 2)
	ch <- provider.StreamChunk{Content: m.response}
	ch <- provider.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}
func (m *mockProvider) CountTokens(content string) (int, error) { return len(content) / 4, nil }
func (m *mockProvider) Healthy(_ context.Context) bool          { return true }

// newTestREST creates a RESTChannel backed by a mock engine and real SQLite store.
func newTestREST(t *testing.T, opts ...func(*config.ChannelConfig)) *RESTChannel {
	t.Helper()

	eng := engine.New(engine.Config{
		Provider:     &mockProvider{response: "Hello from REST!"},
		SystemPrompt: "test",
		MaxContext:   4096,
	})

	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := storage.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	mon := health.NewMonitor(health.MonitorConfig{PIIMode: "disabled"})

	inboxDir := filepath.Join(t.TempDir(), "inbox")
	os.MkdirAll(inboxDir, 0o755)

	var auditBuf bytes.Buffer
	logger := audit.NewLoggerFromWriter(&auditBuf)

	chanCfg := config.ChannelConfig{
		Name:    "rest",
		Enabled: true,
		Listen:  "127.0.0.1:0",
	}
	for _, fn := range opts {
		fn(&chanCfg)
	}

	rc, err := NewREST(RESTConfig{
		Channel:     chanCfg,
		Engine:      eng,
		Store:       store,
		Monitor:     mon,
		AuditLogger: logger,
		InboxDir:    inboxDir,
	})
	if err != nil {
		t.Fatalf("NewREST: %v", err)
	}
	return rc
}

func handler(rc *RESTChannel) http.Handler {
	return rc.server.Handler
}

func TestRESTMessageEndpoint(t *testing.T) {
	rc := newTestREST(t)

	body, _ := json.Marshal(messageRequest{Text: "Hi there"})
	req := httptest.NewRequest(http.MethodPost, "/api/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+rc.APIKey())
	w := httptest.NewRecorder()

	handler(rc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp messageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Response != "Hello from REST!" {
		t.Errorf("response = %q, want %q", resp.Response, "Hello from REST!")
	}
	if resp.ConversationID == "" {
		t.Error("conversation_id should be auto-generated when not provided")
	}
}

func TestRESTMessageWithConversationID(t *testing.T) {
	rc := newTestREST(t)

	body, _ := json.Marshal(messageRequest{ConversationID: "conv-42", Text: "hello"})
	req := httptest.NewRequest(http.MethodPost, "/api/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+rc.APIKey())
	w := httptest.NewRecorder()

	handler(rc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp messageResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.ConversationID != "conv-42" {
		t.Errorf("conversation_id = %q, want %q", resp.ConversationID, "conv-42")
	}
}

func TestRESTMessageEmptyText(t *testing.T) {
	rc := newTestREST(t)

	body, _ := json.Marshal(messageRequest{Text: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+rc.APIKey())
	w := httptest.NewRecorder()

	handler(rc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestRESTAuthRequired(t *testing.T) {
	rc := newTestREST(t, func(c *config.ChannelConfig) {
		c.APIKey = "secret-key-123"
	})

	body, _ := json.Marshal(messageRequest{Text: "hi"})

	// No auth header → 401.
	req := httptest.NewRequest(http.MethodPost, "/api/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(rc).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no auth: status = %d, want 401", w.Code)
	}

	// Wrong key → 401.
	req = httptest.NewRequest(http.MethodPost, "/api/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-key")
	w = httptest.NewRecorder()
	handler(rc).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong auth: status = %d, want 401", w.Code)
	}

	// Correct key → 200.
	body, _ = json.Marshal(messageRequest{Text: "hi"})
	req = httptest.NewRequest(http.MethodPost, "/api/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-key-123")
	w = httptest.NewRecorder()
	handler(rc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("correct auth: status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestRESTAuthAutoGenerated(t *testing.T) {
	rc := newTestREST(t)

	if rc.APIKey() == "" {
		t.Fatal("auto-generated API key should not be empty")
	}

	// Without key → 401.
	body, _ := json.Marshal(messageRequest{Text: "hi"})
	req := httptest.NewRequest(http.MethodPost, "/api/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(rc).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no auth: status = %d, want 401", w.Code)
	}

	// With auto key → 200.
	body, _ = json.Marshal(messageRequest{Text: "hi"})
	req = httptest.NewRequest(http.MethodPost, "/api/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+rc.APIKey())
	w = httptest.NewRecorder()
	handler(rc).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("auto key: status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestRESTHealthEndpoint(t *testing.T) {
	rc := newTestREST(t)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Authorization", "Bearer "+rc.APIKey())
	w := httptest.NewRecorder()
	handler(rc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp healthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PIIMode != "disabled" {
		t.Errorf("pii_mode = %q, want %q", resp.PIIMode, "disabled")
	}
}

func TestRESTConversationsEndpoint(t *testing.T) {
	rc := newTestREST(t)

	rc.store.CreateConversation(context.Background(), storage.Conversation{
		ID:    "c1",
		Title: "Test Convo",
		Agent: "base",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	req.Header.Set("Authorization", "Bearer "+rc.APIKey())
	w := httptest.NewRecorder()
	handler(rc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var convos []conversationPayload
	if err := json.NewDecoder(w.Body).Decode(&convos); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(convos) != 1 {
		t.Fatalf("got %d conversations, want 1", len(convos))
	}
	if convos[0].ID != "c1" {
		t.Errorf("id = %q, want %q", convos[0].ID, "c1")
	}
}

func TestRESTConversationsPagination(t *testing.T) {
	rc := newTestREST(t)

	for i := 0; i < 5; i++ {
		rc.store.CreateConversation(context.Background(), storage.Conversation{
			ID:    fmt.Sprintf("c%d", i),
			Title: fmt.Sprintf("Convo %d", i),
			Agent: "base",
		})
	}

	// Limit=2, offset=0 → 2 results.
	req := httptest.NewRequest(http.MethodGet, "/api/conversations?limit=2&offset=0", nil)
	req.Header.Set("Authorization", "Bearer "+rc.APIKey())
	w := httptest.NewRecorder()
	handler(rc).ServeHTTP(w, req)

	var convos []conversationPayload
	json.NewDecoder(w.Body).Decode(&convos)
	if len(convos) != 2 {
		t.Fatalf("limit=2: got %d, want 2", len(convos))
	}

	// Offset=3 → 2 results (items 3,4).
	req = httptest.NewRequest(http.MethodGet, "/api/conversations?limit=10&offset=3", nil)
	req.Header.Set("Authorization", "Bearer "+rc.APIKey())
	w = httptest.NewRecorder()
	handler(rc).ServeHTTP(w, req)

	json.NewDecoder(w.Body).Decode(&convos)
	if len(convos) != 2 {
		t.Fatalf("offset=3: got %d, want 2", len(convos))
	}
}

func TestRESTFileUpload(t *testing.T) {
	rc := newTestREST(t)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.WriteField("text", "check this file")
	writer.WriteField("conversation_id", "upload-test")

	part, err := writer.CreateFormFile("file", "test.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	part.Write([]byte("file contents here"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/message", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+rc.APIKey())
	w := httptest.NewRecorder()

	handler(rc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp messageResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Files) != 1 {
		t.Fatalf("files count = %d, want 1", len(resp.Files))
	}
	if !strings.HasSuffix(resp.Files[0], "_test.txt") {
		t.Errorf("file = %q, want suffix _test.txt (collision-safe name)", resp.Files[0])
	}

	data, err := os.ReadFile(filepath.Join(rc.inboxDir, resp.Files[0]))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(data) != "file contents here" {
		t.Errorf("file content = %q, want %q", string(data), "file contents here")
	}
}

func TestRESTCORSAllowedOrigin(t *testing.T) {
	rc := newTestREST(t, func(c *config.ChannelConfig) {
		c.AllowedOrigins = []string{"https://app.example.com"}
	})

	// Allowed origin → CORS headers set.
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Authorization", "Bearer "+rc.APIKey())
	w := httptest.NewRecorder()
	handler(rc).ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("CORS origin = %q, want %q", got, "https://app.example.com")
	}

	// Disallowed origin → no CORS headers.
	req = httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Authorization", "Bearer "+rc.APIKey())
	w = httptest.NewRecorder()
	handler(rc).ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disallowed origin: CORS origin = %q, want empty", got)
	}
}

func TestRESTCORSPreflight(t *testing.T) {
	rc := newTestREST(t, func(c *config.ChannelConfig) {
		c.AllowedOrigins = []string{"https://app.example.com"}
	})

	req := httptest.NewRequest(http.MethodOptions, "/api/message", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	handler(rc).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("preflight should set Access-Control-Allow-Methods")
	}
}

func TestRESTAuditLogging(t *testing.T) {
	var auditBuf bytes.Buffer
	logger := audit.NewLoggerFromWriter(&auditBuf)

	eng := engine.New(engine.Config{
		Provider:     &mockProvider{response: "audited"},
		SystemPrompt: "test",
		MaxContext:   4096,
	})
	mon := health.NewMonitor(health.MonitorConfig{PIIMode: "disabled"})

	rc, err := NewREST(RESTConfig{
		Channel:     config.ChannelConfig{Name: "rest", Enabled: true, APIKey: "key"},
		Engine:      eng,
		Monitor:     mon,
		AuditLogger: logger,
		InboxDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewREST: %v", err)
	}

	body, _ := json.Marshal(messageRequest{Text: "hi"})
	req := httptest.NewRequest(http.MethodPost, "/api/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	handler(rc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// Parse the audit log line.
	logBytes := auditBuf.Bytes()
	nlIdx := bytes.IndexByte(logBytes, '\n')
	if nlIdx < 0 {
		t.Fatalf("no audit log line written; raw: %q", auditBuf.String())
	}
	var event audit.Event
	if err := json.Unmarshal(logBytes[:nlIdx], &event); err != nil {
		t.Fatalf("parse audit event: %v; raw: %s", err, auditBuf.String())
	}
	if event.Type != audit.EventRESTRequest {
		t.Errorf("event type = %q, want %q", event.Type, audit.EventRESTRequest)
	}
	if event.Fields["method"] != "POST" {
		t.Errorf("method = %q, want POST", event.Fields["method"])
	}
	if event.Fields["path"] != "/api/message" {
		t.Errorf("path = %q, want /api/message", event.Fields["path"])
	}
	if event.Fields["status"] != "200" {
		t.Errorf("status = %q, want 200", event.Fields["status"])
	}
}
