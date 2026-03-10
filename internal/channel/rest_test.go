package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
func newTestREST(t *testing.T, apiKey string) *RESTChannel {
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

	chanCfg := config.ChannelConfig{
		Name:    "rest",
		Enabled: true,
		Listen:  "127.0.0.1:0",
	}
	if apiKey != "" {
		t.Setenv("TEST_REST_API_KEY", apiKey)
		chanCfg.APIKeyEnv = "TEST_REST_API_KEY"
	}

	return NewREST(RESTConfig{
		Channel:  chanCfg,
		Engine:   eng,
		Store:    store,
		Monitor:  mon,
		InboxDir: inboxDir,
	})
}

// handler returns the auth-wrapped handler for use with httptest.
func handler(rc *RESTChannel) http.Handler {
	return rc.server.Handler
}

func TestRESTMessageEndpoint(t *testing.T) {
	rc := newTestREST(t, "")

	body, _ := json.Marshal(messageRequest{Text: "Hi there"})
	req := httptest.NewRequest(http.MethodPost, "/api/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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
	rc := newTestREST(t, "")

	body, _ := json.Marshal(messageRequest{ConversationID: "conv-42", Text: "hello"})
	req := httptest.NewRequest(http.MethodPost, "/api/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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
	rc := newTestREST(t, "")

	body, _ := json.Marshal(messageRequest{Text: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(rc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestRESTAuthRequired(t *testing.T) {
	rc := newTestREST(t, "secret-key-123")

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

func TestRESTHealthEndpoint(t *testing.T) {
	rc := newTestREST(t, "")

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
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
	rc := newTestREST(t, "")

	// Create a conversation in the store.
	rc.store.CreateConversation(context.Background(), storage.Conversation{
		ID:    "c1",
		Title: "Test Convo",
		Agent: "base",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
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

func TestRESTFileUpload(t *testing.T) {
	rc := newTestREST(t, "")

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
	w := httptest.NewRecorder()

	handler(rc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp messageResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Files) != 1 || resp.Files[0] != "test.txt" {
		t.Errorf("files = %v, want [test.txt]", resp.Files)
	}

	// Verify file was saved to inbox.
	data, err := os.ReadFile(filepath.Join(rc.inboxDir, "test.txt"))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(data) != "file contents here" {
		t.Errorf("file content = %q, want %q", string(data), "file contents here")
	}
}
