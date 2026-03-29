# GoGoClaw Post-Phase 7 Roadmap

## Context

Phases 1–7 deliver a production-ready, security-first AI agent framework: single Go binary, WASM-sandboxed skills, seven security layers, TUI/Telegram/REST channels, MCP support, vector-backed memory, container deployment, and comprehensive documentation.

This roadmap covers Phases 8–12, expanding GoGoClaw from a single-user agent framework into a multi-channel, multi-runtime, orchestration-capable agent platform.

**Known footguns carried forward from Phase 7:**
- `shell_exec` blocklist covers `date`, `time`, `set /p`, and `pause` on Windows, but does not cover `choice`, `more`, `cmd /k`, or PowerShell's `Read-Host`. Unix interactive commands (`vi`, `less`, `top`, `read`) are not blocked at all. A configurable execution timeout with process kill is needed (addressed in Phase 8a).
- Clean up test skills (`hello-skill`, `hello-world`) in `~/.gogoclaw/skills.d/`.
- REST API has no rate limiting. Valid API keys can fire unlimited requests (addressed in Phase 8a).
- Audit log (`gogoclaw.jsonl`) is append-only but has no tamper detection. An attacker with filesystem access can silently modify or truncate it (addressed in Phase 9f).
- Token counting uses `len(content)/4` heuristic everywhere. Context window budgeting is unreliable for models with large context windows (128k+). Real tokenizer integration is needed (addressed in Phase 8c).
- Bootstrap hardcodes `max_context_tokens: 8192` in generated provider YAML, which is wrong for GPT-4o (128k), Claude (200k), etc. Needs model-aware defaults (addressed in Phase 9d).

**Resolved in Phase 7b (no longer footguns):**
- ~~Windows `date` command hangs `shell_exec`~~ — blocked with error message suggesting `powershell -command Get-Date`.
- ~~Network allowlist bypassed by providers~~ — NetworkGuard transport injected into all provider HTTP clients.
- ~~REST API key logged in plaintext~~ — now logs only key length.
- ~~PII gate only scanned user messages~~ — now scans user, tool, and system messages.
- ~~Telegram open by default with empty allowlist~~ — now fail-closed.
- ~~Upload files overwrite on collision~~ — timestamp-prefixed filenames.
- ~~Symlink traversal in path validation~~ — EvalSymlinks resolution added.
- ~~debug_prompt.txt written to disk~~ — removed.
- ~~REST fallback key "gogoclaw-fallback-key"~~ — removed, fails startup instead.

---

## Phase 8: Foundation Hardening & Provider Expansion

**Goal:** Fix the shared-engine architecture that all four code audits identified as the #1 correctness bug, add operational hardening, then expand provider support and introduce intelligent model routing.

### Phase 8a: Per-Conversation Session State & Operational Hardening

**Goal:** Eliminate cross-conversation context bleed and add missing operational safety mechanisms.

**Why this is first:** The engine currently holds a single `history` slice and a single `convID` as instance fields, shared across TUI, REST, and Telegram. Every audit independently identified this as the most critical architectural issue. Multi-channel features in Phases 9–12 are architecturally unsound without this fix. REST and Telegram should not be exposed to multiple users until this is resolved.

**Tasks:**

1. **Per-conversation session state**
   - Extract `history []provider.Message` and `convID string` from `Engine` into a `Session` struct
   - `SessionManager` keyed by channel + conversation ID, with session creation/lookup/cleanup
   - Each channel request gets its own session with independent history, PII mode, and agent profile
   - Engine becomes stateless: shared provider, dispatcher, assembler config, but no per-conversation state
   - Remove `Engine.SetConversationID` — it is a design smell that all audits flagged

2. **Wire conversation persistence**
   - Messages written to SQLite per-session via `storage.Store.AddMessage` on every user/assistant/tool message
   - Session restore on reconnect: load history from SQLite when a known conversation ID is received
   - This makes the SQLite store a system of record, not dead code (flagged by all audits)

3. **Graceful shutdown with context propagation**
   - Create a root context with `signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)` in `main.go`
   - Propagate to all subsystems: Telegram long-polling, REST HTTP server, health monitor, MCP clients
   - On signal: flush pending messages, close channels, stop health monitor, close MCP clients, close storage
   - Replace `context.Background()` in channel message handlers and memory search with the propagated context

4. **`shell_exec` configurable timeout**
   - Add `shell.timeout` field to agent config (default: 30s)
   - Wrap `exec.CommandContext` with a context derived from the configured timeout
   - On timeout, kill the process and return an error: "Command timed out after 30s and was terminated"
   - This replaces the incomplete blocklist approach as the primary safety mechanism — the blocklist remains as a fast-path UX improvement

5. **REST API rate limiting**
   - Add a simple token bucket rate limiter to the REST auth middleware
   - Configurable in channel config: `rate_limit` (requests per minute, default: 60)
   - Return 429 Too Many Requests when exceeded
   - Rate limit is per API key (relevant when multiple keys are supported in the future)

6. **Audit logging for LLM requests and tool calls**
   - Wire `auditDeps.Logger.LogLLMRequest` into the provider path (log model, token counts, latency — not content)
   - Wire `auditDeps.Logger.LogToolCall` into the dispatcher callbacks
   - These event types exist in the audit logger but were never emitted (flagged by audits)

**Milestone:** Each conversation has independent state. Multi-channel operation is correct. The system shuts down cleanly. Shell commands can't hang forever. REST API resists abuse. All LLM and tool activity is audit-logged.

---

### Phase 8b: Telegram Webhook Mode

**Goal:** Replace long polling with webhook-based message delivery for production Telegram deployments.

**Tasks:**

1. **Webhook HTTP handler**
   - Add webhook endpoint to the existing Telegram channel
   - TLS requirement handling (Let's Encrypt integration or reverse proxy documentation)
   - Webhook registration with Telegram Bot API on startup
   - Graceful fallback to long polling if webhook setup fails

2. **Configuration**
   - `webhook_url` field in channel config (empty = long polling)
   - `webhook_port` for the listener
   - Optional `webhook_secret` for request verification

3. **Testing**
   - Verify message delivery parity between polling and webhook modes
   - Test webhook re-registration on restart

**Milestone:** Telegram channel supports both long polling (development) and webhook (production) modes via config toggle.

---

### Phase 8c: Native Anthropic Provider

**Goal:** First-class Anthropic API support with features that don't work through OpenAI-compatible shims.

**Tasks:**

1. **Anthropic client implementation**
   - Native Messages API client (`/v1/messages`)
   - Anthropic-specific message format (system prompt as top-level field, content blocks)
   - Streaming support via SSE
   - Tool use with Anthropic's native tool format

2. **Extended thinking support**
   - Map Anthropic's extended thinking blocks to GoGoClaw's internal representation
   - Display thinking content in TUI (collapsible section)
   - Configurable: enable/disable extended thinking per agent profile

3. **Vision support**
   - Image content blocks in messages
   - Read images from workspace via `file_read`, encode as base64 for the API
   - Display image references in TUI chat

4. **Provider router integration**
   - Register as a first-class provider type (alongside `openai_compat` and `ollama`)
   - Provider config template: `~/.gogoclaw/providers/anthropic.yaml`
   - Failover compatibility with other providers (graceful degradation when Anthropic-specific features aren't available on fallback)

5. **Token counting**
   - Anthropic tokenizer integration (or estimation based on documented rates)
   - Context window management for Claude models (200K context)

**Milestone:** GoGoClaw can use Claude models natively with full tool use, extended thinking, and vision — features unavailable through OpenAI-compatible shims.

---

### Phase 8d: Tiered Model Routing Policy

**Goal:** Automatically route messages to the most cost-effective model that can handle the task.

**Tasks:**

1. **Routing policy engine**
   - Policy definition in `~/.gogoclaw/routing.yaml`
   - Rule-based routing (Phase 1): message length, tool call presence, conversation history depth
   - Tiered model definitions: "fast" (gpt-4o-mini), "standard" (gpt-4o), "reasoning" (Claude Opus)

2. **Classification heuristics**
   - Simple questions (short input, no tools needed) → fast tier
   - Tool-using tasks (file operations, web fetch) → standard tier
   - Complex reasoning (long context, multi-step analysis) → reasoning tier
   - User override: `/model <tier>` command to force a tier for current conversation

3. **Routing policy config**
   ```yaml
   routing:
     enabled: true
     default_tier: "standard"
     tiers:
       fast:
         provider: "openai"
         model: "gpt-4o-mini"
         max_input_tokens: 500
         triggers:
           - "simple_question"
           - "greeting"
       standard:
         provider: "openai"
         model: "gpt-4o"
       reasoning:
         provider: "anthropic"
         model: "claude-sonnet-4-20250514"
         triggers:
           - "multi_tool"
           - "long_context"
           - "code_generation"
   ```

   *Note: Model identifiers in the example above are illustrative. Users should use current model identifiers from their provider's documentation.*

4. **Audit integration**
   - Log routing decisions in audit trail: which tier was selected, why, what the alternatives were
   - Cost tracking integration: show savings from routing vs. always using the reasoning tier

**Milestone:** GoGoClaw intelligently routes to cheaper models for simple tasks and more capable models for complex ones, reducing costs without sacrificing quality.

---

## Phase 9: UX & TUI Polish

**Goal:** Refine the user experience across bootstrap, settings management, and daily TUI usage. These improvements define the interaction patterns that the Web UI (Phase 10a) will replicate.

### Phase 9a: TUI Settings Panel with Restart

**Goal:** In-app configuration editing with live reload or controlled restart, covering all settings initially configured during bootstrap.

**Tasks:**

1. **Settings panel UI**
   - New TUI panel (keybinding: F3 or similar) with categorized settings
   - Categories: Identity & Preferences, Providers & Routing, Security, Memory, Channels
   - Form-style editing with validation feedback
   - Read current values from loaded config, write back to YAML and markdown files
   - Visual indicator for unsaved changes

2. **Provider management**
   - Add new providers: guided form matching bootstrap's provider setup flow
   - Edit existing providers: change model, base URL, API key env var, test connectivity
   - Remove providers: confirmation dialog, check if provider is referenced in routing tiers
   - Reorder provider failover chain via drag-style up/down controls
   - Test provider connectivity from the settings panel (send a ping message, show latency)

3. **Routing mode management**
   - Switch between simple fallback and tiered routing
   - When switching to tiered: prompt user to assign providers to tiers (fast/standard/reasoning)
   - Edit tier assignments: move providers between tiers
   - Preview routing behavior: "Based on current config, a simple greeting would go to [GPT-4o-mini] and a code generation request would go to [Claude Sonnet]"
   - Disable/enable routing without losing tier configuration

4. **Identity & preference editing**
   - Edit all fields from `user.md` and `identity.yaml`: name, agent name, personality, work domain, expertise level
   - Edit interaction preferences: verbosity, proactiveness, uncertainty handling, reasoning visibility
   - Edit tool behavior preferences: shell confirmation mode, file write confirmation mode
   - Changes regenerate the relevant sections of `user.md` and update `identity.yaml`

5. **Channel management**
   - Enable/disable channels (Telegram, REST, and future channels)
   - Edit channel-specific settings (ports, tokens, env vars)
   - Show channel connection status

6. **Restart mechanism**
   - Detect which config changes require restart vs. live reload
   - Live reload (no restart): PII mode, memory thresholds, log levels, `user.md` content
   - Restart required: provider add/remove, channel enable/disable, routing mode change
   - Confirmation dialog: "These changes require a restart. Restart now? [Y/n]"
   - Graceful restart: flush pending messages, close channels, re-initialize engine

7. **Conflict detection**
   - fsnotify integration to detect external config file changes while settings panel is open
   - Warning: "Config file modified externally. Reload? [Y/n]"

**Milestone:** Users can manage all configuration — providers, routing, identity, preferences, channels — from within the TUI without editing YAML files. The settings panel is the ongoing management surface for everything bootstrap sets up initially.

---

### Phase 9b: Provider Cost Tracking

**Goal:** Track and display LLM usage costs per provider, model, and time period.

**Tasks:**

1. **Pricing table**
   - Embedded pricing data for common models (GPT-4o, GPT-4o-mini, Claude Sonnet, Claude Opus, etc.)
   - YAML-based custom pricing overrides in `~/.gogoclaw/pricing.yaml`
   - Per-model input/output token rates
   - Support for free/local models (Ollama) with $0.00 rates

2. **Cost calculation**
   - Hook into the existing audit trail (`llm_request` events already log `tokens_in`/`tokens_out`)
   - Real-time cost accumulation per conversation, per day, per provider
   - Store running totals in SQLite (new `usage_costs` table)

3. **Dashboard integration**
   - Health dashboard (F2) gains a cost section:
     - Today's spend, this week, this month
     - Breakdown by provider and model
     - Cost per conversation (average and current)
     - If tiered routing is enabled: estimated savings vs. always using the reasoning tier
   - Optional cost alerts: warn when daily spend exceeds a configurable threshold

4. **REST API extension**
   - `GET /api/usage` endpoint returning cost data with date range filters
   - Include cost-per-message in conversation detail responses

**Milestone:** Users can see exactly how much each provider costs them, with daily/weekly/monthly breakdowns in the TUI and REST API.

---

### Phase 9c: Skills Manager & Dependencies Panel

**Goal:** A dedicated TUI panel for managing skills, MCP servers, runtimes, and package dependencies.

**Tasks:**

1. **Skills Manager panel (F4)**
   - New TUI panel accessible via F4 keybinding
   - Table view of all installed skills with columns:
     - Name, runtime type (wasm / python / typescript), status (loaded / error / disabled)
     - Sandbox level (🔒 sandboxed / 🔒 full isolation / ⚠ basic isolation)
     - Source (built-in / user-installed)
     - Tool count, last executed timestamp
   - Selecting a skill expands to show full manifest details:
     - Tools provided with descriptions and parameter schemas
     - Permissions: filesystem paths (read/write), network domains, env vars
     - Resource limits: max file size, max execution time, memory cap
     - Hash verification status, skill version, author

2. **Skill actions**
   - Disable/enable a skill without uninstalling (useful for troubleshooting)
   - Uninstall a skill with confirmation dialog (warns if other skills depend on shared cache packages)
   - Install a new skill: path prompt or URL input, shows platform security context before confirming
   - For subprocess skills: run smoke test from the panel
   - For all skills: view recent tool call history (filtered audit log for that skill)

3. **MCP servers section**
   - List of configured MCP servers with connection status (connected / disconnected / error)
   - Available tools per server
   - Health check: test connectivity from the panel
   - Add/remove/edit MCP server configurations
   - Restart individual MCP server connections

4. **Dependencies sub-tab**
   - Accessible as a tab within the Skills panel (e.g., Tab key toggles between Skills and Dependencies)
   - **Interpreters section:**
     - Python and Node availability, version, location (system PATH vs. `~/.gogoclaw/runtimes/`)
     - Whether GoGoClaw-managed or system-installed
     - Actions: install/update standalone runtime, switch interpreter version
   - **Package cache section:**
     - Total cache size at `~/.gogoclaw/cache/packages/`
     - Package list: name, version, size, which skills use it (shared vs. unique)
     - Actions: clear unused packages, clear entire cache with confirmation
   - **Per-skill environments section:**
     - For each subprocess skill: venv size, dependency count, installed packages with versions
     - Visual indicator for shared packages (e.g., "pandas 2.1.0 — shared with csv-processor, data-analysis")
     - Actions: rebuild venv (useful after corrupted install), update dependencies

5. **Disk usage summary**
   - Total disk usage across all skills, venvs, and cache
   - Breakdown: WASM skills (small), subprocess skill code (small), venvs + cache (potentially large)
   - Comparison: actual disk usage vs. theoretical (without cache deduplication) to show savings

**Milestone:** Users have full visibility into what skills are installed, what permissions they have, what sandbox they run in, and how much disk their dependencies consume — all manageable from within the TUI.

---

### Phase 9d: Bootstrap Improvements

**Goal:** Expand the bootstrap ritual to capture richer user preferences and behavioral expectations, producing a detailed `user.md` profile that shapes agent behavior from the first interaction.

**Tasks:**

1. **Atomic question structure**
   - Split all compound questions into discrete steps (one answer per prompt)
   - Current problem: questions like "Do you want Telegram? If yes, what env var?" require two answers in one prompt, leading to unreliable extraction
   - Fix: "Do you want to enable Telegram?" → if yes → "What env var holds your bot token? (default: GOGOCLAW_TELEGRAM_TOKEN)"
   - Apply the same split to REST API questions (enabled → port → API key env var)

2. **New bootstrap sections**

   The bootstrap expands from ~11 questions to ~20-25 atomic questions, organized into sections:

   **Section 1: Identity (existing, cleaned up)**
   - What's your name?
   - What should the agent be called?
   - What's your primary work domain?

   **Section 2: Providers (existing, cleaned up)**
   - Primary provider selection (OpenAI / Ollama / Other)
   - Model preference
   - Fallback provider? → if yes, repeat provider questions
   - Provider strategy: "How should your providers work together?"
     - Simple fallback (always use primary, failover to secondary)
     - Tiered routing (cheaper models for simple tasks, capable models for complex ones)
   - If tiered: "Which provider should handle complex tasks?" (select from configured providers)

   **Section 3: Interaction Preferences (new)**
   - How verbose should responses be? (concise / balanced / detailed)
   - When the agent is uncertain, should it ask you or make reasonable assumptions?
   - Should the agent explain its reasoning, or just give results?
   - How proactive should the agent be? (just do what's asked / suggest next steps / actively spot issues)

   **Section 4: Tool Behavior (new)**
   - Shell commands: always ask first, ask for destructive only, or just run them?
   - File operations: confirm before writing/overwriting, or just do it?
   - Web fetching: any domains you commonly work with that should be in the allowlist?

   **Section 5: Work Style (new)**
   - What's your expertise level in your domain? (learning / intermediate / expert)
   - Do you prefer code comments and explanations, or clean minimal code?
   - Any specific tools, frameworks, or languages you work with daily?
   - Do you work solo or with a team?

   **Section 6: Security & Channels (existing, now atomic)**
   - PII mode selection (strict / warn / permissive / disabled)
   - Telegram: enabled? → if yes → token env var?
   - REST API: enabled? → if yes → port? → API key env var?

   **Section 7: Summary & Confirmation (existing)**

3. **Rich `user.md` generation**
   - Bootstrap produces a detailed natural-language profile instead of the current four-line summary
   - The profile is injected into the system prompt and shapes agent behavior from the first interaction
   - Example output:
     ```markdown
     # User Profile

     Name: Scott
     Expertise: Expert-level software engineer
     Domain: Go development, security tooling
     Work Style: Solo developer, prefers concise responses

     ## Interaction Preferences
     - Be terse and technical. Skip introductory explanations.
     - When uncertain, make reasonable assumptions and note them rather than asking.
     - Proactively flag potential issues but don't over-explain.
     - For code: minimal comments, no boilerplate explanations.
     - Shell commands: ask before destructive operations, run read-only commands freely.
     - Always provide bash commands in separate code blocks for easy copy-paste.

     ## Provider Strategy
     - Routing mode: tiered
     - Simple questions and greetings → GPT-4o-mini (fast, cheap)
     - Complex reasoning, code generation, multi-step analysis → Claude Sonnet (capable)
     - If either provider is unavailable, fall back to the other

     ## Tools & Frameworks
     - Primary language: Go (1.26.1)
     - Regularly uses: Git Bash (MINGW64), bubbletea, wazero, SQLite
     - Preferred shell: Git Bash on Windows

     ## Domain Context
     - Building GoGoClaw, a security-first AI agent framework
     - Cares deeply about: WASM sandboxing, capability-based security, audit trails
     ```

4. **Structured identity data**
   - `identity.yaml` retains programmatically-needed fields: user_name, agent_name, pii_mode, routing_mode, bootstrap_version
   - New fields added: expertise_level, verbosity, proactiveness, shell_confirm_mode, file_write_confirm_mode
   - Behavioral preferences live in `user.md` as natural language (flexible, easy to extend)
   - Infrastructure config stays in YAML (structured, machine-readable)

5. **BootstrapSummary expansion**
   - New fields on the summary struct to capture all new preferences
   - `routing_mode`: "hybrid" or "tiered" (matches existing validated config values from Phase 7b; "hybrid" covers the simple failover chain use case)
   - `reasoning_provider`: which provider handles complex tasks (empty for fallback mode)
   - `verbosity`, `proactiveness`, `uncertainty_handling`, `reasoning_visibility`
   - `shell_confirm_mode`, `file_write_confirm_mode`
   - `expertise_level`, `code_style`, `daily_tools`, `team_or_solo`

6. **Re-bootstrap support**
   - `gogoclaw bootstrap --redo` reruns the bootstrap even if the initialized marker exists
   - Preserves existing config as backup before overwriting
   - Useful for users who want to reconfigure after learning what the settings mean
   - TUI settings panel (9a) can trigger a re-bootstrap from the Identity section
   - Skills manager (9c) handles skill-related configuration separately from bootstrap

7. **Bootstrap template improvements**
   - Update `templates/bootstrap.md` with the expanded question flow
   - Test with multiple LLM providers (GPT-4o-mini, Claude Sonnet, Ollama/Llama3) to ensure reliable structured extraction
   - Ensure the LLM asks exactly one question per turn and waits for the response

**Milestone:** Bootstrap captures a comprehensive user profile that makes the agent feel personalized from the first interaction. Every preference is also editable through the TUI settings panel (9a).

---

### Phase 9e: TUI Enhancements

**Goal:** Quality-of-life improvements and new information panels for daily TUI usage.

**Tasks:**

1. **Audit log viewer (F5)**
   - Scrollable, filterable list of audit events from `~/.gogoclaw/audit/gogoclaw.jsonl`
   - Filter by event type: `llm_request`, `tool_call`, `network_blocked`, `pii_detected`, `skill_loaded`, `config_changed`
   - Filter by date range, provider, skill, or conversation
   - Selecting an event shows its full JSON payload in a detail pane
   - With tiered routing: see which tier each request was routed to and why
   - Highlight security-relevant events (`network_blocked`, `pii_detected`) with color coding
   - Export filtered results to a file

2. **Memory browser (F6)**
   - Scrollable list of stored memories, newest first
   - Search across all memories by keyword
   - Each entry shows: content, tags, source conversation, timestamp, relevance score
   - Delete individual memories
   - Memory system stats: total memory count, vector store size on disk
   - Relevance threshold adjustment (currently 0.3) — test different thresholds against a query
   - Useful for debugging "why did the agent remember/forget that?"

3. **Workspace browser (F7)**
   - Directory tree view of workspace: inbox, outbox, scratch, documents
   - File listing with name, size, modification time
   - View file contents in a read-only pane
   - Actions: delete files from scratch, move files between directories
   - Drag context: when browsing a file, option to "mention in chat" (inserts a file_read reference)

4. **Conversation search**
   - Full-text search across all stored conversations
   - Search results show conversation title, date, and matching snippet
   - Navigate directly to a conversation from search results

5. **Conversation export**
   - Export any conversation as markdown, JSON, or plain text
   - Export to outbox or a user-specified path
   - Batch export: export all conversations in a date range

6. **Keyboard shortcuts and command palette**
   - Ctrl+K (or similar) opens a command palette with fuzzy search
   - Common actions: switch conversation, change agent, toggle PII mode, open settings, jump to any panel
   - Customizable key bindings in config
   - Especially important with 7+ panels — users can type "skills" or "memory" instead of remembering F-keys

7. **Slash commands**
   - `/agent switch <n>` — switch agent profile for current conversation
   - `/pii <mode>` — change PII mode for current conversation
   - `/memory search <query>` — search memory from chat
   - `/model <tier>` — force a routing tier for current conversation
   - `/export` — export current conversation
   - `/cost` — show cost for current conversation
   - `/skills` — list available skills and their status
   - Consistent across all channels (TUI, Telegram, Discord, Slack, etc.)

8. **TUI visual polish**
   - Configurable color themes (dark, light, solarized, custom)
   - Improved tool call visualization: collapsible panels, timing information
   - Streaming response indicators (typing animation, token counter)
   - Status bar enhancements: show active routing tier, current cost, memory hit count

**Full TUI panel architecture after Phase 9:**
```
Escape  Chat (default)
F1      Conversations
F2      Health & Costs
F3      Settings
F4      Skills & Dependencies
F5      Audit Log
F6      Memory Browser
F7      Workspace Browser
Ctrl+K  Command Palette (fuzzy search to any panel or action)
```

**Milestone:** The TUI is a comprehensive, power-user-friendly interface with full skill management, audit visibility, memory browsing, workspace navigation, search, export, shortcuts, and visual refinements.

---

### Phase 9f: Audit Log Integrity

**Goal:** Add tamper detection to the audit trail.

**Tasks:**

1. **HMAC chain**
   - Each audit log entry includes an HMAC of the current entry + previous entry's HMAC, creating a hash chain
   - HMAC key derived from a configurable secret (env var or config)
   - Verification tool: `gogoclaw audit verify` checks the chain and reports any broken links
   - First entry in a new log file includes a chain-start marker

2. **Log rotation**
   - Configurable log rotation by size or date (default: rotate daily, keep 30 days)
   - Each rotated file includes its final HMAC for cross-file chain verification

**Milestone:** Audit log tampering is detectable. Compliance-sensitive deployments can verify log integrity.

---

## Phase 10: Interfaces & Channels

**Goal:** Expand GoGoClaw's reach with a web interface and additional messaging platform channels. The Web UI inherits all interaction patterns, settings categories, skills management, and panel architecture established in Phase 9.

### Phase 10a: Web UI

**Goal:** Browser-based interface backed by the existing REST API channel.

**Tasks:**

1. **Go-served SPA**
   - Static file server embedded in the GoGoClaw binary via `embed.FS`
   - Served on a configurable port (default: 8080)
   - No build step required for users — the UI ships inside the binary

2. **Core UI components**
   - Chat interface with streaming response rendering
   - Conversation list with search (mirrors TUI conversation search from 9e)
   - File upload (drag-and-drop, maps to REST API multipart upload)
   - File download from outbox
   - Tool call visualization (expandable panels showing what the agent did)
   - Markdown rendering for agent responses

3. **Settings panel**
   - Web equivalent of the TUI settings panel (9a)
   - Same categories: Identity & Preferences, Providers & Routing, Security, Memory, Channels
   - Provider add/edit/remove with connectivity test
   - Routing mode management (switch between fallback and tiered, assign providers to tiers)
   - Identity and preference editing (regenerates `user.md`)
   - Restart notification when required

4. **Skills & dependencies manager**
   - Web equivalent of the TUI skills panel (9c)
   - Skill listing with status, permissions, sandbox level, runtime type
   - Install/remove/disable skills
   - Dependencies view: interpreters, package cache, per-skill environments, disk usage
   - MCP server management

5. **Status and dashboards**
   - Health dashboard mirroring TUI's F2 panel
   - Provider status, cost tracking display, routing tier activity
   - Audit log viewer with filtering (mirrors TUI F5 panel)
   - Memory browser with search (mirrors TUI F6 panel)
   - PII mode indicator
   - Agent profile selector

6. **Authentication**
   - Reuse REST API key authentication
   - Session management with configurable timeout
   - Optional: disable auth for localhost-only deployments

7. **Technology choices**
   - Minimal JavaScript framework (vanilla JS, htmx, or Preact — no heavy build toolchain)
   - Server-Sent Events for streaming responses (reuse REST API's SSE support)
   - Mobile-responsive layout

**Milestone:** Users can interact with GoGoClaw from any browser, including full settings management, skills management, and audit visibility, making it accessible to non-terminal users and enabling remote access to self-hosted deployments.

---

### Phase 10b: Discord Channel

**Goal:** Discord bot integration following the established channel pattern.

**Tasks:**

1. **Discord bot client**
   - Discord Gateway (WebSocket) connection for real-time messaging
   - Message handling: text, file attachments, embeds
   - Slash command registration for GoGoClaw commands (`/agent`, `/pii`, `/memory`, `/model`, `/cost`)
   - File transfer: attachments → inbox, outbox files → sent as Discord attachments

2. **Conversation mapping**
   - Discord channel ID or DM → GoGoClaw conversation
   - Thread support: replies within a Discord thread stay in the same GoGoClaw conversation
   - Multi-server support: one GoGoClaw instance can serve multiple Discord servers

3. **Channel behavior**
   - Prepend `[Channel: Discord]` to messages (same pattern as Telegram)
   - Channel behavior instructions in system prompt
   - Rate limiting awareness (Discord API rate limits)

4. **Configuration**
   ```yaml
   channels:
     discord:
       enabled: true
       bot_token: "${DISCORD_BOT_TOKEN}"
       allowed_servers: []           # empty = all servers the bot is in
       allowed_channels: []          # empty = respond in all channels
       command_prefix: "!"           # for non-slash-command invocations
   ```

**Milestone:** GoGoClaw operates as a Discord bot with full tool use, file handling, and conversation persistence.

---

### Phase 10c: Slack Channel

**Goal:** Slack workspace integration for enterprise/team use cases.

**Tasks:**

1. **Slack bot client**
   - Slack Events API (Socket Mode for MVP, HTTP events for production)
   - Message handling: text, file attachments, rich text blocks
   - Slash commands: `/gogoclaw` with subcommands
   - File transfer: Slack file uploads → inbox, outbox files → uploaded to Slack

2. **Conversation mapping**
   - Slack channel/DM → GoGoClaw conversation
   - Thread support: threaded replies stay in the same conversation
   - App mention vs. direct message handling

3. **Enterprise considerations**
   - Workspace-level configuration
   - Per-channel agent profile overrides
   - Respect Slack message formatting (mrkdwn, blocks)
   - Rate limiting compliance

4. **Configuration**
   ```yaml
   channels:
     slack:
       enabled: true
       bot_token: "${SLACK_BOT_TOKEN}"
       app_token: "${SLACK_APP_TOKEN}"    # for Socket Mode
       signing_secret: "${SLACK_SIGNING_SECRET}"
       respond_to_mentions: true
       respond_in_threads: true
   ```

**Milestone:** GoGoClaw operates as a Slack bot, making it deployable in team/enterprise environments.

---

### Phase 10d: Signal Channel

**Goal:** Signal messenger integration, aligning with GoGoClaw's security-first positioning.

**Tasks:**

1. **Signal bridge integration**
   - Integration via signal-cli (Java-based CLI tool) or signald as a bridge
   - Communication with the bridge over JSON-RPC socket or stdin/stdout
   - Message handling: text, attachments, group messages
   - File transfer: Signal attachments → inbox, outbox files → sent as Signal attachments

2. **Signal-specific considerations**
   - Signal has no public bot API — requires a registered phone number
   - End-to-end encryption is handled by the bridge (transparent to GoGoClaw)
   - Group message support: respond when mentioned or to all messages (configurable)
   - Delivery receipts and typing indicators

3. **Dependency management**
   - Document signal-cli/signald installation as a prerequisite
   - Health check for bridge process availability
   - Auto-detection of bridge binary location

4. **Configuration**
   ```yaml
   channels:
     signal:
       enabled: true
       bridge: "signal-cli"              # or "signald"
       bridge_path: "/usr/local/bin/signal-cli"
       phone_number: "${SIGNAL_PHONE}"
       trust_all_keys: false             # require verified safety numbers
       respond_in_groups: "mention_only" # "all", "mention_only", "disabled"
   ```

**Note:** Signal's heavier dependency footprint (Java runtime for signal-cli) makes this channel more complex to deploy than Discord/Slack. Documentation should cover Docker-based deployment where the bridge runs as a sidecar container.

**Milestone:** GoGoClaw is reachable via Signal, offering a privacy-focused messaging option consistent with the project's security-first philosophy.

---

## Phase 11: Skills & Integrations

**Goal:** Expand GoGoClaw's capabilities with built-in skills, API integrations, and Python/TypeScript skill support.

### Phase 11a: Built-in Skills (PDF, Excel, Document Processing)

**Goal:** Ship common document processing capabilities as WASM skills.

**Tasks:**

1. **PDF processor skill**
   - Text extraction from PDF files
   - Page-level access (read specific pages)
   - Metadata extraction (title, author, page count)
   - Tools: `pdf_read`, `pdf_extract_pages`, `pdf_metadata`

2. **Excel/CSV processor skill**
   - Read and write Excel (.xlsx) and CSV files
   - Column selection, filtering, sorting
   - Basic aggregation (sum, average, count, group-by)
   - Tools: `spreadsheet_read`, `spreadsheet_transform`, `spreadsheet_write`

3. **Document processor skill**
   - Read Word (.docx) documents
   - Markdown conversion
   - Text extraction and section parsing
   - Tools: `doc_read`, `doc_to_markdown`

4. **Skill packaging**
   - Compile as `GOOS=wasip1 GOARCH=wasm`
   - Ship in `skills/builtin/` directory, installed by default during bootstrap
   - Manifest with conservative permissions (read inbox/documents, write outbox/scratch)

**Milestone:** GoGoClaw can natively process PDFs, spreadsheets, and Word documents without external tools.

---

### Phase 11b: Built-in API Tools (Brave Search, Tavily)

**Goal:** Add high-frequency search and retrieval APIs as core tools (alongside `web_fetch`).

**Tasks:**

1. **Brave Search integration**
   - Core tool: `brave_search` — web search with structured results
   - API key via environment variable (`${BRAVE_API_KEY}`)
   - Result formatting: title, URL, snippet for each result
   - Rate limiting and error handling
   - Network allowlist: `api.search.brave.com` added automatically when tool is enabled

2. **Tavily Search integration**
   - Core tool: `tavily_search` — AI-optimized search with extracted content
   - API key via environment variable (`${TAVILY_API_KEY}`)
   - Search depth configuration (basic vs. advanced)
   - Network allowlist: `api.tavily.com`

3. **Tool availability**
   - Tools are registered only when their API key is configured
   - Health dashboard shows API tool status: configured/unconfigured
   - Bootstrap ritual (9d) optionally prompts for these API keys

**Milestone:** GoGoClaw has purpose-built search tools that return richer results than raw `web_fetch`.

---

### Phase 11c: API Integration Skills (1Password, Resend)

**Goal:** Specialized API integrations as installable skills.

**Tasks:**

1. **1Password skill**
   - Integration via 1Password CLI (`op`) or Connect Server API
   - Tools: `secret_lookup`, `secret_list` (read-only for safety)
   - Strict permissions: no write access, specific vault scoping
   - Manifest requires explicit user confirmation at install time

2. **Resend skill (email sending)**
   - Tools: `send_email` with confirmation gate (similar to `shell_exec`)
   - Template support for common email formats
   - Audit logging of all sent emails
   - API key via environment variable

3. **Skill distribution**
   - Packaged as WASM skills with manifests
   - Available in a GoGoClaw skills repository (Git-hosted)
   - Install via `gogoclaw skill install <url>`

**Milestone:** GoGoClaw can securely access secrets and send emails through modular, installable skills.

---

### Phase 11d: Python/TypeScript Skill Runtime — Design Document

**Goal:** Formal design document covering all architectural decisions for subprocess-based skill execution.

**Design decisions captured in this document:**

1. **Runtime model**
   - Subprocess with capability broker protocol
   - Same manifest format and permissions model as WASM skills
   - Same `CapabilityBroker` validates all capability requests regardless of runtime

2. **Security model — platform-adaptive sandboxing**
   - **Linux (strong sandbox):** seccomp-bpf syscall filtering (block `socket`, `connect`, `open` outside allowed fds, `execve`) + Landlock filesystem isolation (skill can only see its own directory and broker-granted paths) + PID namespace isolation + scrubbed environment variables. Enforcement is at the OS kernel level — a malicious skill genuinely cannot bypass it.
   - **macOS (basic protections):** Clean environment (no inherited env vars except explicit ones), restricted working directory (temp scratch space), timeout enforcement via process kill, stdin/stdout-only communication. The capability broker protocol is the primary enforcement mechanism. **Users should only run trusted skills.**
   - **Windows (basic protections):** Same as macOS baseline — clean environment, restricted working directory, timeout enforcement. Job Objects for resource limits (memory, CPU) but no filesystem or network isolation. **Users should only run trusted skills.**
   - **Network governance:** Subprocess skills do not get direct network access. HTTP requests go through the capability broker, which proxies them through the existing `NetworkGuard` transport (established in Phase 7b). This ensures subprocess skills are subject to the same domain allowlist as WASM skills and core tools.
   - **Graceful degradation within Linux:** Detect kernel capabilities at startup. Landlock requires 5.13+, seccomp-bpf requires 3.17+. Report sandbox level in health dashboard: `full isolation` / `partial (no filesystem isolation)` / `basic (same as macOS/Windows)`.
   - **User communication:** Skill install time shows platform-specific security context. TUI/channels show sandbox level per skill. Documentation includes a clear platform comparison table.

3. **Capability broker protocol**
   - JSON-RPC 2.0 over newline-delimited JSON on stdin/stdout
   - Stderr reserved for debug output (SDK redirects `print()` there)
   - Synchronous request/response: skill sends capability request, blocks until host responds
   - Host-side handler reuses the existing `CapabilityBroker` — same code path as WASM skills

   **Protocol methods:**
   ```
   execute                  Host → Skill    Initiate tool execution
   host.file_read           Skill → Host    Read a text file (broker validates path)
   host.file_write          Skill → Host    Write a text file (broker validates path + size)
   host.file_read_binary    Skill → Host    Read binary file (base64-encoded response)
   host.file_write_binary   Skill → Host    Write binary file (base64-encoded data)
   host.file_list           Skill → Host    List directory contents
   host.http_get            Skill → Host    HTTP GET (broker validates domain against allowlist)
   host.http_post           Skill → Host    HTTP POST (broker validates domain against allowlist)
   host.env_get             Skill → Host    Read environment variable (broker validates name)
   host.get_workspace_info  Skill → Host    Get logical workspace paths (inbox, outbox, scratch, documents)
   host.log                 Skill → Host    Log a message at specified level
   ```

   **Error codes (JSON-RPC):**
   ```
   -32700  Parse error
   -32600  Invalid request
   -32601  Method not found
   -32602  Invalid params
   -32001  Permission denied (path, network, env var)
   -32002  Resource not found
   -32003  Resource limit exceeded (max_file_size, max_execution_time)
   -32004  Network error (allowlist passed but request failed)
   -32005  Skill error (unhandled exception in skill code)
   ```

   **Example flow:**
   ```
   Host ──── execute(analyze_csv, {file_path: "inbox/data.csv"}) ────> Skill
   Host <──── host.file_read({path: "inbox/data.csv"}) ──────────────── Skill
   Host ──── result: {data: "csv contents..."} ──────────────────────> Skill
        [skill processes data with pandas]
   Host <──── host.file_write({path: "outbox/report.md", data: "..."}) Skill
   Host ──── result: {bytes_written: 2048} ──────────────────────────> Skill
   Host <──── exec-1 result: {result: "Analysis complete..."} ──────── Skill
   ```

4. **Lifecycle model**
   - **Initial implementation: one-shot.** Spawn process per invocation, clean exit after each tool call. Simple, secure, no state leakage.
   - **Future optimization: warm pool.** Keep process alive for a configurable idle timeout (default: 30s). Reuse for sequential tool calls within the same conversation turn. Kill on idle. Per-skill configuration via `lifecycle` manifest field.
   - **Long-running mode (future):** For skills that load ML models or maintain connections. Requires explicit manifest opt-in and stronger permission/confirmation gates.
   - **Manifest lifecycle field:**
     ```yaml
     lifecycle: "one-shot"       # one-shot | warm | long-running
     idle_timeout: "30s"         # warm mode only
     ```

5. **Dependency management**
   - **GoGoClaw-managed installation:** `gogoclaw skill install <path>` handles the full lifecycle
   - **Per-skill virtual environments:** Each Python skill gets its own `.venv` in its skill directory
   - **Shared package cache:** `~/.gogoclaw/cache/packages/` deduplicates packages by name+version across skills. Skill venvs symlink/hard-link to cached packages. A skill needing `pandas==2.1.0` that's already cached uses zero additional disk for that dependency.
   - **Cache cleanup:** `gogoclaw skill remove` prompts to delete cached packages no longer used by any installed skill
   - **TypeScript:** `npm install` in the skill directory, `node_modules` stays local to the skill
   - **SDK distribution:** Python and TypeScript SDKs embedded in the GoGoClaw binary via `//go:embed`. Installed into each skill's environment at install time. SDK version is always in sync with GoGoClaw's broker protocol.

6. **Interpreter management**
   - If interpreter (`python3`/`node`) is not found in PATH, GoGoClaw offers to install a standalone runtime to `~/.gogoclaw/runtimes/`
   - Uses python-build-standalone (prebuilt, self-contained Python) or equivalent Node distributions
   - Shared across all skills of that language
   - Health dashboard shows interpreter status: `Python 3.12.1 ✓` / `Node 20.1.0 ✓` / `Node ✗ not found`
   - **Directory structure:**
     ```
     ~/.gogoclaw/
     ├── runtimes/
     │   ├── python3/           # managed by GoGoClaw
     │   └── node/              # managed by GoGoClaw
     ├── cache/
     │   └── packages/          # shared package cache
     └── skills.d/
         └── data-analysis/
             ├── manifest.yaml
             ├── main.py
             ├── requirements.txt
             └── .venv/         # per-skill, links to cache
     ```

7. **Developer experience**
   - SDK provides transparent `open()` / `io.open()` patching — developers write normal Python, file I/O routes through the broker automatically
   - Explicit `skill.file_read()` API available for developers who prefer precision
   - Test harness: `python -m gogoclaw.test main.py --tool <tool> --args '{...}'` runs the skill outside GoGoClaw with mocked broker calls
   - `--dev` install flag symlinks the skill directory instead of copying, enabling live code iteration
   - stderr captured and logged as debug output; SDK redirects `print()` to stderr
   - Python stack traces surfaced in tool call results (not hidden behind generic errors)

8. **Manifest extension for subprocess skills**
   ```yaml
   name: "data-analysis"
   version: "1.0.0"
   description: "Analyze CSV and tabular data with pandas"
   author: "your-name"
   runtime: "python"                    # "wasm" (default) | "python" | "typescript"
   interpreter: "python3"              # resolved from PATH or ~/.gogoclaw/runtimes/
   entry: "main.py"
   min_version: "3.11"                 # minimum interpreter version
   lifecycle: "one-shot"               # one-shot | warm | long-running
   idle_timeout: "30s"                 # warm mode only

   tools:
     - name: "analyze_csv"
       description: "Analyze a CSV file and return statistical summary"
       parameters:
         type: "object"
         properties:
           file_path:
             type: "string"
             description: "Path to CSV file relative to workspace"
         required: ["file_path"]

   permissions:
     filesystem:
       read:
         - "inbox/*"
         - "documents/*"
       write:
         - "outbox/*"
         - "scratch/*"
     network:
       allowed: false
     env_vars:
       - "OPENAI_API_KEY"
     max_file_size: "10MB"
     max_execution_time: "60s"
   ```

9. **Go implementation architecture**
   - New `SubprocessRuntime` alongside existing `Runtime` (wazero) in `internal/skill/`
   - Both runtimes share the same `CapabilityBroker` instance
   - `SkillDispatcher` checks manifest `runtime` field to route to the correct runtime
   - Linux sandbox setup via `golang.org/x/sys/unix` (seccomp, Landlock) — no CGo
   - Sandbox capabilities applied via `SysProcAttr` on `exec.Cmd` before the skill process starts

10. **Skill install flow**
    ```
    $ gogoclaw skill install ./data-analysis

    Installing skill: data-analysis
      Runtime:     python
      Interpreter: python3 (3.11.4) ✓
      Platform:    linux (seccomp ✓ landlock ✓)

    Creating virtual environment... done
    Installing dependencies from requirements.txt...
      pandas==2.1.0    (142 MB) — cached ✓ (used by: csv-processor)
      numpy==1.25.0    (51 MB)  — downloading...
    Installing GoGoClaw SDK v0.1.0... done
    Running smoke test... ✓

    Skill installed: data-analysis
      Tools: analyze_csv, transform_data, generate_report
      Size: 51 MB new (142 MB shared from cache)
      Sandbox: 🔒 full isolation (seccomp + landlock)
    ```

**Milestone:** Comprehensive design document reviewed and approved, covering all architectural decisions before implementation begins.

---

### Phase 11e: Python/TypeScript Skill Runtime — Implementation

**Goal:** Implement the subprocess skill runtime per the design document from Phase 11d.

**Tasks:**

1. **Subprocess runtime**
   - `internal/skill/subprocess.go`: process spawning, stdin/stdout pipe management, timeout enforcement
   - JSON-RPC message loop: read skill requests from stdout, route to broker, write responses to stdin
   - Error handling: capture stderr, surface Python/Node stack traces in tool call results
   - Integration with `SkillDispatcher`: route to `SubprocessRuntime` when manifest says `runtime: "python"` or `runtime: "typescript"`

2. **Linux sandbox**
   - `internal/skill/sandbox_linux.go`: seccomp BPF filter construction and Landlock ruleset setup
   - Syscall whitelist: `read`, `write`, `exit`, `exit_group`, `brk`, `mmap`, `mprotect`, `munmap`, `rt_sigaction`, `rt_sigprocmask`, `futex`, `clock_gettime`, `getrandom` (Python needs these for basic operation)
   - Syscall blocklist: `socket`, `connect`, `bind`, `sendto`, `recvfrom`, `execve`, `fork`, `clone` (prevent network and process spawning)
   - Landlock: restrict filesystem visibility to skill directory (read-only) + temp scratch dir (read-write)
   - Apply via `SysProcAttr` on `exec.Cmd` — filter is active before skill code runs
   - `internal/skill/sandbox_other.go` (build tag: `!linux`): basic protections only — clean environment, restricted working directory, timeout

3. **Skill installer**
   - `internal/skill/installer.go`: full install flow — validate manifest, create venv, install dependencies, install SDK, smoke test, register
   - Interpreter detection and version checking
   - Shared package cache management (`~/.gogoclaw/cache/packages/`)
   - SDK extraction from `embed.FS`
   - `--dev` mode (symlink instead of copy)
   - Cache cleanup on `gogoclaw skill remove`

4. **Runtime management**
   - Optional standalone interpreter installation to `~/.gogoclaw/runtimes/`
   - Download prebuilt Python/Node distributions
   - Health dashboard integration: interpreter availability and version display

5. **Python SDK**
   - `sdk/python/gogoclaw/`: broker client, tool decorator, `run()` entry point
   - Transparent `open()` / `io.open()` patching for file I/O
   - `requests`-compatible HTTP wrapper routing through `host.http_get`/`host.http_post`
   - `print()` redirect to stderr
   - Test harness: `python -m gogoclaw.test`

6. **TypeScript SDK**
   - `sdk/typescript/gogoclaw/`: same protocol, `process.stdin`/`process.stdout` transport
   - Tool registration and execution loop
   - File and HTTP wrappers

7. **CLI commands**
   - `gogoclaw skill install <path> [--dev]`
   - `gogoclaw skill remove <name>`
   - `gogoclaw skill list`
   - `gogoclaw skill test <name> --tool <tool> --args '{...}'`
   - `gogoclaw skill update <name>` (re-run pip/npm install)

8. **Testing**
   - Unit tests for subprocess runtime message loop
   - Unit tests for seccomp filter construction and Landlock ruleset
   - Integration tests: install a Python skill, execute a tool call, verify broker enforcement
   - Cross-platform CI: verify basic protections on macOS/Windows, full sandbox on Linux
   - Test malicious skill behavior: attempted direct file access, network access, process spawning

**Milestone:** Python and TypeScript skills run alongside WASM skills with the same manifest format, same permission model, and platform-appropriate sandboxing.

---

## Phase 12: Orchestration & Intelligence

**Goal:** Transform GoGoClaw from a single-agent framework into an orchestration-capable platform.

### Phase 12a: Agent-to-Agent Delegation

**Goal:** An agent can spawn sub-agents with scoped tasks and reduced permissions.

**Tasks:**

1. **Sub-agent engine**
   - New core tool: `delegate_task` — creates a sub-agent with a specific task description
   - Sub-agent gets its own engine instance with its own conversation, system prompt, and tool access
   - Parent agent defines: task description, allowed tools, max turns, timeout
   - Sub-agent runs to completion and returns a result to the parent

2. **Permission scoping**
   - Sub-agent inherits a subset of the parent's permissions (never escalates)
   - Parent specifies which tools the sub-agent can use
   - Sub-agent cannot delegate further (single-level delegation for MVP)
   - PII mode and network allowlist apply to sub-agents

3. **Agent profiles for delegation**
   - Predefined sub-agent profiles: "researcher" (web_fetch + memory_search), "coder" (file_read + file_write + shell_exec), "analyst" (file_read + spreadsheet skills)
   - Custom profiles definable in `~/.gogoclaw/agents/`

4. **Conversation management**
   - Sub-agent conversations stored separately but linked to parent conversation
   - Audit trail captures delegation events: who delegated, what task, what result
   - TUI shows delegation in progress: "Agent delegated to 'researcher': Searching for recent AI papers..."

5. **Resource limits**
   - Max concurrent sub-agents (configurable, default: 3)
   - Per-delegation token budget
   - Timeout per delegation

**Milestone:** GoGoClaw agents can break complex tasks into subtasks, delegate to specialized sub-agents, and synthesize results.

---

### Phase 12b: Scheduled Tasks / Cron

**Goal:** Agent executes tasks on a schedule without user interaction.

**Tasks:**

1. **Task scheduler**
   - Cron-like schedule definitions in `~/.gogoclaw/schedules.yaml`
   - Task definition: prompt, agent profile, output destination, notification channel
   - Schedule engine using Go's `time.Ticker` or a cron library

2. **Task types**
   - Prompt-based: "Summarize all new files in inbox and write a report to outbox"
   - Skill-based: run a specific skill tool with fixed arguments
   - Pipeline: chain multiple tasks with output flowing to the next task's input

3. **Output and notification**
   - Task results written to a configurable location (outbox, specific file path)
   - Optional notification via any configured channel (Telegram, Discord, Slack, Signal, email)
   - Task execution history stored in SQLite

4. **Configuration**
   ```yaml
   schedules:
     daily_inbox_summary:
       cron: "0 8 * * *"              # 8 AM daily
       agent: "base"
       prompt: "Summarize all new files in inbox/ and write a report to outbox/daily-summary.md"
       notify:
         channel: "telegram"
         on: "completion"              # completion | error | always
     weekly_cost_report:
       cron: "0 9 * * 1"              # Monday 9 AM
       agent: "analyst"
       prompt: "Generate a weekly cost and usage report from the audit log"
       output: "outbox/weekly-cost-report.md"
   ```

5. **Integration with systemd**
   - Schedules run when GoGoClaw is running as a service (Phase 7's systemd unit)
   - Missed schedule detection: run missed tasks on startup if within a grace period

**Milestone:** GoGoClaw can perform recurring tasks autonomously — daily summaries, periodic file processing, scheduled reports.

---

### Phase 12c: PII Gate v2 (NER-Based Detection)

**Goal:** Replace regex-only PII detection with NLP-based named entity recognition for higher accuracy and fewer false positives.

**Tasks:**

1. **NER model integration**
   - Small, efficient NER model for local inference (e.g., spaCy's `en_core_web_sm` equivalent, or a Go-native NER library)
   - Entity types: PERSON, ORG, GPE (geopolitical entity), PHONE, EMAIL, SSN, CREDIT_CARD, ACCOUNT_NUMBER
   - Run alongside existing regex patterns (NER augments, doesn't replace)

2. **Configurable sensitivity**
   - Per-entity-type sensitivity thresholds
   - Custom entity definitions for domain-specific PII (e.g., medical record numbers, policy numbers)
   - Training data for custom entities (simple CSV format)

3. **Performance optimization**
   - NER runs asynchronously — don't block the LLM request on slow classification
   - Cache classification results for repeated content
   - Configurable: NER-only, regex-only, or combined mode

4. **Improved heuristics**
   - Context-aware detection: "John Smith" in a business email vs. in a financial document
   - Reduced false positives for common words that match name patterns
   - Confidence scoring for each detection

**Milestone:** PII detection catches context-dependent sensitive data that regex misses, with fewer false positives.

---

### Phase 12d: Voice Interface

**Goal:** Speech-to-text and text-to-speech pipeline for voice interaction with GoGoClaw.

**Tasks:**

1. **Speech-to-text (STT)**
   - Whisper integration for local STT (via whisper.cpp Go bindings or subprocess)
   - Cloud STT fallback (OpenAI Whisper API, Google Speech-to-Text)
   - Audio capture from microphone (platform-specific: ALSA on Linux, CoreAudio on macOS, WASAPI on Windows)
   - Voice activity detection (VAD) for hands-free operation

2. **Text-to-speech (TTS)**
   - Local TTS options (Piper, espeak-ng)
   - Cloud TTS fallback (OpenAI TTS, Google TTS)
   - Audio playback to speakers
   - Configurable voice selection

3. **Voice channel**
   - New channel type implementing the Channel interface
   - Audio → STT → engine → TTS → audio pipeline
   - Interrupt handling: user can speak while agent is still responding
   - Push-to-talk and hands-free modes

4. **Integration with existing channels**
   - Telegram voice messages: receive voice note → STT → process → respond (text or voice)
   - Discord voice channels (future)

**Milestone:** Users can talk to GoGoClaw using their microphone and hear responses spoken back, with both local and cloud processing options.

---

## Dependency Map

```
Phase 8a  (Session & hardening) ── prerequisite for all multi-channel work (Phases 9–12)
Phase 8b  (Telegram webhook)   ── depends on 8a (correct session state needed for webhook mode)
Phase 8c  (Anthropic)          ── standalone (can parallelize with 8b after 8a)
Phase 8d  (Tiered routing)     ── depends on 8c (needs multiple providers)

Phase 9a  (TUI settings)       ── standalone (benefits from 8d for routing management UI)
Phase 9b  (Cost tracking)      ── standalone (uses existing audit trail, benefits from 8d for savings display)
Phase 9c  (Skills manager)     ── standalone (uses existing skill registry, prepares for 11d/11e subprocess skills)
Phase 9d  (Bootstrap improve)  ── benefits from 8d (routing mode question), benefits from 9a (settings must reflect bootstrap fields)
Phase 9e  (TUI enhancements)   ── benefits from 9a–9c (command palette needs to know all panels)
Phase 9f  (Audit integrity)    ── standalone (enhances audit log from 8a; audit viewer in 9e can show chain status)

Phase 10a (Web UI)             ── depends on 9a–9e (inherits interaction patterns, settings, skills, and panel architecture)
Phase 10b (Discord)            ── follows Telegram channel pattern, inherits slash commands from 9e
Phase 10c (Slack)              ── follows Telegram channel pattern, inherits slash commands from 9e
Phase 10d (Signal)             ── follows channel pattern, external bridge dependency

Phase 11a (Built-in skills)    ── uses existing WASM runtime, visible in skills manager (9c)
Phase 11b (API tools)          ── extends core tools
Phase 11c (API skills)         ── uses existing WASM runtime, visible in skills manager (9c)
Phase 11d (Py/TS design)       ── standalone (design document), informed by skills manager UX (9c)
Phase 11e (Py/TS impl)         ── depends on 11d, dependencies panel (9c) already designed for subprocess skills

Phase 12a (Delegation)         ── depends on clean engine API (exists)
Phase 12b (Scheduled tasks)    ── depends on engine, benefits from 12a
Phase 12c (PII gate v2)        ── extends existing PII gate, could leverage 11e (Python subprocess for NER model)
Phase 12d (Voice)              ── depends on at least one channel existing
```

---

## Open Questions

1. **Web UI framework choice:** Vanilla JS, htmx, or Preact? Tradeoff between bundle size, development speed, and maintainability. Needs prototyping.

2. **Discord/Slack library selection:** Go ecosystem has multiple options for each. Evaluate: discordgo vs. arikawa (Discord), slack-go vs. slacker (Slack). Selection criteria: maintenance status, API coverage, WebSocket stability.

3. **Signal bridge reliability:** signal-cli and signald are community-maintained. Evaluate long-term viability and maintenance risk before committing.

4. **PII Gate v2 NER runtime:** Go-native NER libraries are limited. Options: embed a small Python NER model (uses the subprocess infrastructure from Phase 11e), use a Go port of a lightweight model, or use a cloud NER API. Tradeoff between accuracy, speed, and offline capability.

5. **Voice interface audio stack:** Cross-platform audio capture/playback is complex. Evaluate: PortAudio (CGo), pure Go options, or shelling out to system audio tools. May need to relax the "no CGo" constraint for this feature, or scope it to Linux-only initially.

6. **Tiered routing classifier:** Rule-based heuristics are a starting point but may need LLM-based classification for accuracy. The meta-question: is it cost-effective to use a cheap model to classify whether you need an expensive model?

7. **Agent delegation depth:** MVP is single-level (agent → sub-agent). Should the design anticipate multi-level delegation (agent → sub-agent → sub-sub-agent), or is that unnecessary complexity?

8. **Bootstrap re-run strategy:** When a user re-bootstraps (9d), should unchanged fields keep their current values (merge strategy), or should the entire profile be regenerated from scratch? Merge is friendlier but more complex to implement.
