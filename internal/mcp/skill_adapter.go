package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/DonScott603/gogoclaw/internal/tools"
)

// MCPSkillAdapter wraps an MCP Client as a set of GoGoClaw tools.
type MCPSkillAdapter struct {
	client *Client
}

// NewSkillAdapter creates an adapter for the given MCP client.
func NewSkillAdapter(client *Client) *MCPSkillAdapter {
	return &MCPSkillAdapter{client: client}
}

// RegisterTools calls ListTools on the MCP server and registers each tool
// with the dispatcher. Tool names are prefixed with "mcp_{servername}_" to
// prevent collisions with core tools and other MCP servers.
func (a *MCPSkillAdapter) RegisterTools(ctx context.Context, dispatcher *tools.Dispatcher) error {
	mcpTools, err := a.client.ListTools(ctx)
	if err != nil {
		return err
	}

	prefix := "mcp_" + a.client.Name() + "_"

	for _, mt := range mcpTools {
		// Capture for closure.
		toolName := mt.Name
		params := mt.InputSchema
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}

		dispatcher.Register(tools.ToolDef{
			Name:        prefix + toolName,
			Description: fmt.Sprintf("[MCP:%s] %s", a.client.Name(), mt.Description),
			Parameters:  params,
			Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
				return a.client.CallTool(ctx, toolName, args)
			},
		})
	}
	return nil
}
