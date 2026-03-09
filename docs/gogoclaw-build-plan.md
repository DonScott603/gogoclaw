# GoGoClaw Build Plan

## Project Overview

GoGoClaw is a security-first AI agent framework written in Go, designed as an alternative to OpenClaw. Where OpenClaw bolts security on after the fact, GoGoClaw makes isolation and access control foundational. The core is a single Go binary with WASM-based skill sandboxing via wazero, built-in network access control, PII classification and routing, and encrypted storage — all without requiring Docker, Node.js, or external infrastructure.

**License:** Apache 2.0

---

## Architecture

### Design Principles

1. **Security by default** — No implicit capabilities. Every permission is explicitly granted.
2. **Pure Go** — No CGo dependencies. Single static binary. Easy to deploy and cross-compile.
3. **Three-layer separation** — Core engine (library) → Service layer (API) → Interface consumers (TUI, REST, Telegram). The TUI is not the architecture.
4. **Provider agnostic** — Ollama for local inference, any OpenAI-compatible endpoint for cloud, with optional native Anthropic support.
5. **Minimal trust** — Skills run in WASM sandboxes. Network access goes through an allowlist. Secrets are encrypted at rest. Audit everything.

### High-Level Component Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                        Interfaces                           │
│  ┌─────────┐    ┌──────────────┐    ┌───────────────────┐   │
│  │   TUI   │    │  REST API    │    │    Telegram Bot    │   │
│  │(bubblte)│    │  (net/http)  │    │   (telebot lib)   │   │
│  └────┬────┘    └──────┬───────┘    └─────────┬─────────┘   │
│       └────────────────┼──────────────────────┘             │
│                        │ Channel Interface                  │
├────────────────────────┼────────────────────────────────────┤
│                   Service Layer                             │
│  ┌─────────────────────┴──────────────────────────────┐     │
│  │              Agent Engine (core library)            │     │
│  │                                                    │     │
│  │  ┌──────────┐ ┌──────────┐ ┌────────────────────┐  │     │
│  │  │ Context  │ │ Provider │ │   Tool Dispatcher   │  │     │
│  │  │ Assembler│ │ Router   │ │                     │  │     │
│  │  └──────────┘ └──────────┘ └────────────────────┘  │     │
│  │  ┌──────────┐ ┌──────────┐ ┌────────────────────┐  │     │
│  │  │   PII    │ │  Memory  │ │   Config Manager   │  │     │
│  │  │   Gate   │ │  System  │ │                     │  │     │
│  │  └──────────┘ └──────────┘ └────────────────────┘  │     │
│  │  ┌──────────┐ ┌──────────┐ ┌────────────────────┐  │     │
│  │  │  Audit   │ │  Health  │ │   Conversation     │  │     │
│  │  │  Logger  │ │  Monitor │ │   Store            │  │     │
│  │  └──────────┘ └──────────┘ └────────────────────┘  │     │
│  └────────────────────────────────────────────────────┘     │
├─────────────────────────────────────────────────────────────┤
│                    Skill Runtime                            │
│  ┌──────────────────────────────────────────────────────┐   │
│  │              wazero WASM Sandbox                      │   │
│  │  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌───────────┐  │   │
│  │  │ Skill A │ │ Skill B │ │ Skill C │ │ MCP Skill │  │   │
│  │  │ (WASM)  │ │ (WASM)  │ │ (WASM)  │ │ (bridge)  │  │   │
│  │  └─────────┘ └─────────┘ └─────────┘ └───────────┘  │   │
│  └──────────────────────────────────────────────────────┘   │
├─────────────────────────────────────────────────────────────┤
│                     Storage Layer                           │
│  ┌────────────┐  ┌────────────────┐  ┌────────────────┐    │
│  │  SQLite    │  │  chromem-go    │  │  Config Files  │    │
│  │  (convos)  │  │  (vectors)     │  │  (YAML)        │    │
│  └────────────┘  └────────────────┘  └────────────────┘    │
└─────────────────────────────────────────────────────────────┘
```

---

## Project Structure

```
gogoclaw/
├── cmd/
│   └── gogoclaw/
│       └── main.go                 # Entry point, CLI flags, bootstrap detection
├── internal/
│   ├── engine/
│   │   ├── engine.go               # Core agent engine orchestrator
│   │   ├── context.go              # Context assembler (system prompt + memory + history + tools)
│   │   ├── conversation.go         # Conversation lifecycle management
│   │   └── session.go              # Per-conversation state and overrides
│   ├── provider/
│   │   ├── provider.go             # Provider interface definition
│   │   ├── ollama.go               # Native Ollama client
│   │   ├── openai_compat.go        # Generic OpenAI-compatible client
│   │   ├── anthropic.go            # Native Anthropic client (optional)
│   │   ├── router.go               # Provider chain failover logic
│   │   └── token_counter.go        # Token counting per provider/model
│   ├── pii/
│   │   ├── classifier.go           # PII pattern detection engine
│   │   ├── gate.go                 # Routing enforcement (strict/warn/permissive/disabled)
│   │   └── patterns.go             # Regex patterns for SSN, account numbers, etc.
│   ├── skill/
│   │   ├── runtime.go              # wazero WASM runtime manager
│   │   ├── sandbox.go              # Per-skill sandbox with capability broker
│   │   ├── manifest.go             # Skill manifest parsing and validation
│   │   ├── registry.go             # Skill loading, signing verification, hash pinning
│   │   ├── host.go                 # WASM host functions (file I/O, network, etc.)
│   │   └── discovery.go            # Tool discovery for two-tier system
│   ├── mcp/
│   │   ├── client.go               # MCP protocol client (JSON-RPC over stdio/SSE)
│   │   ├── skill_adapter.go        # Wraps MCP server as a GoGoClaw skill
│   │   └── transport.go            # stdio and SSE transport implementations
│   ├── tools/
│   │   ├── core.go                 # Always-available core tools registry
│   │   ├── file_ops.go             # file_read, file_write, file_list, file_search
│   │   ├── shell.go                # shell_exec with confirmation gate
│   │   ├── web_fetch.go            # web_fetch with network allowlist enforcement
│   │   ├── memory_tools.go         # memory_save, memory_search (agent-facing)
│   │   ├── discover.go             # discover_tools meta-tool
│   │   ├── think.go                # think (reasoning scratchpad, no side effects)
│   │   └── dispatcher.go           # Tool call validation, parallel dispatch, timeout
│   ├── memory/
│   │   ├── memory.go               # Memory system orchestrator
│   │   ├── writer.go               # Extract facts from conversations
│   │   ├── retriever.go            # Query vectors + rank by relevance/recency
│   │   ├── manager.go              # Deduplication, decay, consolidation (post-MVP)
│   │   └── vectorstore.go          # VectorStore interface (chromem-go implementation)
│   ├── storage/
│   │   ├── conversations.go        # SQLite conversation/message persistence
│   │   ├── encryption.go           # At-rest encryption for SQLite and memory
│   │   └── migrations.go           # Schema versioning and migrations
│   ├── channel/
│   │   ├── channel.go              # Channel interface definition
│   │   ├── telegram.go             # Telegram Bot API adapter
│   │   ├── rest.go                 # REST API adapter (net/http)
│   │   └── file_transfer.go        # Attachment handling, inbox/outbox routing
│   ├── config/
│   │   ├── config.go               # Config loading, merging, validation
│   │   ├── watcher.go              # fsnotify file watching for live reload
│   │   ├── resolver.go             # Environment variable ${VAR} resolution
│   │   └── schema.go               # Config struct definitions and defaults
│   ├── agent/
│   │   ├── profile.go              # Agent profile loading with base inheritance
│   │   ├── prompt.go               # System prompt assembly with template variables
│   │   └── bootstrap.go            # First-run bootstrap ritual logic
│   ├── security/
│   │   ├── network.go              # Domain allowlist enforcement for all HTTP
│   │   ├── path.go                 # Path traversal prevention and validation
│   │   ├── signing.go              # Skill signing verification
│   │   ├── secrets.go              # Secret storage (env vars, keyring, encrypted config)
│   │   └── sanitizer.go            # Input sanitization between LLM and skills
│   ├── audit/
│   │   ├── logger.go               # Structured JSON Lines audit logging
│   │   └── events.go               # Event type definitions
│   ├── health/
│   │   ├── monitor.go              # Health checking for providers, channels, memory
│   │   └── status.go               # Status types and aggregation
│   └── tui/
│       ├── app.go                  # Bubbletea application root
│       ├── chat.go                 # Chat panel (conversation view)
│       ├── conversations.go        # Conversation list panel
│       ├── health.go               # Health dashboard panel
│       ├── settings.go             # Settings editor panel
│       ├── workspace.go            # Workspace file browser panel
│       └── confirm.go              # Confirmation dialogs (shell_exec, PII overrides)
├── pkg/
│   └── types/
│       ├── message.go              # Shared message types (InboundMessage, OutboundMessage)
│       ├── tool.go                 # Tool definition, tool call, tool result types
│       └── attachment.go           # Attachment type with io.Reader streaming
├── skills/
│   └── builtin/
│       ├── pdf/                    # PDF processing skill (Go → WASM)
│       │   ├── main.go
│       │   └── manifest.yaml
│       ├── csv/                    # CSV/tabular data skill
│       │   ├── main.go
│       │   └── manifest.yaml
│       └── text/                   # Text/markdown processing skill
│           ├── main.go
│           └── manifest.yaml
├── templates/
│   ├── bootstrap.md                # First-run bootstrap conversation template
│   ├── config/                     # Default config file templates
│   │   ├── config.yaml
│   │   ├── providers/
│   │   │   └── example.yaml
│   │   ├── agents/
│   │   │   └── base.yaml
│   │   ├── channels/
│   │   │   ├── telegram.yaml
│   │   │   └── rest.yaml
│   │   ├── skills/
│   │   │   └── example.yaml
│   │   └── network.yaml
│   └── prompts/
│       └── summarize.md            # Rolling summarization prompt template
├── go.mod
├── go.sum
├── Makefile                        # Build targets: build, test, lint, install
├── Dockerfile                      # Minimal container (non-root, read-only rootfs)
├── gogoclaw.service                # systemd unit file with hardening
├── LICENSE                         # Apache 2.0
├── README.md
└── CLAUDE.md                       # Instructions for Claude Code when working on this repo
```

---

## Configuration Structure

All configuration lives under `~/.gogoclaw/`:

```
~/.gogoclaw/
├── config.yaml                     # Core settings (workspace paths, logging, etc.)
├── providers/
│   ├── ollama.yaml                 # Local Ollama provider
│   └── minimax.yaml                # Cloud provider example
├── agents/
│   ├── base.yaml                   # Base agent profile (all others inherit from this)
│   ├── base.md                     # Base system prompt (markdown)
│   ├── financial.yaml              # Financial work agent profile
│   └── financial.md                # Financial agent system prompt
├── channels/
│   ├── telegram.yaml               # Telegram bot config
│   └── rest.yaml                   # REST API config
├── skills/
│   └── csv-processor.yaml          # Per-skill permission overrides
├── network.yaml                    # Global network allowlist
├── memory/
│   └── config.yaml                 # Memory/vector system config
├── data/
│   ├── conversations.db            # Encrypted SQLite (conversations + messages)
│   └── vectors/                    # chromem-go persistence directory
├── workspace/
│   ├── inbox/                      # Files for agent to process (mappable)
│   ├── outbox/                     # Files produced by agent (mappable)
│   ├── scratch/                    # Temporary working files
│   └── documents/                  # Reference material
├── skills.d/                       # Installed skill WASM binaries + manifests
├── memory/
│   ├── long-term.md                # Curated persistent memories (human-readable)
│   └── daily/                      # Daily memory files (YYYY-MM-DD.md)
├── audit/
│   └── gogoclaw.jsonl              # Structured audit log
├── templates/
│   └── bootstrap.md                # Customizable bootstrap template (copied on init)
└── initialized                     # Marker file: bootstrap has been completed
```

### Example Configuration Files

**config.yaml** — Core settings:
```yaml
workspace:
  base: "~/.gogoclaw/workspace"
  inbox: "~/.gogoclaw/workspace/inbox"       # Override with external path
  outbox: "~/.gogoclaw/workspace/outbox"     # Override with external path
  scratch: "~/.gogoclaw/workspace/scratch"
  documents: "~/.gogoclaw/workspace/documents"

logging:
  level: "info"                              # debug | info | warn | error
  audit:
    enabled: true
    path: "~/.gogoclaw/audit/gogoclaw.jsonl"
    encrypt: false

storage:
  conversations:
    path: "~/.gogoclaw/data/conversations.db"
    encrypt: true
    passphrase_env: "GOGOCLAW_DB_PASSPHRASE" # or use OS keyring
  ephemeral_mode: false                      # true = nothing persisted to disk
```

**providers/minimax.yaml** — Cloud provider:
```yaml
name: "minimax"
type: "openai_compatible"
base_url: "https://api.minimax.io/v1"
api_key: ${MINIMAX_API_KEY}
default_model: "MiniMax-M2.1"
max_context_tokens: 204800
timeout: 60s
retry: 1
```

**providers/ollama.yaml** — Local provider:
```yaml
name: "ollama"
type: "ollama"
base_url: "http://localhost:11434/v1"
default_model: "qwen2.5-coder:14b"
max_context_tokens: 8192
timeout: 30s
retry: 2
```

**agents/base.yaml** — Base agent profile:
```yaml
name: "GoGoClaw Assistant"
system_prompt_file: "base.md"

provider_routing:
  mode: "cloud-only"                         # cloud-only | local-only | hybrid
  provider_chain:
    - provider: "minimax"
      timeout: 60s
      retry: 1

pii:
  mode: "disabled"                           # strict | warn | permissive | disabled

skills:
  always_available: true                     # Load core tools
  auto_discover: true                        # Enable discover_tools

context:
  max_history_tokens: 4096                   # How much history to include
  summarization:
    enabled: true
    provider: "minimax"                      # Which provider summarizes
    threshold_tokens: 3072                   # Summarize when history exceeds this

memory:
  enabled: true
  top_k: 10
  relevance_threshold: 0.7
  recency_weight: 0.2

shell:
  confirmation: "always"                     # always | destructive_only | never
```

**agents/financial.yaml** — Inherits from base, overrides for PII:
```yaml
inherits: "base"
name: "Financial Assistant"
system_prompt_file: "financial.md"

provider_routing:
  mode: "hybrid"
  provider_chain:
    - provider: "ollama"
      timeout: 30s
      retry: 2
    - provider: "minimax"
      timeout: 60s
      retry: 1

pii:
  mode: "strict"

skills:
  allowed:
    - "pdf-processor"
    - "csv-processor"
    - "schwab-portal"

context:
  max_history_tokens: 2048                   # Tighter for local model

network:
  additional_allowlist:
    - ".schwab.com"
    - ".fidelity.com"
```

**network.yaml** — Global network allowlist:
```yaml
allowlist:
  - "api.minimax.io"
  - "api.anthropic.com"
  - "api.openai.com"
  - "localhost"
  - "127.0.0.1"
deny_all_others: true
log_blocked: true
```

**memory/config.yaml** — Memory system:
```yaml
enabled: true
embedding:
  provider: "ollama"
  model: "nomic-embed-text"
  fallback_provider: "minimax"               # If Ollama is down
storage:
  backend: "chromem-go"
  path: "~/.gogoclaw/data/vectors"
  encrypted: true
retrieval:
  top_k: 10
  relevance_threshold: 0.7
  recency_weight: 0.2
consolidation:
  enabled: false                             # Post-MVP
  interval: "weekly"
```

**Skill manifest example** — skills.d/csv-processor/manifest.yaml:
```yaml
name: "csv-processor"
version: "1.0.0"
description: "Read, filter, transform, and analyze CSV and tabular data files"
author: "gogoclaw-builtin"
hash: "sha256:a1b2c3d4e5f6..."               # Pinned binary hash

tools:
  - name: "csv_read"
    description: "Read a CSV file and return structured data"
    parameters:
      type: "object"
      properties:
        file_path:
          type: "string"
          description: "Path to CSV file relative to workspace"
        columns:
          type: "array"
          items:
            type: "string"
          description: "Optional: specific columns to return"
        filter:
          type: "string"
          description: "Optional: filter expression (e.g., 'amount > 1000')"
      required: ["file_path"]

  - name: "csv_transform"
    description: "Transform CSV data: sort, group, aggregate, join"
    parameters:
      type: "object"
      properties:
        file_path:
          type: "string"
        operations:
          type: "array"
          items:
            type: "object"
      required: ["file_path", "operations"]

permissions:
  filesystem:
    read:
      - "inbox/*"
      - "documents/*"
    write:
      - "outbox/*"
      - "scratch/*"
  network: false
  env_vars: false
  max_file_size: "10MB"
  max_execution_time: "30s"
```

---

## Security Model

### Layer 1: Skill Sandboxing (wazero WASM)

Every skill runs in an isolated wazero WASM sandbox with zero implicit capabilities.

**How it works:**
- Skills are compiled Go programs targeting `GOOS=wasip1 GOARCH=wasm`
- The wazero runtime instantiates each skill in its own module with no imported host functions by default
- Capabilities are injected as host functions based on the skill's manifest permissions
- A skill with `filesystem.read: ["inbox/*"]` gets a `host_file_read(path) -> bytes` function that validates the path before reading
- A skill with `network: false` gets no network-related host functions at all
- The skill never gets raw syscalls, file handles, or sockets

**Capability broker pattern:**
```go
type CapabilityBroker interface {
    FileRead(skillID string, path string) ([]byte, error)   // validates against manifest
    FileWrite(skillID string, path string, data []byte) error
    HTTPGet(skillID string, url string) ([]byte, error)     // validates against allowlist
    // Skills never call these directly — they're injected as WASM host functions
}
```

### Layer 2: Network Allowlist

All outbound HTTP from the Go core goes through a controlled HTTP client that enforces the global allowlist plus any per-skill/per-agent restrictions.

- Domain matching supports wildcards (`.schwab.com` matches `www.schwab.com`, `api.schwab.com`)
- Blocked requests are logged with the requesting component, target domain, and timestamp
- No DNS resolution bypass — the allowlist operates at the domain level before DNS resolution

### Layer 3: PII Classification Gate

Runs before every LLM request in hybrid or cloud-only mode (when not disabled).

**Detection patterns:**
- SSN formats (XXX-XX-XXXX, XXXXXXXXX)
- Account numbers (common custodian formats)
- Phone numbers (US formats)
- Email addresses
- Names (harder — uses heuristic matching against known client name patterns if configured)
- Credit card numbers (Luhn-validated)

**Modes:**
- `strict` — Block and route to local only. Failover stays local. User override required to send to cloud.
- `warn` — Flag in UI and audit log, proceed to cloud provider. Visible indicator in TUI/Telegram.
- `permissive` — Log only, no user-facing notification. For environments with DPAs in place.
- `disabled` — No detection runs. Minimal overhead.

**Per-conversation override:** User can relax PII mode for a specific conversation without changing global config. TUI shows conversation-level PII indicator.

### Layer 4: Secrets Management

- API keys resolved from environment variables (`${VAR_NAME}` syntax in config)
- Future: OS keyring integration (macOS Keychain, Linux Secret Service)
- Secrets never written to config files, logs, or conversation history
- Memory system scrubs detected secrets before persisting

### Layer 5: Skill Provenance

- **Signing:** Skills can be signed; core verifies signature before loading
- **Hash pinning:** Manifest includes `hash` field; binary is verified at load time
- **Local-only:** No remote skill marketplace. Skills are explicitly installed to `~/.gogoclaw/skills.d/`
- **Audit:** Every skill load, capability grant, and tool invocation is logged

### Layer 6: Input Sanitization

- LLM output → skill input: parsed through schema validation, instruction-like content stripped
- Skill output → LLM context: wrapped in clear delimiters, output length capped per skill config
- Tool call arguments validated against JSON schema before dispatch

### Layer 7: Audit Trail

Structured JSON Lines log at `~/.gogoclaw/audit/gogoclaw.jsonl`:

```json
{"ts":"2026-03-09T10:30:00Z","event":"llm_request","provider":"minimax","model":"MiniMax-M2.1","tokens_in":1240,"tokens_out":856,"pii_detected":false,"agent":"base"}
{"ts":"2026-03-09T10:30:01Z","event":"tool_call","tool":"file_read","skill":"core","args":{"path":"inbox/report.csv"},"result":"ok","duration_ms":12}
{"ts":"2026-03-09T10:30:02Z","event":"network_blocked","domain":"evil.com","requester":"skill:csv-processor","reason":"not_in_allowlist"}
{"ts":"2026-03-09T10:30:03Z","event":"pii_detected","patterns":["ssn","phone"],"mode":"strict","action":"blocked_cloud","provider_attempted":"minimax"}
```

---

## Key Interfaces

These are the core abstractions that make the system modular and testable.

### Channel Interface

```go
// Channel represents a communication channel (Telegram, REST API, etc.)
type Channel interface {
    // Start begins listening for messages
    Start(ctx context.Context) error
    // Stop gracefully shuts down the channel
    Stop(ctx context.Context) error
    // Send delivers a message to a conversation
    Send(ctx context.Context, conversationID string, msg OutboundMessage) error
    // OnMessage registers the handler for incoming messages
    OnMessage(handler func(ctx context.Context, msg InboundMessage))
    // Name returns the channel identifier
    Name() string
}

type InboundMessage struct {
    ConversationID string
    SenderID       string
    Text           string
    Attachments    []Attachment
    Channel        string
    Timestamp      time.Time
}

type OutboundMessage struct {
    Text        string
    Attachments []Attachment
    Metadata    map[string]string // channel-specific (e.g., Telegram parse_mode)
}

type Attachment struct {
    Filename string
    MimeType string
    Size     int64
    Reader   io.Reader
}
```

### Provider Interface

```go
// Provider represents an LLM provider (Ollama, OpenAI-compatible, Anthropic)
type Provider interface {
    // Name returns the provider identifier
    Name() string
    // Chat sends a completion request and returns the response
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    // ChatStream sends a completion request and streams the response
    ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
    // CountTokens estimates token count for the given content
    CountTokens(content string) (int, error)
    // Healthy checks if the provider is reachable
    Healthy(ctx context.Context) bool
}

type ChatRequest struct {
    Model       string
    Messages    []Message
    Tools       []ToolDefinition
    MaxTokens   int
    Temperature float64
}

type ChatResponse struct {
    Content   string
    ToolCalls []ToolCall
    Usage     TokenUsage
}

type ToolCall struct {
    ID        string
    Name      string
    Arguments json.RawMessage
}
```

### VectorStore Interface

```go
// VectorStore abstracts vector storage (chromem-go implementation)
type VectorStore interface {
    // Store embeds and stores a document
    Store(ctx context.Context, doc MemoryDocument) error
    // Search returns the top-k most similar documents to the query
    Search(ctx context.Context, query string, topK int, opts SearchOptions) ([]MemoryResult, error)
    // Delete removes a document by ID
    Delete(ctx context.Context, id string) error
    // Close cleanly shuts down the store
    Close() error
}

type MemoryDocument struct {
    ID        string
    Content   string
    Tags      []string
    Timestamp time.Time
    Source    string // conversation ID that produced this memory
}

type MemoryResult struct {
    Document   MemoryDocument
    Similarity float64 // cosine similarity score
    Score      float64 // blended score (similarity + recency)
}

type SearchOptions struct {
    MinSimilarity float64
    RecencyWeight float64
    Tags          []string // optional tag filter
}
```

### Skill Runtime Interface

```go
// SkillRuntime manages WASM skill execution
type SkillRuntime interface {
    // LoadSkill loads and validates a skill from its WASM binary and manifest
    LoadSkill(ctx context.Context, path string) (*Skill, error)
    // Execute runs a tool call within the skill's sandbox
    Execute(ctx context.Context, skillID string, toolName string, args json.RawMessage) (*ToolResult, error)
    // UnloadSkill removes a skill from the runtime
    UnloadSkill(skillID string) error
    // ListSkills returns all loaded skills and their tools
    ListSkills() []SkillInfo
    // SearchSkills finds skills matching a description (for discover_tools)
    SearchSkills(query string) []ToolDefinition
}

type Skill struct {
    ID       string
    Manifest SkillManifest
    Tools    []ToolDefinition
    Hash     string
    Loaded   time.Time
}

type SkillManifest struct {
    Name        string          `yaml:"name"`
    Version     string          `yaml:"version"`
    Description string          `yaml:"description"`
    Author      string          `yaml:"author"`
    Hash        string          `yaml:"hash"`
    Tools       []ToolSchema    `yaml:"tools"`
    Permissions SkillPermissions `yaml:"permissions"`
}

type SkillPermissions struct {
    Filesystem struct {
        Read  []string `yaml:"read"`
        Write []string `yaml:"write"`
    } `yaml:"filesystem"`
    Network          bool   `yaml:"network"`
    EnvVars          bool   `yaml:"env_vars"`
    MaxFileSize      string `yaml:"max_file_size"`
    MaxExecutionTime string `yaml:"max_execution_time"`
}
```

---

## Phased Implementation Roadmap

### Phase 1: Foundation (Weeks 1–3)

**Goal:** Runnable binary that can send a message to an LLM and get a response through the TUI.

**Tasks:**

1. **Project scaffolding**
   - Initialize Go module (`github.com/[your-username]/gogoclaw`)
   - Set up directory structure per the project structure above
   - Makefile with build, test, lint, install targets
   - CLAUDE.md with repo conventions for Claude Code
   - Apache 2.0 LICENSE file

2. **Configuration system**
   - Config struct definitions with YAML tags
   - Multi-file loader: scan `~/.gogoclaw/` directories, merge configs
   - Environment variable resolver (`${VAR}` → `os.Getenv`)
   - Validation with clear error messages
   - fsnotify watcher for live config reload
   - Template config files in `templates/config/`

3. **Provider layer**
   - Provider interface definition
   - OpenAI-compatible client (covers MiniMax, OpenAI, Groq, etc.)
   - Ollama client (native — handles model management quirks)
   - Provider router with chain failover, exponential backoff
   - Token counter (tiktoken-go for OpenAI-compat, estimate for Ollama)
   - Streaming support (SSE parsing)

4. **Basic TUI**
   - Bubbletea app shell with panel layout
   - Chat panel: input box, message display, streaming response rendering
   - Basic conversation list (single conversation for now)
   - Status bar showing active provider and agent profile

5. **Minimal engine**
   - Message in → provider call → message out loop
   - System prompt loading from agent profile markdown file
   - Conversation history in memory (no persistence yet)

**Milestone:** You can launch GoGoClaw, type a message in the TUI, and get a response from your configured LLM provider.

---

### Phase 2: Core Tools & Storage (Weeks 4–6)

**Goal:** Agent can use tools, conversations persist, workspace is functional.

**Tasks:**

1. **Tool system**
   - Tool definition types and JSON schema validation
   - Tool dispatcher: receive tool calls from LLM, validate, execute, return results
   - Parallel tool call support via goroutines
   - Tool call timeout and cancellation
   - Always-available core tools:
     - `file_read`, `file_write`, `file_list`, `file_search`
     - `shell_exec` with confirmation gate
     - `web_fetch` with allowlist stub (full network enforcement in Phase 4)
     - `think` (reasoning scratchpad)

2. **Conversation persistence**
   - SQLite schema: conversations table, messages table
   - Message storage with token counts and tool call records
   - Conversation listing and loading
   - Application-level encryption for the database

3. **Context assembler**
   - Token budget calculation: total context - system prompt - tools = available for history
   - History truncation to fit budget (newest messages first)
   - Per-provider context limits from config

4. **Workspace**
   - Directory creation and validation at startup
   - External directory mapping from config
   - Inbox/outbox routing
   - Path validation (traversal prevention)

5. **TUI enhancements**
   - Conversation list panel (switch between conversations)
   - Confirmation dialog for shell_exec
   - File display in chat (show when agent reads/writes files)
   - Tool call visualization (show what the agent is doing)

**Milestone:** Agent can read files from the workspace, run shell commands (with confirmation), and conversations survive restarts.

---

### Phase 3: Memory System (Weeks 7–8)

**Goal:** Vector-backed memory with chromem-go, rolling summarization.

**Tasks:**

1. **Vector store integration**
   - VectorStore interface implementation backed by chromem-go
   - Embedding generation via Ollama (`nomic-embed-text`) or cloud provider
   - Persistence configuration (encrypted at rest)

2. **Memory tools**
   - `memory_save`: embed and store a fact with metadata (tags, source conversation, timestamp)
   - `memory_search`: embed query, similarity search, blend with recency, return top-K

3. **Context assembler integration**
   - On each message: embed current conversation context, retrieve relevant memories
   - Inject top-K memories into system prompt under "Relevant memories" section
   - Token budget now accounts for memory section

4. **Rolling summarization**
   - When conversation history exceeds threshold, summarize oldest segment
   - Store summary as a message with `role: "system"` type
   - Extract key facts from summarized segment → memory_save automatically
   - Configurable per provider (skip for local models if desired)

5. **Memory writer**
   - End-of-conversation extraction: parse conversation for facts, preferences, decisions
   - Daily memory file generation (`~/.gogoclaw/memory/daily/YYYY-MM-DD.md`)
   - Human-readable format alongside vector storage

**Milestone:** Agent remembers facts across conversations, retrieves relevant context automatically, and summarizes long conversations.

---

### Phase 4: Security (Weeks 9–11)

**Goal:** Full security model operational — PII gate, network enforcement, audit trail.

**Tasks:**

1. **PII classification gate**
   - Pattern matching engine (regex-based for MVP)
   - SSN, account number, phone, email, credit card patterns
   - Gate integration with provider router: intercept before LLM call
   - Four modes: strict / warn / permissive / disabled
   - Per-conversation override support

2. **Network allowlist enforcement**
   - Custom HTTP transport wrapping `net/http` that checks all outbound requests
   - Global allowlist from `network.yaml`
   - Per-agent additional allowlist from agent profile
   - Per-skill domain restrictions from manifest
   - Blocked request logging

3. **Secrets management**
   - Env var resolution (already built in Phase 1, harden here)
   - Memory/log scrubbing: scan for API key patterns before persisting
   - Conversation history scrubbing

4. **Audit trail**
   - Structured JSON Lines logger
   - Event types: llm_request, tool_call, network_blocked, pii_detected, skill_loaded, config_changed
   - Configurable log levels
   - Optional log encryption

5. **Skill security (preparation for Phase 5)**
   - Skill manifest validation
   - Hash verification at load time
   - Signing verification (if signature present)

6. **Health dashboard**
   - TUI panel showing: provider status, channel status, memory system status, PII mode
   - Periodic health checks (configurable interval)
   - Color-coded indicators

**Milestone:** PII gate blocks/warns on sensitive data, network requests are enforced against allowlist, full audit trail operational, health dashboard visible in TUI.

---

### Phase 5: WASM Skill Runtime (Weeks 12–14)

**Goal:** Skills run in wazero sandboxes with the capability broker pattern.

**Tasks:**

1. **wazero runtime**
   - Module instantiation with zero default imports
   - Host function injection based on manifest permissions
   - Capability broker: file I/O, network access mediated by the core
   - Skill lifecycle: load, execute, unload
   - Resource limits: memory cap, execution timeout, output size limit

2. **Skill SDK**
   - Go package that skills import to interact with host functions
   - Standard request/response serialization (JSON over WASM memory)
   - Example skill template

3. **Two-tier tool discovery**
   - `discover_tools` meta-tool implementation
   - Skill description embedding for similarity search
   - Dynamic tool injection: discovered tools added to context for subsequent turns

4. **Built-in skills**
   - PDF processor (text extraction)
   - CSV processor (read, filter, transform)
   - Text/markdown processor

5. **Skill registry**
   - Scan `~/.gogoclaw/skills.d/` for installed skills
   - Validation: manifest present, hash matches, permissions are valid
   - TUI skill management panel

**Milestone:** Skills run in isolated WASM sandboxes, core tools and discoverable skills both work, built-in skills can process common file types.

---

### Phase 6: Channels & Bootstrap (Weeks 15–17)

**Goal:** Telegram and REST API channels operational, bootstrap ritual complete.

**Tasks:**

1. **Telegram channel**
   - Bot API integration (long polling for MVP, webhook later)
   - Message handling: text, files, inline keyboards for confirmations
   - File transfer: attachments → inbox, outbox files → sent as documents
   - Conversation mapping: Telegram chat ID → GoGoClaw conversation

2. **REST API channel**
   - HTTP server with JSON API
   - Endpoints: POST /message, GET /conversations, GET /health
   - File upload via multipart form
   - API key authentication

3. **Bootstrap ritual**
   - Phase 1 (infrastructure): directory scaffolding, provider detection, default config generation
   - Phase 2 (identity): LLM-driven conversational setup via bootstrap.md template
   - Write results to: user.md, identity.md, agent profiles, provider configs
   - Marker file on completion
   - Works through TUI or Telegram (whichever connects first)

4. **Agent profile system**
   - Base profile inheritance
   - Template variable resolution (`{{current_date}}`, `{{user_name}}`, etc.)
   - Per-conversation prompt overrides
   - Profile switching in TUI

5. **TUI settings panel**
   - Config editor for all major settings
   - Provider management (add/edit/test providers)
   - Agent profile editor
   - Write-back to YAML files
   - Conflict detection with fsnotify

**Milestone:** Full end-to-end flow: bootstrap → configure → chat via TUI, Telegram, or REST API.

---

### Phase 7: MCP Support & Polish (Weeks 18–20)

**Goal:** MCP servers as skill-type, system hardening, documentation.

**Tasks:**

1. **MCP client**
   - JSON-RPC protocol implementation
   - stdio transport (spawn MCP server as subprocess)
   - SSE transport (connect to remote MCP server)
   - Tool discovery: list MCP server's tools, map to GoGoClaw tool definitions

2. **MCP skill adapter**
   - Wrap MCP server connection as a GoGoClaw skill
   - Apply same permission model: allowlist which MCP servers, optionally filter tools
   - Health monitoring for MCP server connections

3. **System hardening**
   - Dockerfile with non-root user, read-only rootfs, no capabilities
   - systemd unit file with full hardening (NoNewPrivileges, ProtectSystem, etc.)
   - Security review of all network paths
   - Fuzz testing for input sanitization

4. **Documentation**
   - README with quick start guide
   - Configuration reference
   - Skill development guide
   - Security model documentation
   - Architecture decision records

5. **Testing**
   - Unit tests for all interfaces
   - Integration tests for provider failover, PII gate, tool dispatch
   - End-to-end test: message in → tool calls → response out

**Milestone:** Production-ready system with MCP support, container deployment, comprehensive docs.

---

## Key Dependencies

| Package | Purpose | License |
|---------|---------|---------|
| `github.com/tetratelabs/wazero` | Pure Go WASM runtime | Apache 2.0 |
| `github.com/philippgille/chromem-go` | Embedded vector database | MPL-2.0 |
| `github.com/charmbracelet/bubbletea` | TUI framework | MIT |
| `github.com/charmbracelet/lipgloss` | TUI styling | MIT |
| `github.com/charmbracelet/bubbles` | TUI components | MIT |
| `github.com/fsnotify/fsnotify` | File system notifications | BSD-3 |
| `gopkg.in/yaml.v3` | YAML config parsing | MIT |
| `modernc.org/sqlite` | Pure Go SQLite driver | BSD-3 |
| `github.com/tiktoken-go/tokenizer` | Token counting | MIT |
| `gopkg.in/telebot.v4` | Telegram Bot API | MIT |

*Note: chromem-go changed from AGPL to MPL-2.0 in v0.7.0. MPL-2.0 is compatible with Apache 2.0 — only modifications to chromem-go's own source files must remain under MPL.*

---

## CLAUDE.md (For Claude Code)

This file should live at the root of the repository:

```markdown
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
```

---

## Open Items to Resolve

1. **chromem-go license (RESOLVED):** chromem-go changed from AGPL to MPL-2.0 in v0.7.0. MPL-2.0 is a weak file-level copyleft compatible with Apache 2.0 in combined works. No license conflict — modifications to chromem-go source files must remain MPL, but the rest of GoGoClaw stays Apache 2.0.

2. **Embedding model for cloud-only users:** Users without Ollama need an embedding endpoint. Confirm which cloud providers' embedding APIs to support (OpenAI, MiniMax, etc.) and implement as fallback.

3. **WASM skill SDK ergonomics:** The developer experience for writing skills needs prototyping. The host function interface (passing data across the WASM boundary) has size and serialization constraints that may need iteration.

4. **Anthropic native provider:** Decide if this is needed for MVP or if LiteLLM / OpenAI-compat shim is sufficient. Anthropic's message format differs enough that features like extended thinking may not work through a shim.

5. **Bootstrap template content:** The actual bootstrap.md conversation script needs to be written and tested with multiple LLM providers to ensure reliable structured data extraction.
