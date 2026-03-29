package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// Transport is the communication layer for an MCP server.
type Transport interface {
	Start(ctx context.Context) error
	Send(msg []byte) error
	Receive() ([]byte, error)
	Close() error
}

// StdioTransport communicates with an MCP server subprocess via stdin/stdout.
type StdioTransport struct {
	command string
	args    []string

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
}

// NewStdioTransport creates a transport that spawns the given command.
func NewStdioTransport(command string, args []string) *StdioTransport {
	return &StdioTransport{command: command, args: args}
}

func (t *StdioTransport) Start(ctx context.Context) error {
	command := t.command
	// On Windows, resolve bare command names via LookPath since they may
	// lack extensions (e.g., "npx" needs to resolve to "npx.cmd").
	if runtime.GOOS == "windows" {
		if resolved, err := exec.LookPath(command); err == nil {
			command = resolved
		}
	}

	t.cmd = exec.CommandContext(ctx, command, t.args...)
	t.cmd.Stderr = os.Stderr

	stdin, err := t.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp: stdio: stdin pipe: %w", err)
	}
	t.stdin = stdin

	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp: stdio: stdout pipe: %w", err)
	}
	t.scanner = bufio.NewScanner(stdout)
	t.scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("mcp: stdio: start %q: %w", t.command, err)
	}
	return nil
}

func (t *StdioTransport) Send(msg []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stdin == nil {
		return fmt.Errorf("mcp: stdio: not started")
	}
	// JSON-RPC messages are newline-delimited.
	if _, err := t.stdin.Write(append(msg, '\n')); err != nil {
		return fmt.Errorf("mcp: stdio: write: %w", err)
	}
	return nil
}

func (t *StdioTransport) Receive() ([]byte, error) {
	if t.scanner == nil {
		return nil, fmt.Errorf("mcp: stdio: not started")
	}
	if !t.scanner.Scan() {
		if err := t.scanner.Err(); err != nil {
			return nil, fmt.Errorf("mcp: stdio: read: %w", err)
		}
		return nil, io.EOF
	}
	return t.scanner.Bytes(), nil
}

func (t *StdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stdin != nil {
		t.stdin.Close()
	}
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Wait()
	}
	return nil
}

// SSETransport communicates with a remote MCP server via HTTP SSE.
type SSETransport struct {
	url    string
	client *http.Client

	mu       sync.Mutex
	msgCh    chan []byte
	errCh    chan error
	cancel   context.CancelFunc
	closed   bool
}

// NewSSETransport creates a transport that connects to the given SSE URL.
// The provided RoundTripper ensures connections go through the NetworkGuard.
func NewSSETransport(url string, transport http.RoundTripper) *SSETransport {
	return &SSETransport{
		url:    url,
		client: &http.Client{Transport: transport},
		msgCh:  make(chan []byte, 64),
		errCh:  make(chan error, 1),
	}
}

func (t *SSETransport) Start(ctx context.Context) error {
	sseCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel

	req, err := http.NewRequestWithContext(sseCtx, http.MethodGet, t.url, nil)
	if err != nil {
		cancel()
		return fmt.Errorf("mcp: sse: create request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := t.client.Do(req)
	if err != nil {
		cancel()
		return fmt.Errorf("mcp: sse: connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return fmt.Errorf("mcp: sse: HTTP %d", resp.StatusCode)
	}

	go t.readSSE(resp.Body)
	return nil
}

func (t *SSETransport) readSSE(body io.ReadCloser) {
	defer body.Close()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			t.msgCh <- []byte(data)
		}
	}
	if err := scanner.Err(); err != nil {
		select {
		case t.errCh <- fmt.Errorf("mcp: sse: read: %w", err):
		default:
		}
	}
}

func (t *SSETransport) Send(msg []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return fmt.Errorf("mcp: sse: closed")
	}

	req, err := http.NewRequest(http.MethodPost, t.url, strings.NewReader(string(msg)))
	if err != nil {
		return fmt.Errorf("mcp: sse: create post request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp: sse: send: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("mcp: sse: send: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (t *SSETransport) Receive() ([]byte, error) {
	select {
	case msg := <-t.msgCh:
		return msg, nil
	case err := <-t.errCh:
		return nil, err
	}
}

func (t *SSETransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	if t.cancel != nil {
		t.cancel()
	}
	return nil
}
