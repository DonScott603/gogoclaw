package app

import (
	"context"
	"log"
	"net/http"

	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/health"
	"github.com/DonScott603/gogoclaw/internal/mcp"
	"github.com/DonScott603/gogoclaw/internal/tools"
)

// MCPDeps holds MCP client connections and their lifecycle.
type MCPDeps struct {
	Clients []*mcp.Client
}

// InitMCP connects to all enabled MCP servers, registers their tools with
// the dispatcher, and registers each client with the health monitor.
func InitMCP(cfg *config.Config, dispatcher *tools.Dispatcher, monitor *health.Monitor, mcpTransport http.RoundTripper) MCPDeps {
	var clients []*mcp.Client

	for key, serverCfg := range cfg.MCP {
		if !serverCfg.Enabled {
			continue
		}
		name := serverCfg.Name
		if name == "" {
			name = key
		}

		var transport mcp.Transport
		switch serverCfg.Transport {
		case "stdio":
			transport = mcp.NewStdioTransport(serverCfg.Command, serverCfg.Args)
		case "sse":
			log.Printf("mcp: SSE server %q at %s — ensure domain is in network.yaml allowlist", name, serverCfg.URL)
			transport = mcp.NewSSETransport(serverCfg.URL, mcpTransport)
		default:
			log.Printf("mcp: server %q: unknown transport %q (skipping)", name, serverCfg.Transport)
			continue
		}

		client := mcp.NewClient(name, transport)
		ctx := context.Background()

		if err := client.Initialize(ctx); err != nil {
			log.Printf("mcp: server %q: initialize failed: %v (skipping)", name, err)
			client.Close()
			continue
		}

		adapter := mcp.NewSkillAdapter(client)
		if err := adapter.RegisterTools(ctx, dispatcher); err != nil {
			log.Printf("mcp: server %q: register tools failed: %v (skipping)", name, err)
			client.Close()
			continue
		}

		// Register with health monitor.
		monitor.Register(client)

		clients = append(clients, client)
		log.Printf("mcp: server %q connected (%s transport)", name, serverCfg.Transport)
	}

	return MCPDeps{Clients: clients}
}

// Close shuts down all MCP client connections.
func (d *MCPDeps) Close() {
	for _, c := range d.Clients {
		c.Close()
	}
}
