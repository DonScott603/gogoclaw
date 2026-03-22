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
- All outbound HTTP must go through the network allowlist.
- PII gate runs before every LLM request (when enabled).
- Skill permissions are enforced by the capability broker, never by the skill itself.
- os.Chmod(0600) is effectively a no-op on Windows — Go can only toggle the read-only attribute, not POSIX user/group/other permissions. Sensitive files like ~/.gogoclaw/env rely on the user home directory's inherited NTFS ACLs for protection, which restrict access to the owning user, Administrators, and SYSTEM by default. This is acceptable for a local single-user tool.

## Directory Map
- cmd/gogoclaw/ — entry point
- internal/engine/ — core agent loop
- internal/provider/ — LLM providers
- internal/skill/ — WASM skill runtime
- internal/tools/ — always-available core tools
- internal/memory/ — vector-backed memory system
- internal/channel/ — communication channels
- internal/config/ — config loading and watching
- internal/security/ — network, path, secrets, signing
- internal/tui/ — bubbletea interface
- pkg/types/ — shared types
- skills/builtin/ — built-in WASM skills
- templates/ — default configs and prompts

## Cleanup Notes
- Test skills `hello-skill` and `hello-world` in `~/.gogoclaw/skills.d/` should be deleted if present — they are leftover from Phase 5 testing.