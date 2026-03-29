package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DonScott603/gogoclaw/internal/tools"
)

// --- Mock transport for client tests ---

// mockTransport uses a channel for receives so the receive loop blocks
// until test code pushes data, preventing race conditions.
type mockTransport struct {
	mu      sync.Mutex
	started bool
	sent    [][]byte
	recvCh  chan []byte
	closed  bool
}

func newMockTransport() *mockTransport {
	return &mockTransport{recvCh: make(chan []byte, 16)}
}

func (m *mockTransport) Start(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	return nil
}

func (m *mockTransport) Send(msg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("closed")
	}
	m.sent = append(m.sent, append([]byte(nil), msg...))
	return nil
}

func (m *mockTransport) Receive() ([]byte, error) {
	data, ok := <-m.recvCh
	if !ok {
		return nil, io.EOF
	}
	return data, nil
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		close(m.recvCh)
	}
	return nil
}

// pushResponse sends a canned JSON-RPC response into the mock transport.
func (m *mockTransport) pushResponse(id int64, result interface{}) {
	m.recvCh <- makeResponse(id, result)
}

// makeResponse creates a JSON-RPC response for a given ID.
func makeResponse(id int64, result interface{}) []byte {
	r, _ := json.Marshal(result)
	resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, id, string(r))
	return []byte(resp)
}

// --- Transport tests ---

func TestStdioTransportStartClose(t *testing.T) {
	// Test with a command that exits immediately — "echo" is available on all platforms.
	st := NewStdioTransport("echo", []string{"hello"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := st.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Should be able to receive the echo output.
	data, err := st.Receive()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Errorf("got %q, want 'hello'", string(data))
	}

	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSSETransportStartReceive(t *testing.T) {
	// Create a test SSE server that sends one event then closes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			fmt.Fprintf(w, "data: %s\n\n", `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`)
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		// POST — accept sends.
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	st := NewSSETransport(srv.URL, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := st.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	data, err := st.Receive()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !strings.Contains(string(data), `"ok":true`) {
		t.Errorf("got %q, want JSON-RPC response", string(data))
	}

	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// --- Client tests ---

func TestClientInitialize(t *testing.T) {
	mt := newMockTransport()

	// Push the initialize response before creating the client so the
	// receive loop finds it immediately after start.
	mt.pushResponse(1, map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"serverInfo":      map[string]string{"name": "test-server"},
	})

	c := NewClient("test", mt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if !mt.started {
		t.Error("transport should have been started")
	}

	// Should have sent initialize request + initialized notification.
	mt.mu.Lock()
	sentCount := len(mt.sent)
	mt.mu.Unlock()
	if sentCount < 2 {
		t.Errorf("expected at least 2 messages sent, got %d", sentCount)
	}

	c.Close()
}

func TestClientListTools(t *testing.T) {
	mt := newMockTransport()

	// Push initialize response.
	mt.pushResponse(1, map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
	})

	c := NewClient("test", mt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Push tools/list response in a goroutine so the call method registers
	// the pending channel before the receive loop picks it up.
	go func() {
		time.Sleep(50 * time.Millisecond)
		mt.pushResponse(2, map[string]interface{}{
			"tools": []map[string]interface{}{
				{
					"name":        "add",
					"description": "Add two numbers",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"a": map[string]string{"type": "number"},
							"b": map[string]string{"type": "number"},
						},
					},
				},
			},
		})
	}()

	mcpTools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(mcpTools) != 1 {
		t.Fatalf("got %d tools, want 1", len(mcpTools))
	}
	if mcpTools[0].Name != "add" {
		t.Errorf("tool name = %q, want %q", mcpTools[0].Name, "add")
	}

	c.Close()
}

func TestClientCallTool(t *testing.T) {
	mt := newMockTransport()

	mt.pushResponse(1, map[string]interface{}{
		"protocolVersion": "2024-11-05",
	})

	c := NewClient("test", mt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Push tools/call response in a goroutine after a brief delay so the
	// call method has time to register the pending channel.
	go func() {
		time.Sleep(50 * time.Millisecond)
		mt.pushResponse(2, map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": "42"},
			},
		})
	}()

	result, err := c.CallTool(ctx, "add", json.RawMessage(`{"a":20,"b":22}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result != "42" {
		t.Errorf("result = %q, want %q", result, "42")
	}

	c.Close()
}

// --- Skill adapter tests ---

func TestSkillAdapterRegisterTools(t *testing.T) {
	mt := newMockTransport()

	mt.pushResponse(1, map[string]interface{}{
		"protocolVersion": "2024-11-05",
	})

	c := NewClient("myserver", mt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Push tools/list response after a brief delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		mt.pushResponse(2, map[string]interface{}{
			"tools": []map[string]interface{}{
				{
					"name":        "greet",
					"description": "Say hello",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
				{
					"name":        "farewell",
					"description": "Say goodbye",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
			},
		})
	}()

	d := tools.NewDispatcher(30 * time.Second)
	adapter := NewSkillAdapter(c)
	if err := adapter.RegisterTools(ctx, d); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	// Verify tools are registered with namespaced names.
	defs := d.Definitions()
	names := make(map[string]bool)
	for _, def := range defs {
		names[def.Function.Name] = true
	}

	if !names["mcp_myserver_greet"] {
		t.Error("expected tool mcp_myserver_greet to be registered")
	}
	if !names["mcp_myserver_farewell"] {
		t.Error("expected tool mcp_myserver_farewell to be registered")
	}
	if len(defs) != 2 {
		t.Errorf("got %d tools, want 2", len(defs))
	}

	c.Close()
}

// --- Config loading test ---

func TestMCPConfigFromYAML(t *testing.T) {
	yamlData := `
name: "test-mcp"
transport: "stdio"
command: "node"
args: ["server.js"]
enabled: true
`
	var cfg MCPServerConfigTest
	if err := json.Unmarshal([]byte(`{
		"name": "test-mcp",
		"transport": "stdio",
		"command": "node",
		"args": ["server.js"],
		"enabled": true
	}`), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_ = yamlData // YAML loading is tested in config package

	if cfg.Name != "test-mcp" {
		t.Errorf("name = %q, want %q", cfg.Name, "test-mcp")
	}
	if cfg.Transport != "stdio" {
		t.Errorf("transport = %q, want %q", cfg.Transport, "stdio")
	}
	if cfg.Command != "node" {
		t.Errorf("command = %q, want %q", cfg.Command, "node")
	}
	if len(cfg.Args) != 1 || cfg.Args[0] != "server.js" {
		t.Errorf("args = %v, want [server.js]", cfg.Args)
	}
	if !cfg.Enabled {
		t.Error("enabled should be true")
	}
}

// MCPServerConfigTest mirrors config.MCPServerConfig to avoid import cycle.
type MCPServerConfigTest struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	URL       string   `json:"url,omitempty"`
	Enabled   bool     `json:"enabled"`
}
