# CLAUDE.md — GoGoClaw Development Guide

## Project
GoGoClaw is a security-first AI agent framework in Go. Single binary, no CGo.

## Build & Run
- `make build` — compile to ./bin/gogoclaw
- `make test` — run all tests
- `make lint` — run golangci-lint
- `make install` — install to $GOPATH/bin

## Architecture Rules
1. No CGo. All dependencies must be pure Go.
2. Three-layer separation: engine (internal/engine) → service → interface (TUI/REST/Telegram).
3. All LLM communication goes through the Provider interface.
4. All tool execution goes through the Tool Dispatcher.
5. All network access goes through the controlled HTTP client (security/network.go).
6. Skills run in wazero WASM sandboxes. Skills never get raw filesystem or network access.

## Code Conventions
- Use `context.Context` for all operations that may block or need cancellation.
- Errors are wrapped with `fmt.Errorf("component: action: %w", err)`.
- Interfaces live in the package that consumes them, not the package that implements them.
- Config structs use `yaml` and `json` tags.
- Test files use table-driven tests.

## Security Rules
- Never log secret values (API keys, passwords, tokens).
- All file paths must be validated through security/path.go before use.
- All outbound HTTP must go through the network allowlist. This includes providers (via injected Transport), web_fetch, and MCP SSE connections. Each gets a separate requester label in audit logs (provider, web_fetch, mcp).
- PII gate runs before every LLM request (when enabled). It scans system, user, and tool messages — only assistant messages are excluded.
- Skill permissions are enforced by the capability broker, never by the skill itself.
- Telegram requires explicit allowlist — empty allowed_users means no access (fail-closed).
- web_fetch validates Content-Type headers and rejects binary responses (only text/* and application/json accepted).
- os.Chmod(0600) is effectively a no-op on Windows — Go can only toggle the read-only attribute, not POSIX user/group/other permissions. Sensitive files like ~/.gogoclaw/env rely on the user home directory's inherited NTFS ACLs for protection, which restrict access to the owning user, Administrators, and SYSTEM by default. This is acceptable for a local single-user tool.

## Directory Map
- cmd/gogoclaw/ — entry point
- internal/engine/ — core agent loop
- internal/provider/ — LLM providers (OpenAI-compatible, Ollama, router)
- internal/skill/ — WASM skill runtime (registry, sandbox, capability broker)
- internal/tools/ — always-available core tools
- internal/memory/ — vector-backed memory system (chromem-go)
- internal/channel/ — communication channels (REST, Telegram)
- internal/config/ — config loading, watching, and validation
- internal/security/ — network guard, path validator, secret scrubber
- internal/pii/ — PII classifier and routing gate
- internal/audit/ — structured JSON Lines audit logging
- internal/agent/ — bootstrap ritual, identity, template variables
- internal/app/ — subsystem initialization (wiring layer)
- internal/mcp/ — MCP client (JSON-RPC over stdio/SSE transports)
- internal/health/ — health monitor with periodic checks
- internal/storage/ — SQLite conversation persistence
- internal/tui/ — bubbletea terminal interface
- pkg/types/ — shared types (InboundMessage, OutboundMessage)
- skills/builtin/ — built-in WASM skills
- templates/ — default configs and bootstrap prompt

## Bootstrap Ritual
The bootstrap runs on first launch when ~/.gogoclaw/initialized is absent. Two phases:

1. **Infrastructure** — creates directory tree under ~/.gogoclaw/ (workspace/, memory/, audit/, skills.d/, agents/, providers/, channels/, mcp/), copies template files (config.yaml, agents/base.md).
2. **Identity** — interactive Q&A through the engine using templates/bootstrap.md. The LLM collects user preferences and outputs a JSON summary. Bootstrap then writes:
   - agents/identity.yaml (user name, agent name, personality, work domain, PII mode)
   - agents/user.md (human-readable user profile)
   - agents/base.yaml (agent config with provider chain, PII mode, memory, shell settings)
   - providers/*.yaml (one per configured provider, with ${ENV_VAR} API key references)
   - channels/rest.yaml and channels/telegram.yaml
   - network.yaml (allowlist with provider domains auto-detected)

After writing configs, bootstrap collects environment variables (API keys) with hidden terminal input and persists them to ~/.gogoclaw/env and via setx on Windows.

Pre-bootstrap, the NetworkGuard temporarily allows common cloud provider domains (api.openai.com, api.anthropic.com, etc.) so the bootstrap LLM call can succeed before network.yaml exists.

## Env File Precedence
~/.gogoclaw/env stores KEY=value pairs loaded at process start. System environment variables take priority — if a variable is already set in the environment (via setx, shell profile, etc.), the env file value is skipped. This prevents stale env file values from overriding corrections made via setx.

## Channel Architecture
- **REST** — net/http server with auth middleware (constant-time comparison), CORS, audit logging, body size limits (1 MB JSON, 32 MB per file), collision-safe upload filenames. NewREST returns error (no fallback key). Wired via app.InitREST.
- **Telegram** — telebot v4 with long polling, access control (fail-closed on empty allowlist), group chat rejection, message splitting for long responses, file upload/download with collision-safe names. Wired via app.InitTelegram.

Both channels prefix messages with `[Channel: REST API]` or `[Channel: Telegram]` so the system prompt can instruct the LLM to respond inline rather than writing files.

## MCP (Model Context Protocol)
Configs live in ~/.gogoclaw/mcp/*.yaml. Two transport types:
- **stdio** — spawns MCP server as subprocess, JSON-RPC via stdin/stdout
- **SSE** — connects to remote server via HTTP SSE (domain must be in network.yaml)

MCP tools are registered on the dispatcher with namespaced names: `mcp_{servername}_{toolname}` to prevent collisions with core tools and other MCP servers. SecurityDeps has a separate `MCPTransport` field (netGuard.Transport("mcp")) for audit label separation from web_fetch.

## Known Windows Quirks
- `date`, `time`, `set /p`, `pause` commands are blocked in shell_exec — they hang on Windows. Error message suggests cmd-compatible alternatives (e.g., `powershell -command Get-Date`).
- Go PATH may need explicit export: `/c/Program Files/Go/bin`
- setx is used for system-wide env var persistence during bootstrap
- os.Chmod(0600) is effectively a no-op (see Security Rules above)
- -race flag is disabled in Makefile (no CGo available)

## Known Limitations
- **Single-session engine** — the engine is shared across all channels with a single conversation context. Per-conversation session isolation is planned for Phase 8.
- **At-rest encryption** — config flags exist (storage.conversations.encrypt, logging.audit.encrypt) but are not yet implemented. Reserved for future use.
- **Conversation persistence** — SQLite store exists with full CRUD but is not wired into live message flow. Conversations are in-memory only during a session.
- **Profile inheritance** — the `inherits` field exists in AgentConfig but runtime merging is not implemented. Only the base agent is used.
- **Provider test coverage** — the provider package has 0 test files. Integration testing relies on mock providers in other packages.

## Cleanup Notes
- Test skills `hello-skill` and `hello-world` in `~/.gogoclaw/skills.d/` should be deleted if present — they are leftover from Phase 5 testing.
