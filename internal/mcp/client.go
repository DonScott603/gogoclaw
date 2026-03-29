// Package mcp implements the Model Context Protocol client,
// supporting JSON-RPC over stdio and SSE transports.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
)

// ToolDefinition is the MCP tool schema returned by tools/list.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Client is an MCP protocol client over any Transport.
type Client struct {
	name      string
	transport Transport

	mu       sync.Mutex
	pending  map[int64]chan json.RawMessage
	nextID   atomic.Int64
	caps     json.RawMessage // server capabilities from initialize
	closed   bool
	recvDone chan struct{}
}

// NewClient creates an MCP client with the given name and transport.
func NewClient(name string, transport Transport) *Client {
	return &Client{
		name:      name,
		transport: transport,
		pending:   make(map[int64]chan json.RawMessage),
		recvDone:  make(chan struct{}),
	}
}

// Name returns the server name this client is connected to.
func (c *Client) Name() string { return c.name }

// jsonrpcRequest is a JSON-RPC 2.0 request.
type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// jsonrpcResponse is a JSON-RPC 2.0 response.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"` // for notifications
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Initialize starts the MCP session — sends initialize, receives capabilities.
func (c *Client) Initialize(ctx context.Context) error {
	if err := c.transport.Start(ctx); err != nil {
		return err
	}

	// Start background receiver.
	go c.receiveLoop()

	params := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]string{
			"name":    "gogoclaw",
			"version": "1.0.0",
		},
	}

	result, err := c.call(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("mcp: %s: initialize: %w", c.name, err)
	}
	c.caps = result

	// Send initialized notification (no response expected).
	return c.notify("notifications/initialized", nil)
}

// ListTools calls tools/list and returns the MCP tool definitions.
func (c *Client) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	result, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: tools/list: %w", c.name, err)
	}

	var resp struct {
		Tools []ToolDefinition `json:"tools"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("mcp: %s: parse tools/list: %w", c.name, err)
	}
	return resp.Tools, nil
}

// CallTool invokes a tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	params := map[string]interface{}{
		"name": name,
	}
	if len(args) > 0 {
		var argsMap interface{}
		if err := json.Unmarshal(args, &argsMap); err == nil {
			params["arguments"] = argsMap
		}
	}

	result, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return "", fmt.Errorf("mcp: %s: tools/call %s: %w", c.name, name, err)
	}

	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		// Fall back to returning the raw result.
		return string(result), nil
	}

	var texts []string
	for _, c := range resp.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		}
	}
	content := ""
	if len(texts) > 0 {
		content = texts[0]
		for _, t := range texts[1:] {
			content += "\n" + t
		}
	}
	if resp.IsError {
		return "", fmt.Errorf("mcp: %s: tool %s error: %s", c.name, name, content)
	}
	return content, nil
}

// Healthy checks if the transport is still connected by sending a ping.
func (c *Client) Healthy(ctx context.Context) bool {
	_, err := c.call(ctx, "ping", nil)
	return err == nil
}

// Close sends a shutdown request and closes the transport.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	// Best-effort shutdown — don't block forever.
	_ = c.transport.Close()
	return nil
}

func (c *Client) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ch := make(chan json.RawMessage, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.transport.Send(data); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("mcp: %s: connection closed", c.name)
		}
		return result, nil
	}
}

func (c *Client) notify(method string, params interface{}) error {
	msg := struct {
		JSONRPC string      `json:"jsonrpc"`
		Method  string      `json:"method"`
		Params  interface{} `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.transport.Send(data)
}

func (c *Client) receiveLoop() {
	defer close(c.recvDone)
	for {
		data, err := c.transport.Receive()
		if err != nil {
			c.mu.Lock()
			closed := c.closed
			// Drain all pending channels on error.
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			if !closed {
				log.Printf("mcp: %s: receive error: %v", c.name, err)
			}
			return
		}

		var resp jsonrpcResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			log.Printf("mcp: %s: invalid JSON-RPC message: %v", c.name, err)
			continue
		}

		// Notification (no ID).
		if resp.ID == nil {
			if resp.Method != "" {
				log.Printf("mcp: %s: notification: %s", c.name, resp.Method)
			}
			continue
		}

		c.mu.Lock()
		ch, ok := c.pending[*resp.ID]
		c.mu.Unlock()

		if !ok {
			log.Printf("mcp: %s: unexpected response ID %d", c.name, *resp.ID)
			continue
		}

		if resp.Error != nil {
			// Send error as nil result — caller checks channel close.
			ch <- json.RawMessage(fmt.Sprintf(`{"error":%q}`, resp.Error.Message))
		} else {
			ch <- resp.Result
		}
	}
}
