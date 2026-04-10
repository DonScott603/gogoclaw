# GoGoClaw Post-Phase 7 Roadmap (v4)

## Context

Phases 1–7 deliver a production-ready, security-first AI agent framework: single Go binary, WASM-sandboxed skills, seven security layers, TUI/Telegram/REST channels, MCP support, vector-backed memory, container deployment, and comprehensive documentation.

This roadmap covers Phases 8–12, expanding GoGoClaw from a single-user agent framework into a multi-channel, multi-runtime, orchestration-capable agent platform.

**Changes from v3:** Phase 8a split into 8a-i (session isolation, persistence, hardening — includes schema migration framework and config migration framework) and 8a-ii (at-rest encryption), allowing 8b to start before encryption lands. Phase 8d split into 8d (Anthropic native provider) and 8d-ii (llama.cpp provider), so tiered routing (8g) can proceed if Anthropic integration is complex. Cloud-only embedding fallback moved from 8f to 8d (lands with provider work, closes gap for Anthropic-only users sooner). Phase 8b gains a note preserving coupled fact extraction until 8f decouples it. Phase 8e milestone reworded to reflect that extraction plugs in via 8f, not 8e. Total sub-phases: 30 (was 28).

**Known footguns carried forward from Phase 7:**

- `shell_exec` blocklist covers `date`, `time`, `set /p`, and `pause` on Windows, but does not cover `choice`, `more`, `cmd /k`, or PowerShell's `Read-Host`. Unix interactive commands (`vi`, `less`, `top`, `read`) are not blocked at all. A configurable execution timeout with process kill is needed (addressed in Phase 8a-i).
- Clean up test skills (`hello-skill`, `hello-world`) in `~/.gogoclaw/skills.d/`.
- REST API has no rate limiting. Valid API keys can fire unlimited requests (addressed in Phase 8a-i).
- Audit log (`gogoclaw.jsonl`) is append-only but has no tamper detection. An attacker with filesystem access can silently modify or truncate it (addressed in Phase 9f).
- Token counting uses `len(content)/4` heuristic everywhere. Context window budgeting is unreliable for models with large context windows (128k+). Real tokenizer integration is needed (addressed in Phase 8d).
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

## Phase 8: Foundation Hardening, Provider Expansion & Memory Architecture

**Goal:** Fix the shared-engine architecture, add operational hardening, refactor the engine for async summarization, introduce conversation lifecycle management, implement a two-tier memory architecture, expand provider support, and introduce intelligent model routing.

### Phase 8a-i: Per-Conversation Session State & Operational Hardening

**Goal:** Eliminate cross-conversation context bleed, build the migration infrastructure for both SQLite schemas and config files, and add missing operational safety mechanisms.

**Why this is first:** The engine currently holds a single `history` slice and a single `convID` as instance fields, shared across TUI, REST, and Telegram. Every audit independently identified this as the most critical architectural issue. Multi-channel features in Phases 9–12 are architecturally unsound without this fix. REST and Telegram should not be exposed to multiple users until this is resolved.

**Tasks:**

1. **Per-conversation session state**
   - Extract `history []provider.Message` and `convID string` from `Engine` into a `Session` struct
   - `SessionManager` keyed by channel + conversation ID, with session creation/lookup/cleanup
   - Each channel request gets its own session with independent history, PII mode, and agent profile
   - Engine becomes stateless: shared provider, dispatcher, assembler config, but no per-conversation state
   - Remove `Engine.SetConversationID` — it is a design smell that all audits flagged
   - Session struct includes lifecycle fields from day one (used by Phase 8e): `lastActivityAt`, `lastBoundaryAt`, `tokensSinceBoundary`

2. **Schema migration framework and conversation persistence wiring**
   - Build `internal/storage/migrations.go`: version table (`schema_version`), sequential migration runner, each migration is a numbered function
   - Migration 1: extract existing `CREATE TABLE` statements from `migrate()` into migration function (existing databases get version-stamped without schema change)
   - Messages written to SQLite per-session via `storage.Store.AddMessage` on every user/assistant/tool message
   - Session restore on reconnect: load history from SQLite when a known conversation ID is received
   - This makes the SQLite store a system of record, not dead code (flagged by all audits)

3. **Config migration framework**
   - Add `config_version` field to `config.yaml` (current format = version 1)
   - Build `internal/config/migrations.go`: detect version mismatch on startup, run sequential migration functions, backup original config before migrating (`config.yaml.bak.v{N}`)
   - Migration 1: no-op (stamps current config as version 1)
   - Same pattern as SQLite migrations — subsequent phases register new migrations as they add config sections
   - Log all migration actions to audit trail

4. **Graceful shutdown with context propagation**
   - Create a root context with `signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)` in `main.go`
   - Propagate to all subsystems: Telegram long-polling, REST HTTP server, health monitor, MCP clients
   - On signal: flush pending messages, close channels, stop health monitor, close MCP clients, close storage
   - Replace `context.Background()` in channel message handlers and memory search with the propagated context

5. **`shell_exec` configurable timeout**
   - Add `shell.timeout` field to agent config (default: 30s)
   - Wrap `exec.CommandContext` with a context derived from the configured timeout
   - On timeout, kill the process and return an error: "Command timed out after 30s and was terminated"
   - This replaces the incomplete blocklist approach as the primary safety mechanism — the blocklist remains as a fast-path UX improvement

6. **REST API rate limiting**
   - Add a simple token bucket rate limiter to the REST auth middleware
   - Configurable in channel config: `rate_limit` (requests per minute, default: 60)
   - Return 429 Too Many Requests when exceeded
   - Rate limit is per API key (relevant when multiple keys are supported in the future)

7. **Testing**
   - Unit tests for `SessionManager`: create, lookup, cleanup, concurrent access
   - Unit tests for schema migration runner: version detection, sequential execution, rollback on failure
   - Unit tests for config migration runner: version detection, backup creation, sequential execution
   - Integration test: simultaneous TUI and REST conversations maintain separate histories
   - Integration test: graceful shutdown flushes all channels
   - Test: `shell_exec` timeout kills process and returns error
   - Test: REST rate limiter returns 429

**Milestone:** Each conversation has isolated state. SQLite is the system of record with a versioned migration framework. Config files are version-stamped with automatic migration support. Graceful shutdown works. Shell commands time out. REST API is rate-limited. The engine is ready for the async summarization refactor. At-rest encryption (8a-ii) can proceed in parallel with 8b.

---

### Phase 8a-ii: At-Rest Encryption

**Goal:** Encrypt persistent data before it accumulates in the now-live SQLite store.

**Why split from 8a-i:** Encryption involves key management complexity (key derivation, storage, rotation) that shouldn't block the critical path. Session isolation and persistence wiring are the architectural prerequisites for everything downstream. Encryption is important but can land in parallel with 8b.

**Tasks:**

1. **SQLite database encryption**
   - Application-level encryption using a key derived from a user-configured secret or auto-generated key stored in `~/.gogoclaw/env`
   - Schema migration (migration 2): adds encryption metadata columns, re-encrypts existing unencrypted messages
   - Config migration: adds `storage.conversations.encrypt` with default `false` for existing users, `true` for new installs

2. **Audit log encryption**
   - Optional per-entry encryption for the JSONL audit trail
   - Configurable via `logging.audit.encrypt` (implements the reserved config flag)

3. **Key management**
   - Key derivation from user-configured secret (passphrase → key via Argon2 or similar)
   - Auto-generated key for users who don't configure a secret
   - Key rotation support: re-encrypt with new key without data loss
   - `gogoclaw config rotate-key` CLI command

4. **Testing**
   - Test: encrypted SQLite round-trip — write messages, close, reopen with key, verify data
   - Test: key rotation — encrypt with key A, rotate to key B, verify all data readable
   - Test: encrypted audit log entries — write, read back, verify
   - Test: migration from unencrypted to encrypted database

**Milestone:** Conversation data and audit logs are encrypted at rest. Existing unencrypted data is migrated transparently.

---

### Phase 8b: Async Engine Summarization Refactor

**Goal:** Move summarization from blocking pre-response to non-blocking post-response, eliminating user-perceived latency from the summarization path.

**Why now:** The current engine calls `maybeSummarize()` synchronously at the top of `Send()` and `SendStream()`, before the provider sees the user's message. When summarization triggers, the user waits 1–3 seconds (or more for two-pass in enhanced mode) with no feedback. Moving this to async is prerequisite for the two-pass summarization improvements in the enhanced memory tier (8h) and cleanly separates the mid-conversation summarizer from the boundary summarizer (8e).

**Note on coupled fact extraction:** The current `MaybeSummarize` calls `ExtractFacts` as a side effect. This behavior is preserved in the async path — fact extraction now runs asynchronously inside the background summarizer. This is intentional. Decoupling extraction from summarization happens in 8f when the boundary handler takes over extraction responsibility. The implementation prompt should preserve the existing `ExtractFacts` call inside the async summarization goroutine.

**Tasks:**

1. **Async summarization infrastructure on Session**
   - `summarizing atomic.Bool` — prevents concurrent summarizations on the same session
   - `pendingSummary chan *pendingSummaryResult` — buffered channel (capacity 1) for completed results
   - `pendingSummaryResult` struct: `result *memory.SummarizeResult` + `snapshotLen int` (history length at snapshot time)

2. **Revised engine flow**
   - `applyPendingSummary()`: non-blocking channel check at the top of `Send()` and `SendStream()`. If a pending result exists, reconcile it with current history using `snapshotLen` to identify messages added since the snapshot
   - `maybeStartSummarization()`: called at the end of `runToolLoop()` after the final assistant message is appended, and at the end of the `SendStream` goroutine after stream completion. Checks token threshold, launches background goroutine if over threshold and no summarization in-flight
   - Summarization only triggers after assistant responses (not after user messages or intermediate tool call rounds) to ensure history is in a consistent state

3. **History reconciliation**
   - When applying a pending summary, preserve all messages added after `snapshotLen`
   - Reconciled history: `[summary system message]` + `[kept messages from snapshot]` + `[messages added since snapshot]`
   - If the user sent multiple messages while summarization was in-flight, all are preserved

4. **Improved summarization content**
   - Include condensed tool call representations instead of skipping tool messages: "Used file_read on /path/to/config.yaml, ran shell_exec: go test ./…" — tool name and key argument, not full result payload
   - Entity overlap quality check (Go-level string matching): extract proper nouns and technical terms from the original segment, verify they appear in the summary. Log a warning if key terms are missing (no LLM call, sub-millisecond)

5. **Boundary summarization coordination**
   - Define `BoundarySummarizer` interface alongside existing `Summarizer` interface — distinct prompt, distinct storage behavior (produces both compacted history and a retrievable vector document)
   - Boundary summarizer checks `summarizing` flag before running; waits for in-flight mid-conversation summarization to complete or cancels it
   - `BoundarySummarizer` uses a structured handoff prompt: what the user was working on, decisions made, open questions, commitments made

6. **Testing**
   - Unit test: `applyPendingSummary` with no pending result is a no-op
   - Unit test: `applyPendingSummary` correctly reconciles history when messages were added during summarization
   - Unit test: `maybeStartSummarization` does not launch when `summarizing` flag is true
   - Unit test: summarization only fires after assistant responses, not intermediate tool call rounds
   - Integration test: rapid-fire messages during background summarization — all messages preserved

**Milestone:** Users never wait for summarization. Mid-conversation compaction happens invisibly in the background. Boundary summarizer interface is defined for Phase 8e. Fact extraction continues to run (now asynchronously) inside the summarizer until 8f decouples it.

---

### Phase 8c: Telegram Webhooks

**Goal:** Replace Telegram long-polling with webhook mode for lower latency and better resource efficiency.

**Tasks:**

1. **Webhook server**
   - HTTPS endpoint for Telegram to push updates
   - Configurable webhook URL and port in `channels/telegram.yaml`
   - Self-signed certificate support for development; user-provided certs for production
   - Fallback to long-polling if webhook setup fails

2. **Webhook registration**
   - Automatic `setWebhook` call on startup with configured URL
   - `deleteWebhook` on graceful shutdown
   - Health check: verify webhook is active via `getWebhookInfo`

3. **Session state integration**
   - Webhook handlers create/lookup sessions via `SessionManager` (from 8a-i)
   - Same conversation mapping as long-polling: Telegram chat ID → GoGoClaw conversation

**Milestone:** Telegram channel supports both long-polling (default) and webhook modes.

---

### Phase 8d: Anthropic Native Provider & Cloud Embedding Fallback

**Goal:** Add native Anthropic provider for features unavailable through OpenAI-compatible shims, and ensure cloud-only users have a working embedding endpoint.

**Tasks:**

1. **Anthropic native client (`internal/provider/anthropic.go`)**
   - Direct Messages API integration (not OpenAI-compat shim)
   - Tool use with Anthropic's native schema
   - Extended thinking support
   - Vision support: image content blocks in messages
   - Read images from workspace via `file_read`, encode as base64 for the API
   - Display image references in TUI chat

2. **Anthropic provider router integration**
   - Register as a first-class provider type (alongside `openai_compat` and `ollama`)
   - Provider config template: `~/.gogoclaw/providers/anthropic.yaml`
   - Failover compatibility with other providers (graceful degradation when Anthropic-specific features aren't available on fallback)

3. **Anthropic token counting**
   - Anthropic tokenizer integration (or estimation based on documented rates)
   - Context window management for Claude models (200K context)

4. **Cloud-only embedding support**
   - Standard tier must work without Ollama — cloud-only users need an embedding endpoint
   - Implement OpenAI-compatible embedding fallback in the chromem-go integration: if no Ollama is detected and no dedicated embedding provider is configured (8h), use the conversation provider's `/v1/embeddings` endpoint
   - Supported providers: OpenAI (`text-embedding-3-small`), Anthropic (via Voyage AI proxy), any OpenAI-compatible endpoint
   - Health dashboard shows embedding source: "Ollama (nomic-embed-text)" / "OpenAI (text-embedding-3-small)" / "not configured"
   - Config migration: adds embedding fallback config section with sensible defaults

5. **Testing**
   - Unit tests for Anthropic message format conversion, tool schema mapping, extended thinking parsing
   - Unit tests for existing providers (`openai_compat.go`, `ollama.go`): SSE parsing, request building, error handling, streaming edge cases — the provider package currently has zero test files (flagged by audits)
   - Test: cloud-only embedding fallback — verify memory system initializes and stores/retrieves with OpenAI embedding endpoint when Ollama is absent
   - Integration test: Anthropic tool use round-trip
   - Test: failover from Anthropic to OpenAI-compat on error

**Milestone:** GoGoClaw can use Claude models natively with full tool use, extended thinking, and vision. Cloud-only users have working memory without Ollama.

---

### Phase 8d-ii: llama.cpp Provider

**Goal:** Add llama.cpp as a first-class local inference provider. Split from 8d so tiered routing (8g) can proceed even if Anthropic integration is complex.

**Tasks:**

1. **llama.cpp provider (`internal/provider/llamacpp.go`)**
   - Thin wrapper around `openai_compat` (llama-server exposes OpenAI-compatible `/v1/chat/completions`)
   - llama.cpp-specific health check via `/health` endpoint
   - Model metadata retrieval from `/props` endpoint (context length, model name) with fallback to config values
   - Provider config template: `~/.gogoclaw/providers/llamacpp.yaml`
   - Documentation: model management is user-managed (GGUF files, llama-server startup), not GoGoClaw-managed

2. **llama.cpp embedding support**
   - `/v1/embeddings` endpoint support for dedicated local embedding models (feeds into 8h enhanced memory)
   - Note: llama.cpp typically serves one model at a time — document the multi-instance pattern for running separate conversation and embedding models

3. **Testing**
   - Unit tests for llama.cpp health check parsing, metadata retrieval
   - Integration test: llama.cpp provider via local server (or mock)

**Milestone:** llama.cpp is available as a lightweight local inference option alongside Ollama. Tiered routing (8g) has at least two provider types to work with regardless of Anthropic progress.

---

### Phase 8e: Conversation Lifecycle Management

**Goal:** Implement a three-layer boundary model that detects natural conversation checkpoints and fires boundary hooks, preparing for memory extraction in 8f.

**Design principles:**
- Soft boundaries insert checkpoint markers into existing conversations — they do not create new conversation IDs
- The user never sees "your conversation has been closed" unexpectedly
- Memory extraction fires at every boundary type (explicit, soft, daily) — once 8f implements the extraction pipeline
- Extraction and summarization are Go-level pipelines, not agent-level metacognitive prompts to the conversation model

**Tasks:**

1. **Layer 1: Explicit close**
   - TUI: new conversation (Ctrl+N), switch conversation, quit (Esc)
   - Telegram: `/new` command
   - REST API: `POST /conversations/{id}/close` endpoint
   - Web UI (future 10b): new conversation button, navigation away with `beforeunload` hook
   - All explicit closes trigger the full boundary pipeline

2. **Layer 2: Soft boundary detection**
   - Background goroutine on `SessionManager`, scanning active sessions on a configurable ticker (default: every 60 seconds)
   - Idle timeout: fires when `time.Since(session.lastActivityAt)` exceeds configured threshold per channel
   - Token budget threshold: fires when `session.tokensSinceBoundary` exceeds 80% of the model's context window
   - Topic drift detection is deferred to enhanced tier (8h) — not included in standard lifecycle
   - Soft boundary does not create a new conversation — it checkpoints the existing one

3. **Layer 3: Daily reset (safety net)**
   - Once per day (configurable time, default midnight local), scan all sessions active in the last 24 hours without an explicit close
   - Run the boundary pipeline on each
   - Guarantees no conversation goes more than 24 hours without memory extraction (once 8f enables it)

4. **Boundary pipeline (common to all three layers)**
   - Step 1: Prompt the agent to update Tier 1 working memory via tool call (agent-level, may fail gracefully — requires 8f)
   - Step 2: Run fact extraction on the segment since last boundary (heuristic in standard — requires 8f)
   - Step 3: Write-time dedup check for each extracted fact (requires 8f)
   - Step 4: Store surviving facts to Tier 2 vector store with category and importance metadata (requires 8f)
   - Step 5: Append to daily memory file
   - Step 6: Run `BoundarySummarizer` on the segment — store output as both compacted history and retrievable session summary vector document
   - Steps 1 and 2–6 can run concurrently (independent concerns)
   - **Until 8f lands, the pipeline fires but steps 1–5 are no-ops.** Step 6 (boundary summarization) can operate independently using the `BoundarySummarizer` interface defined in 8b.

5. **Channel-specific lifecycle profiles**
   ```yaml
   channels:
     tui:
       lifecycle: explicit_only  # Layer 1 + Layer 3 (daily safety net)
     telegram:
       lifecycle: auto_boundary  # All three layers
       idle_timeout: 4h
     rest:
       lifecycle: auto_boundary
       idle_timeout: 1h
     web:
       lifecycle: auto_boundary
       idle_timeout: 2h
   ```

6. **`OnConversationBoundary` hook interface**
   - `OnConversationBoundary(ctx context.Context, session *Session, boundaryType BoundaryType) error`
   - Memory system implements this interface (8f)
   - Boundary types: `BoundaryExplicit`, `BoundarySoft`, `BoundaryDaily`

7. **Coordination with async summarization (8b)**
   - Boundary handler checks `session.summarizing` flag
   - If mid-conversation summarization is in-flight: wait for completion (with timeout), then apply the result before running boundary summarization
   - Prevents conflicting concurrent operations on the same session's history

8. **Testing**
   - Unit test: idle timeout fires after configured duration
   - Unit test: token budget threshold fires at 80% of context window
   - Unit test: daily reset catches sessions that were never explicitly closed
   - Unit test: soft boundary does not change conversation ID
   - Unit test: boundary handler waits for in-flight async summarization
   - Integration test: Telegram conversation with 5-hour idle gap — verify boundary fired
   - Integration test: explicit close followed by new message — verify clean session state

**Milestone:** Conversation boundary detection and the boundary pipeline skeleton are operational. The `OnConversationBoundary` hook fires at all three layers. Boundary summarization (step 6) runs via the `BoundarySummarizer` from 8b. Memory extraction (steps 1–5) plugs in when 8f lands. No user-visible disruption.

---

### Phase 8f: Memory Architecture v2 — Standard

**Goal:** Implement a two-tier memory architecture that works with no new model infrastructure beyond what's already configured.

**Design:** Tier 1 is a small, bounded, agent-curated working memory file always present in the system prompt. Tier 2 is the existing vector store with improved retrieval (hybrid vector + FTS5) and improved extraction (heuristic v2 reading both sides of the conversation). Standard tier requires zero additional models or API calls beyond the existing conversation provider.

**Tasks:**

1. **Tier 1: Agent-curated working memory**
   - Bounded file at `~/.gogoclaw/memory/MEMORY.md`
   - Loaded into the system prompt at session start as a frozen snapshot (à la Hermes Agent)
   - Character limit: configurable, default ~2200 chars (~800 tokens)
   - New agent tools: `memory_working_add`, `memory_working_replace`, `memory_working_remove`, `memory_working_list`
   - When near capacity, the agent consolidates or replaces entries to make room
   - Boundary handler (8e) prompts the agent to update working memory before extraction runs
   - Atomic file writes (write to temp file, rename) to prevent corruption
   - Secret scrubbing applied before writing (reuses existing `SecretScrubber`)

2. **Tier 2: Hybrid retrieval**
   - FTS5 virtual table on SQLite messages table — schema migration adds the virtual table and populates from existing messages
   - Hybrid retrieval in `ContextAssembler`: query both chromem-go (vector similarity) and FTS5 (keyword matching) in parallel
   - Score fusion via Reciprocal Rank Fusion (RRF): normalize ranks from both sources, combine with configurable weighting (default: 0.6 vector, 0.4 keyword)
   - Replaces pure vector search — significantly improves recall for project names, error codes, version numbers, and other keyword-heavy queries

3. **`FactExtractor` interface and heuristic v2**
   ```go
   type ExtractedFact struct {
       Content    string   // normalized third-person statement
       Category   string   // identity, preference, technical, decision, relationship, ephemeral
       Confidence string   // high, medium
       Tags       []string // auto-generated from content
   }

   type FactExtractor interface {
       Extract(ctx context.Context, messages []provider.Message) ([]ExtractedFact, error)
   }
   ```
   - `HeuristicFactExtractor` implementing `FactExtractor`
   - Reads both user and assistant messages (current version only reads assistant)
   - User-message + assistant-acknowledgment pairs: "high" confidence
   - Standalone user declarative statements ("I work at…", "we use…", "my team prefers…"): "medium" confidence
   - Category assignment via pattern matching: identity patterns ("I am", "I work at", "my name") → `identity`; preference patterns ("I prefer", "I like") → `preference`; technical patterns ("we use", "our stack", "deployed on") → `technical`; decision patterns ("let's go with", "I've decided") → `decision`; default → `ephemeral`
   - Negative patterns to skip: "would you like", "for example", "in general", "let me know", code blocks, markdown formatting
   - Existing `ExtractFacts` function retained as `legacyExtractFacts` for backward compatibility in tests

4. **Importance-tiered decay**
   - `MemoryDocument` gains a `Category` field
   - Decay function uses different half-lives per category:
     - `identity`: 90 days (name, employer, role — changes rarely)
     - `preference`: 30 days (tool choices, style preferences)
     - `technical`: 14 days (stack details, version numbers — changes with projects)
     - `decision`: 14 days (architectural decisions — relevant to active work)
     - `relationship`: 30 days (team members, collaborators)
     - `ephemeral`: 3 days (one-off questions, temporary context)
   - Replaces the current flat 7-day exponential decay in `computeRecency`

5. **Write-time deduplication**
   - Before storing a new fact, search the vector store for high-similarity existing documents (threshold: 0.92)
   - If a near-duplicate is found: skip the new fact, update the existing document's timestamp (keeps it fresh)
   - If a close match with meaningful differences is found (similarity 0.85–0.92): store as a new document (captures the evolution)
   - Below 0.85: store as new

6. **Extraction decoupled from summarizer**
   - The `Summarizer.MaybeSummarize` method no longer calls `ExtractFacts` as a side effect
   - Extraction is triggered by the boundary handler (8e) via the `OnConversationBoundary` hook
   - The boundary handler calls the `FactExtractor` on the segment since the last boundary, then passes results to the Writer for Tier 2 storage and daily file append

7. **Memory config**
   ```yaml
   memory:
     enabled: true
     tier: "standard"  # "standard" or "enhanced"
     working_memory:
       enabled: true
       char_limit: 2200
       path: "~/.gogoclaw/memory/MEMORY.md"
     storage:
       path: "~/.gogoclaw/data/vectors"
     retrieval:
       relevance_threshold: 0.3
       recency_weight: 0.2
       hybrid_search: true  # enable FTS5 + vector fusion
       vector_weight: 0.6   # RRF weighting
       keyword_weight: 0.4
     extraction:
       extractor: "heuristic"  # "heuristic" or "llm" (llm requires enhanced tier)
       dedup_threshold: 0.92
     decay:
       model: "exponential"  # "exponential" or "weibull" (weibull requires enhanced tier)
       # Per-category half-lives (days)
       identity_halflife: 90
       preference_halflife: 30
       technical_halflife: 14
       decision_halflife: 14
       relationship_halflife: 30
       ephemeral_halflife: 3
   ```

8. **Testing**
   - Unit tests for `HeuristicFactExtractor`: user+assistant pair extraction, standalone user declaratives, negative pattern filtering, category assignment
   - Update `TestExtractFactsNoAssistant` — now expects facts from user declarative statements (deliberate behavioral change)
   - Unit tests for hybrid retrieval: verify RRF fusion produces better results than pure vector for keyword-heavy queries
   - Unit tests for importance-tiered decay: verify different half-lives per category
   - Unit tests for write-time dedup: near-duplicate skip, close-match store, novel store
   - Integration test: boundary fires → extraction → dedup → vector store + daily file
   - Integration test: Tier 1 working memory tools — add, replace, remove, character limit enforcement

**Milestone:** Two-tier memory architecture is operational with zero new model infrastructure. Working memory provides always-in-context high-signal facts. Hybrid retrieval handles both semantic and keyword queries. Facts are extracted from both sides of the conversation with structured categories and importance-aware decay. The boundary pipeline (8e) is now fully functional with extraction plugged in.

---

### Phase 8g: Tiered Model Routing Policy

**Goal:** Automatically route messages to the most cost-effective model that can handle the task.

**Tasks:**

1. **Routing policy engine**
   - Policy definition in `~/.gogoclaw/routing.yaml`
   - Rule-based routing: message length, tool call presence, conversation history depth
   - Tiered model definitions: "fast" (gpt-4o-mini / local 9B), "standard" (gpt-4o), "reasoning" (Claude Sonnet)

2. **Classification heuristics**
   - Simple questions (short input, no tools needed) → fast tier
   - Tool-using tasks (file operations, web fetch) → standard tier
   - Complex reasoning (long context, multi-step analysis) → reasoning tier
   - User override: `/model <tier>` command to force a tier for current conversation

3. **Internal routing for non-conversation tasks**
   - Memory extraction, summarization, and fact extraction are routeable tasks (used by 8h)
   - `PolicyRouter.RouteTask(taskType string)` returns the appropriate provider for internal tasks
   - Task types: `"extraction"`, `"summarization"`, `"reranking"`, `"embedding"`
   - Default: all internal tasks use the conversation provider (backward compatible)

4. **Routing policy config**
   ```yaml
   routing:
     enabled: true
     default_tier: "standard"
     tiers:
       fast:
         provider: "ollama"
         model: "qwen3.5:9b"
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
     internal_tasks:
       extraction: "fast"       # route fact extraction to fast tier
       summarization: "standard"
       reranking: null           # use dedicated reranker config (8h)
       embedding: null           # use dedicated embedding config (8h)
   ```
   Note: Model identifiers in the example above are illustrative. Users should use current model identifiers from their provider's documentation.

5. **Audit integration**
   - Log routing decisions in audit trail: which tier was selected, why, what the alternatives were
   - Cost tracking integration: show savings from routing vs. always using the reasoning tier

**Milestone:** GoGoClaw intelligently routes to cheaper models for simple tasks and more capable models for complex ones, reducing costs without sacrificing quality. Internal tasks like extraction and summarization can be routed independently.

---

### Phase 8h: Memory Architecture v2 — Enhanced

**Goal:** Upgrade the memory system with LLM-based extraction, dedicated local models for embedding and reranking, and advanced retrieval and decay algorithms. Builds on the standard tier (8f) and requires the routing policy (8g) for model routing.

**Tasks:**

1. **LLM-based fact extractor**
   - `LLMFactExtractor` implementing `FactExtractor`
   - Structured JSON extraction prompt with "facts already known" dedup context (top 10 existing memories by relevance to the conversation segment, preventing re-extraction of known facts)
   - Returns `[]ExtractedFact` with content, category, confidence, and tags
   - Routed through `PolicyRouter.RouteTask("extraction")` — typically the fast tier (local 9B)
   - Falls back to `HeuristicFactExtractor` on LLM failure (timeout, parse error, provider down)
   - Extraction prompt:
     ```
     You are extracting durable facts from a conversation segment.

     Facts already known about this user:
     {{top 10 existing memories by relevance}}

     Extract NEW facts not already captured above. Return ONLY a JSON
     array of objects:
     - "content": normalized third-person ("The user prefers...", "The project uses...")
     - "category": one of "identity", "preference", "technical", "decision", "relationship"
     - "confidence": "high" or "medium"
     - "tags": relevant keywords for retrieval

     Rules:
     - Read BOTH user and assistant messages
     - Skip facts already covered above
     - Skip greetings, pleasantries, procedural exchanges
     - Skip facts only relevant to the immediate task
     - Prefer specific over vague
     - If no new durable facts exist, return []
     ```

2. **Dedicated embedding provider**
   - `memory.embedding` config section: `provider`, `model`, `base_url`
   - Supports Ollama (nomic-embed-text, BGE, GTE variants), llama.cpp (`/v1/embeddings`), or OpenAI API
   - Decouples embedding availability from the conversation provider — memory works even when the API is rate-limited
   - Falls back to the conversation provider's embedding endpoint if dedicated config is absent (uses the cloud fallback from 8d)
   - Config example:
     ```yaml
     memory:
       embedding:
         provider: "ollama"
         model: "nomic-embed-text"
         # or for llama.cpp:
         # provider: "llamacpp"
         # base_url: "http://localhost:8081"
     ```

3. **Cross-encoder reranker**
   - Sits between retrieval candidates and final results
   - Retrieves 40 candidates from hybrid search (vector + FTS5)
   - Reranks via cross-encoder model: Jina reranker API, or local model via Ollama/llama.cpp
   - `memory.reranker` config section with its own provider entry
   - Falls back to no reranking (standard tier behavior) if config is absent or model is unavailable
   - Config example:
     ```yaml
     memory:
       reranker:
         enabled: true
         provider: "jina"
         model: "jina-reranker-v3"
         candidate_count: 40
     ```

4. **MMR diversity filtering**
   - Maximal Marginal Relevance applied after reranking
   - Prevents near-duplicate memories from crowding out breadth in retrieval results
   - Configurable lambda parameter (default: 0.7 — balance relevance vs. diversity)

5. **Two-pass boundary summarization**
   - Map-reduce pattern for long segments (more than a configurable message count, default: 30)
   - Pass 1: extract structured topic outline from the segment (topics discussed, keyed by message ranges)
   - Pass 2: summarize each topic independently, combine into final checkpoint summary
   - Only applies to `BoundarySummarizer` — mid-conversation summarizer stays single-pass (token cost concern even though latency is no longer an issue due to async)
   - Routeable to a different model tier than the conversation via `PolicyRouter.RouteTask("summarization")`
   - Short segments (under 30 messages) use single-pass even in enhanced mode

6. **Weibull decay**
   - Replaces exponential decay when `memory.decay.model: "weibull"` is configured
   - Configurable shape parameter per importance category
   - Shape < 1: memories fade quickly at first, then persist (useful for ephemeral facts)
   - Shape = 1: equivalent to exponential decay
   - Shape > 1: memories persist for a while, then fade sharply (useful for decisions with expiry)
   - Default shape parameters: identity=1.5, preference=1.2, technical=1.0, decision=1.3, ephemeral=0.7

7. **Enhanced memory config**
   ```yaml
   memory:
     tier: "enhanced"
     extraction:
       extractor: "llm"
       dedup_threshold: 0.92
       known_facts_context: 10  # number of existing memories to include in extraction prompt
     embedding:
       provider: "ollama"
       model: "nomic-embed-text"
     reranker:
       enabled: true
       provider: "jina"
       model: "jina-reranker-v3"
       candidate_count: 40
     retrieval:
       mmr_enabled: true
       mmr_lambda: 0.7
     decay:
       model: "weibull"
       identity_shape: 1.5
       preference_shape: 1.2
       technical_shape: 1.0
       decision_shape: 1.3
       ephemeral_shape: 0.7
   ```

8. **Testing**
   - Unit tests for `LLMFactExtractor`: valid JSON parsing, fallback to heuristic on malformed response, dedup context injection
   - Unit tests for cross-encoder reranking: reordering of candidates, fallback to no reranking
   - Unit tests for MMR: diversity filtering produces non-redundant results
   - Unit tests for Weibull decay: verify different shape parameters produce expected decay curves
   - Unit tests for two-pass summarization: topic extraction, per-topic summary, combination
   - Integration test: end-to-end enhanced extraction → reranking → retrieval with dedicated local embedding model
   - Benchmark: retrieval quality comparison between standard (hybrid, no reranker) and enhanced (hybrid + reranker + MMR)

**Milestone:** Memory system operates with dedicated local models for embedding and reranking, LLM-based fact extraction, and advanced decay modeling. Retrieval quality is significantly improved for long-running agent deployments with large memory stores.

---

## Phase 9: UX & TUI Polish

**Goal:** Refine the user experience across bootstrap, settings management, and daily TUI usage. These improvements define the interaction patterns that the Web UI (Phase 10b) will replicate.

### Phase 9a: TUI Settings Panel with Restart

**Goal:** In-app configuration editing with live reload or controlled restart, covering all settings initially configured during bootstrap.

**Tasks:**

1. **Charmbracelet v2 migration (prerequisite)**
   - Migrate from bubbletea v1 to v2 before building new panels — v2 has breaking API changes and migrating after building seven panels on v1 is double work
   - Update all existing TUI code (app.go, chat.go, conversations.go, health.go, confirm.go) to v2 APIs
   - Verify existing functionality (chat, streaming, tool call visualization, health dashboard) works on v2
   - Update lipgloss and bubbles to compatible versions

2. **Settings panel UI**
   - New TUI panel (keybinding: F3 or similar) with categorized settings
   - Categories: Identity & Preferences, Providers & Routing, Security, Memory, Channels
   - Form-style editing with validation feedback
   - Read current values from loaded config, write back to YAML and markdown files
   - Visual indicator for unsaved changes

3. **Provider management**
   - Add new providers: guided form matching bootstrap's provider setup flow
   - Edit existing providers: change model, base URL, API key env var, test connectivity
   - Remove providers: confirmation dialog, check if provider is referenced in routing tiers
   - Reorder provider failover chain via drag-style up/down controls
   - Test provider connectivity from the settings panel (send a ping message, show latency)

4. **OS keyring integration for secrets**
   - Replace env-var-only secret management with platform-native keyring support
   - macOS: Keychain via `keychain` package, Linux: Secret Service (D-Bus) via `go-keyring`, Windows: Credential Manager via `go-keyring`
   - Settings panel offers "Store in system keychain" when adding/editing provider API keys
   - Fallback to env vars when keyring is unavailable (headless Linux, containers)
   - Secret resolution order: keyring → env var → config file `${VAR}` syntax
   - Existing env var-based secrets continue to work — keyring is opt-in, not forced

5. **Routing mode management**
   - Switch between simple fallback and tiered routing
   - When switching to tiered: prompt user to assign providers to tiers (fast/standard/reasoning)
   - Edit tier assignments: move providers between tiers
   - Preview routing behavior: "Based on current config, a simple greeting would go to [GPT-4o-mini] and a code generation request would go to [Claude Sonnet]"
   - Disable/enable routing without losing tier configuration

6. **Identity & preference editing**
   - Edit all fields from `user.md` and `identity.yaml`: name, agent name, personality, work domain, expertise level
   - Edit interaction preferences: verbosity, proactiveness, uncertainty handling, reasoning visibility
   - Edit tool behavior preferences: shell confirmation mode, file write confirmation mode
   - Changes regenerate the relevant sections of `user.md` and update `identity.yaml`

7. **Agent profile inheritance**
   - Implement runtime profile merging in `internal/agent/profile.go`: when a profile has a non-empty `Inherits` field, load the parent profile and deep-merge child overrides on top
   - Settings panel displays the inheritance chain and which fields are overridden vs. inherited
   - Profile switching: select active profile from the settings panel or via `/agent switch` command
   - Required for specialized profiles (e.g., `financial.yaml` inheriting from `base.yaml`)

8. **Channel management**
   - Enable/disable channels (Telegram, REST, and future channels)
   - Edit channel-specific settings (ports, tokens, env vars)
   - Show channel connection status

9. **Restart mechanism**
   - Detect which config changes require restart vs. live reload
   - Live reload (no restart): PII mode, memory thresholds, log levels, `user.md` content
   - Restart required: provider add/remove, channel enable/disable, routing mode change
   - Confirmation dialog: "These changes require a restart. Restart now? [Y/n]"
   - Graceful restart: flush pending messages, close channels, re-initialize engine

10. **Conflict detection**
    - fsnotify integration to detect external config file changes while settings panel is open
    - Warning: "Config file modified externally. Reload?"

**Milestone:** TUI is on Charmbracelet v2. Users can view and edit all GoGoClaw settings — including secrets via OS keyring, agent profiles with inheritance, and routing configuration — from within the TUI, with live reload for safe changes and controlled restart for structural changes.

---

### Phase 9b: Cost & Token Tracking

**Goal:** Track and display LLM costs across all providers and routing tiers.

**Tasks:**

1. **Token usage tracking**
   - Record input/output token counts per request in the audit trail (already partially present)
   - Aggregate by: conversation, provider, model, routing tier, time period
   - Store running totals in SQLite for efficient querying

2. **Cost calculation**
   - Configurable cost-per-token rates in provider config
   - Calculate cost per request, per conversation, per day/week/month
   - Show cost savings from tiered routing (actual cost vs. "if everything used reasoning tier")

3. **Health & Costs panel (F2)**
   - Extend the existing health panel with cost information
   - Show: today's cost, this week's cost, cost by provider, cost by routing tier
   - Show: token usage trends, average tokens per conversation

**Milestone:** Users have full visibility into what their agent usage costs across all providers and can see the savings from intelligent routing.

---

### Phase 9c: TUI Skills Manager

**Goal:** In-TUI skill management with full visibility into installed skills, permissions, dependencies, and disk usage.

**Tasks:**

1. **Skills panel (F4)**
   - Scrollable list of installed skills with status, runtime type (WASM / Python / TypeScript / MCP), sandbox level
   - Permission display per skill: file access paths, network domains, env vars, tool access
   - Skill actions: disable/enable, uninstall (with confirmation), install from path

2. **Skill provenance verification**
   - Implement cryptographic signature verification in `internal/security/signing.go` (currently hash-only)
   - Skills panel displays provenance status per skill: "✓ signed (author-key-fingerprint)" / "⚠ hash-only" / "✗ unsigned"
   - Ed25519 key-based signing: skill authors sign with their private key, GoGoClaw verifies with the public key
   - Public key trust store at `~/.gogoclaw/trusted-keys/` — user explicitly adds trusted author keys
   - Installation of signed skills from untrusted keys prompts: "This skill is signed by an unknown author. Trust this key? [y/N]"
   - Unsigned subprocess skills (Python/TS) get an additional warning given their weaker sandbox on non-Linux platforms

3. **MCP servers section**
   - List of configured MCP servers with connection status
   - Available tools per server
   - Health check: test connectivity from the panel
   - Add/remove/edit MCP server configurations

4. **Dependencies sub-tab**
   - Interpreters: Python and Node availability, version, location
   - Package cache: total size, package list with per-skill usage
   - Per-skill environments: venv size, dependency count, installed packages
   - Actions: rebuild venv, clear unused cache packages

5. **Disk usage summary**
   - Total disk usage across all skills, venvs, and cache
   - Breakdown by component

**Milestone:** Users have full visibility into what skills are installed, their provenance and signing status, what permissions they have, and how much disk they consume — all manageable from within the TUI.

---

### Phase 9d: Bootstrap Improvements

**Goal:** Expand the bootstrap ritual to capture richer user preferences and produce a detailed `user.md` profile.

**Tasks:**

1. **Atomic question structure**
   - Split all compound questions into discrete steps (one answer per prompt)
   - Fix: "Do you want Telegram?" → if yes → "What env var holds your bot token?"

2. **Expanded preference capture**
   - Work domain, expertise level, daily tools
   - Interaction preferences: verbosity, proactiveness, uncertainty handling
   - Tool preferences: shell confirmation mode, file write confirmation mode
   - Routing mode preference (with the tiered routing from 8g available)

3. **Structured identity data**
   - `identity.yaml` retains programmatically-needed fields: user_name, agent_name, pii_mode, routing_mode, bootstrap_version
   - New fields: expertise_level, verbosity, proactiveness, shell_confirm_mode, file_write_confirm_mode
   - Behavioral preferences live in `user.md` as natural language

4. **Re-bootstrap support**
   - `gogoclaw bootstrap --redo` reruns the bootstrap even if initialized
   - Preserves existing config as backup before overwriting
   - TUI settings panel (9a) can trigger a re-bootstrap from the Identity section

5. **Config migration tooling (CLI surface)**
   - The migration framework itself is built in 8a-i — this phase adds the user-facing CLI commands
   - `gogoclaw config migrate --dry-run` to preview changes without applying
   - `gogoclaw config status` to show current config version and available migrations

6. **Bootstrap model-aware defaults**
   - Fix hardcoded `max_context_tokens: 8192` in generated provider YAML
   - Look up model-specific context window sizes: GPT-4o (128k), Claude (200k), Llama3 (8k), etc.
   - Embedded model metadata table with known context windows, updated per release
   - Fallback to conservative default (8192) for unknown models

**Milestone:** Bootstrap captures a comprehensive user profile that makes the agent feel personalized from the first interaction. Config files migrate automatically between versions. Provider configs use model-appropriate context windows.

---

### Phase 9e: TUI Enhancements

**Goal:** Quality-of-life improvements and new information panels for daily TUI usage.

**Tasks:**

1. **Conversation auto-naming**
   - Heuristic title: when the user sends their first message, replace "New Conversation" with a truncated version of the input (~50 chars, clean word boundary)
   - LLM-generated title: after the first assistant response completes, fire a lightweight background request ("Generate a 3–6 word title for this exchange. Respond with ONLY the title.") to replace the heuristic title
   - If the LLM call fails, the heuristic title stays
   - Auto-naming fires once per conversation (on first exchange) and never re-triggers, including after soft boundaries

2. **Manual conversation rename**
   - In the conversation list panel (Ctrl+L): keybinding `r` or `F2` while a conversation is selected turns the title into an editable text field
   - Enter confirms, Escape cancels
   - Calls `storage.Store.UpdateConversationTitle` on confirm
   - `/rename <title>` slash command — works across all channels

3. **Audit log viewer (F5)**
   - Scrollable, filterable list of audit events from `~/.gogoclaw/audit/gogoclaw.jsonl`
   - Filter by event type: `llm_request`, `tool_call`, `network_blocked`, `pii_detected`, `skill_loaded`, `config_changed`
   - Filter by date range, provider, skill, or conversation
   - Selecting an event shows its full JSON payload in a detail pane
   - With tiered routing: see which tier each request was routed to and why
   - Highlight security-relevant events (`network_blocked`, `pii_detected`) with color coding
   - Export filtered results to a file

4. **Memory browser (F6)**
   - Scrollable list of stored memories, newest first
   - Search across all memories by keyword
   - Each entry shows: content, tags, category, source conversation, timestamp, relevance score
   - Delete individual memories
   - Memory system stats: total memory count, vector store size on disk, working memory usage
   - Relevance threshold adjustment — test different thresholds against a query

5. **Workspace browser (F7)**
   - Directory tree view of workspace: inbox, outbox, scratch, documents
   - File listing with name, size, modification time
   - View file contents in a read-only pane
   - Actions: delete files from scratch, move files between directories
   - "Mention in chat" option (inserts a `file_read` reference)

6. **Conversation search**
   - Full-text search across all stored conversations
   - Search results show conversation title, date, and matching snippet
   - Navigate directly to a conversation from search results

7. **Conversation export**
   - Export any conversation as markdown, JSON, or plain text
   - Export to outbox or a user-specified path
   - Batch export: export all conversations in a date range

8. **Keyboard shortcuts and command palette**
   - Ctrl+K opens a command palette with fuzzy search
   - Common actions: switch conversation, change agent, toggle PII mode, open settings, jump to any panel
   - Customizable key bindings in config

9. **Slash commands**
   - `/agent switch <n>` — switch agent profile for current conversation
   - `/pii <mode>` — change PII mode for current conversation
   - `/memory search <query>` — search memory from chat
   - `/model <tier>` — force a routing tier for current conversation
   - `/export` — export current conversation
   - `/cost` — show cost for current conversation
   - `/skills` — list available skills and their status
   - `/rename <title>` — rename current conversation
   - Consistent across all channels (TUI, Telegram, Discord, Slack, etc.)

10. **TUI visual polish**
    - Configurable color themes (dark, light, solarized, custom)
    - Improved tool call visualization: collapsible panels, timing information
    - Streaming response indicators (typing animation, token counter)
    - Status bar enhancements: show active routing tier, current cost, memory hit count

Full TUI panel architecture after Phase 9:
```
Escape    Chat (default)
F1        Conversations
F2        Health & Costs
F3        Settings
F4        Skills & Dependencies
F5        Audit Log
F6        Memory Browser
F7        Workspace Browser
Ctrl+K    Command Palette (fuzzy search to any panel or action)
```

**Milestone:** The TUI is a comprehensive, power-user-friendly interface with auto-named conversations, full skill management, audit visibility, memory browsing, workspace navigation, search, export, shortcuts, and visual refinements.

---

### Phase 9f: Audit Log Integrity & Security Hardening

**Goal:** Add tamper detection to the audit trail and fuzz-test security-critical input handling.

**Tasks:**

1. **HMAC chain**
   - Each audit log entry includes an HMAC of the current entry + previous entry's HMAC, creating a hash chain
   - HMAC key derived from a configurable secret (env var or config)
   - Verification tool: `gogoclaw audit verify` checks the chain and reports any broken links
   - First entry in a new log file includes a chain-start marker

2. **Log rotation**
   - Configurable log rotation by size or date (default: rotate daily, keep 30 days)
   - Each rotated file includes its final HMAC for cross-file chain verification

3. **Fuzz testing for input sanitization**
   - Fuzz `internal/security/sanitizer.go`: LLM output → skill input sanitization (instruction stripping, schema validation)
   - Fuzz `internal/pii/classifier.go`: PII pattern detection across malformed, Unicode-heavy, and adversarial inputs
   - Fuzz `internal/security/path.go`: path traversal prevention with edge cases (null bytes, Unicode normalization, double encoding)
   - Fuzz `internal/skill/host.go`: capability broker path validation
   - Use Go's native `testing.F` fuzz framework — runs in CI and locally
   - Fix any crashes or bypasses discovered during fuzzing

**Milestone:** Audit log tampering is detectable. Security-critical input handling has been fuzz-tested and hardened. Compliance-sensitive deployments can verify log integrity.

---

## Phase 10: Deployment, Interfaces & Channels

**Goal:** Enable headless deployment, then expand GoGoClaw's reach with a web interface and additional messaging platform channels. The Web UI inherits all interaction patterns, settings categories, skills management, and panel architecture established in Phase 9.

### Phase 10a: Container & Service Deployment

**Goal:** Production deployment support via Docker and systemd, enabling GoGoClaw to run as an always-on headless service.

**Why now:** Phase 8 made multi-channel operation safe (session isolation) and Phase 9 built the management UI. Headless deployment is the prerequisite for the Web UI (users access GoGoClaw from a browser on another machine), for Telegram webhooks in production, and for scheduled tasks (Phase 12b). Docker also makes Signal channel deployment practical (signal-cli runs as a sidecar).

**Tasks:**

1. **Dockerfile**
   - Multi-stage build: Go compilation stage → minimal runtime image (distroless or alpine)
   - Non-root user (`gogoclaw:gogoclaw`)
   - Read-only root filesystem with writable volume mount at `/data` (maps to `~/.gogoclaw/`)
   - No Linux capabilities (`--cap-drop ALL`)
   - Health check endpoint via REST API `/api/health`
   - Build args for version injection
   - `.dockerignore` for clean builds

2. **Docker Compose**
   - Single-service compose file for basic deployment
   - Volume mounts for config, data, and audit logs
   - Environment variable passthrough for API keys
   - Optional: multi-service compose with signal-cli sidecar (for Phase 10e)

3. **systemd unit file**
   - `gogoclaw.service` with full hardening: `NoNewPrivileges=yes`, `ProtectSystem=strict`, `ProtectHome=read-only`, `PrivateTmp=yes`, `ReadWritePaths=` for data directory only
   - `EnvironmentFile` support for API keys (reads from `~/.gogoclaw/env`)
   - Automatic restart on failure with backoff
   - `journalctl -u gogoclaw` for log viewing
   - Install instructions for common distros (Ubuntu/Debian, Fedora, Arch)

4. **Cross-platform binary releases**
   - Makefile targets for cross-compilation: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`
   - GitHub Actions workflow for automated release builds
   - Checksum file for release verification

5. **Deployment documentation**
   - Docker quickstart guide
   - systemd installation and hardening guide
   - Reverse proxy setup (nginx/Caddy) for TLS termination in front of REST API and Web UI
   - Backup and restore procedures for `~/.gogoclaw/` data

**Milestone:** GoGoClaw can be deployed as a Docker container or systemd service with production hardening. Cross-platform binaries are available for download. Users have clear deployment documentation.

---

### Phase 10b: Web UI

**Goal:** Browser-based interface backed by the existing REST API channel.

**Tasks:**

1. **Go-served SPA**
   - Static file server embedded in the GoGoClaw binary via `embed.FS`
   - Served on a configurable port (default: 8080)
   - No build step required for users — the UI ships inside the binary

2. **Core UI components**
   - Chat interface with streaming response rendering
   - Conversation list with search and auto-generated titles (mirrors TUI)
   - File upload (drag-and-drop, maps to REST API multipart upload)
   - File download from outbox
   - Tool call visualization (expandable panels showing what the agent did)
   - Markdown rendering for agent responses

3. **Settings, skills, and dashboards**
   - Web equivalents of all TUI panels: settings (9a), skills (9c), health/costs (9b), audit (9e), memory browser (9e), workspace browser (9e)
   - Same functional capabilities as the TUI versions

4. **Lifecycle profile**
   - Web UI uses `auto_boundary` lifecycle with 2-hour idle timeout
   - `beforeunload` hook fires explicit close event when user navigates away or closes tab
   - Conversation auto-naming and manual rename from within the Web UI

5. **Authentication**
   - Reuse REST API key authentication
   - Session management with configurable timeout
   - Optional: disable auth for localhost-only deployments

6. **Technology choices**
   - Minimal JavaScript framework (vanilla JS, htmx, or Preact — no heavy build toolchain)
   - Server-Sent Events for streaming responses
   - Mobile-responsive layout

**Milestone:** Users can interact with GoGoClaw from any browser with full feature parity to the TUI.

---

### Phase 10c: Discord Channel

**Goal:** Discord bot integration following the established channel pattern.

**Tasks:**

1. **Discord bot client**
   - Discord Gateway (WebSocket) connection for real-time messaging
   - Message handling: text, file attachments, embeds
   - Slash command registration for GoGoClaw commands
   - File transfer: attachments → inbox, outbox files → sent as Discord attachments

2. **Conversation mapping**
   - Discord channel ID + thread ID → GoGoClaw conversation
   - DM support: Discord user ID → GoGoClaw conversation
   - Lifecycle profile: `auto_boundary`, configurable idle timeout (default: 4h, same as Telegram)

3. **Access control**
   - Configurable allowed users, roles, or servers
   - Fail-closed: no allowlist = bot doesn't respond (same pattern as Telegram)

**Milestone:** GoGoClaw is accessible via Discord with the same capabilities as Telegram.

---

### Phase 10d: Slack Channel

**Goal:** Slack app integration following the established channel pattern.

**Tasks:**

1. **Slack app**
   - Slack Events API for real-time messaging (WebSocket mode or HTTP)
   - Message handling: text, file attachments, blocks/formatting
   - Slash command registration
   - File transfer via Slack's Files API

2. **Conversation mapping**
   - Slack channel ID + thread timestamp → GoGoClaw conversation
   - DM support: Slack user ID → GoGoClaw conversation
   - Lifecycle profile: `auto_boundary`, configurable idle timeout (default: 4h)

3. **Access control**
   - Configurable allowed workspaces, channels, and users
   - Fail-closed pattern

**Milestone:** GoGoClaw is accessible via Slack with the same capabilities as Telegram and Discord.

---

### Phase 10e: Signal Channel

**Goal:** Signal messaging integration aligned with GoGoClaw's security-first positioning.

**Tasks:**

1. **Signal bridge integration**
   - Integration via signal-cli or signald (community-maintained bridges)
   - Message handling: text, file attachments
   - Evaluate long-term maintenance risk of community bridges before committing

2. **Conversation mapping and access control**
   - Signal phone number or group ID → GoGoClaw conversation
   - Lifecycle profile: `auto_boundary`, configurable idle timeout (default: 4h)
   - Fail-closed access control

**Milestone:** GoGoClaw is accessible via Signal, the most security-aligned messaging platform in GoGoClaw's channel lineup.

---

## Phase 11: Skill Expansion & Multi-Runtime

**Goal:** Expand the skill ecosystem with new built-in skills, API-based tools, and support for Python and TypeScript skill runtimes.

### Phase 11a: Built-in Skill Expansion

**Goal:** Add commonly-needed skills that ship with GoGoClaw.

**Tasks:**

1. **Document processing skills**
   - Enhanced PDF processor: text extraction, page splitting, metadata reading
   - Spreadsheet processor: CSV/Excel read, filter, transform, basic charts
   - Markdown processor: format conversion, template rendering

2. **Data skills**
   - JSON/YAML processor: parse, query (jq-like), transform, validate
   - SQLite query skill: read-only queries against configured databases

**Milestone:** GoGoClaw ships with practical skills for common document and data processing tasks.

---

### Phase 11b: API Tools

**Goal:** Extend the core tool set with API-calling capabilities.

**Tasks:**

1. **HTTP client tool**
   - Enhanced `web_fetch` with POST/PUT/DELETE support
   - Header configuration, authentication helpers
   - Response parsing (JSON, XML, HTML extraction)
   - Subject to network allowlist

2. **Structured API tools**
   - REST API builder: define API endpoints in YAML, generate tools automatically
   - GraphQL support (basic query/mutation)

**Milestone:** The agent can interact with external APIs as first-class tools.

---

### Phase 11c: API Skills (WASM)

**Goal:** Community-installable skills for popular APIs packaged as WASM modules.

**Tasks:**

1. **API skill templates**
   - GitHub: issues, PRs, repo search
   - Calendar: event creation, listing, modification
   - Email: read, compose, send (via configured SMTP)

2. **Skill packaging**
   - Standardized manifest format for API skills
   - Credential handling via the secrets system

**Milestone:** Users can install API skills from a standard format with consistent security guarantees.

---

### Phase 11d: Python & TypeScript Skill Design

**Goal:** Design document for subprocess-based skill runtimes.

**Tasks:**

1. **Architecture design**
   - Hybrid two-tier runtime with explicit trust levels + strong Linux sandboxing (seccomp + Landlock)
   - One-shot lifecycle initially, warm pool as future optimization
   - JSON-RPC 2.0 capability broker protocol over newline-delimited stdin/stdout
   - GoGoClaw-managed dependency installation with shared package cache
   - Standalone runtimes at `~/.gogoclaw/runtimes/`

2. **Security model**
   - Platform-specific sandboxing: Linux (seccomp + Landlock), macOS (Seatbelt), Windows (Job Objects)
   - Graceful degradation with documented security implications per platform

**Milestone:** Complete design document approved before implementation begins.

---

### Phase 11e: Python & TypeScript Skill Implementation

**Goal:** Implement the designed subprocess skill runtimes.

**Tasks:**

1. **Subprocess runtime manager**
   - Process spawning, stdin/stdout communication, lifecycle management
   - Sandboxing per platform
   - Per-skill virtual environments (Python) and node_modules (TypeScript)

2. **SDKs**
   - Python SDK: broker client, tool decorator, `run()` entry point, file/HTTP wrappers
   - TypeScript SDK: same protocol, same capability set

3. **CLI commands**
   - `gogoclaw skill install <path>`, `remove`, `list`, `test`, `update`

**Milestone:** Python and TypeScript skills run alongside WASM skills with the same manifest format, same permission model, and platform-appropriate sandboxing.

---

## Phase 12: Orchestration & Intelligence

**Goal:** Transform GoGoClaw from a single-agent framework into an orchestration-capable platform.

### Phase 12a: Agent-to-Agent Delegation

**Goal:** An agent can spawn sub-agents with scoped tasks and reduced permissions.

**Tasks:**

1. **Sub-agent engine**
   - `delegate_task` core tool — creates a sub-agent with specific task, allowed tools, max turns, timeout
   - Sub-agent gets its own session with independent history and system prompt
   - Parent defines permission scope; sub-agent cannot escalate

2. **Agent profiles for delegation**
   - Predefined sub-agent profiles: "researcher", "coder", "analyst"
   - Custom profiles definable in `~/.gogoclaw/agents/`

3. **Resource limits**
   - Max concurrent sub-agents (default: 3), per-delegation token budget, timeout per delegation

**Milestone:** GoGoClaw agents can break complex tasks into subtasks, delegate to specialized sub-agents, and synthesize results.

---

### Phase 12b: Scheduled Tasks / Cron

**Goal:** Agent executes tasks on a schedule without user interaction.

**Tasks:**

1. **Task scheduler**
   - Cron-like schedule definitions in `~/.gogoclaw/schedules.yaml`
   - Task types: prompt-based, skill-based, pipeline (chained tasks)
   - Output and notification via any configured channel

2. **Integration with systemd**
   - Schedules run when GoGoClaw is running as a service
   - Missed schedule detection with grace period

**Milestone:** GoGoClaw can perform recurring tasks autonomously — daily summaries, periodic file processing, scheduled reports.

---

### Phase 12c: PII Gate v2

**Goal:** Upgrade PII detection from regex patterns to NER-based entity recognition.

**Tasks:**

1. **NER integration**
   - Replace or supplement regex patterns with a named entity recognition model
   - Options: embedded Python NER model (uses Phase 11e subprocess infrastructure), Go-native NER library, or cloud NER API
   - Target: detect names, addresses, dates of birth, and other PII that regex misses

2. **Context-aware classification**
   - Distinguish between PII in different contexts (e.g., "John Smith" as a client name vs. a historical figure)
   - Configurable sensitivity per entity type

**Milestone:** PII detection catches a significantly wider range of sensitive data with fewer false positives.

---

### Phase 12d: Voice Interface

**Goal:** Voice input/output for hands-free agent interaction.

**Tasks:**

1. **Audio pipeline**
   - Speech-to-text (STT): Whisper.cpp for local, cloud API for higher accuracy
   - Text-to-speech (TTS): local or cloud providers
   - Interrupt handling: user can speak while agent is responding

2. **Voice channel**
   - New channel type implementing the Channel interface
   - Push-to-talk and hands-free modes
   - Integration with Telegram voice messages

**Milestone:** Users can talk to GoGoClaw using their microphone and hear responses spoken back.

---

## Dependency Map

```
Critical path for Phase 8: 8a-i → 8b → 8e → 8f (standard memory architecture)

Phase 8a-i  (Session & hardening)    ── prerequisite for everything
├── 8a-ii (At-rest encryption)       ── depends on 8a-i, parallel with 8b
├── 8b    (Async summarization)      ── depends on 8a-i
│   └── 8e (Conversation lifecycle)  ── depends on 8b
│       └── 8f (Memory standard)     ── depends on 8e
├── 8c    (Telegram webhooks)        ── depends on 8a-i, parallel with 8b/8d
├── 8d    (Anthropic + cloud embed)  ── depends on 8a-i, parallel with 8b/8c
│   └── 8d-ii (llama.cpp)           ── depends on 8a-i, parallel with 8d
│       └── 8g (Tiered routing)      ── depends on 8d-ii (minimum); benefits from 8d
│           └── 8h (Memory enhanced) ── depends on 8f + 8g

Phase 9a  (TUI settings)    ── standalone (benefits from 8g for routing management UI);
                                includes Charmbracelet v2 migration
Phase 9b  (Cost tracking)   ── standalone (uses existing audit trail, benefits from 8g)
Phase 9c  (Skills manager)  ── standalone (uses existing skill registry); includes skill signing
Phase 9d  (Bootstrap)       ── benefits from 8g, benefits from 9a; adds CLI surface for
                                config migration framework built in 8a-i
Phase 9e  (TUI enhancements) ── benefits from 9a–9c (command palette needs all panels)
Phase 9f  (Audit & hardening) ── standalone; includes fuzz testing

Phase 10a (Docker/systemd)  ── standalone (benefits from 8a-i session isolation)
Phase 10b (Web UI)          ── depends on 10a (headless deployment), depends on 9a–9e
                                (inherits patterns); uses auto_boundary lifecycle (8e)
Phase 10c (Discord)         ── follows channel pattern from Telegram
Phase 10d (Slack)           ── follows channel pattern from Telegram
Phase 10e (Signal)          ── follows channel pattern, benefits from 10a (Docker sidecar)

Phase 11a (Built-in skills)  ── uses existing WASM runtime
Phase 11b (API tools)        ── extends core tools
Phase 11c (API skills)       ── uses existing WASM runtime
Phase 11d (Py/TS design)     ── standalone design document
Phase 11e (Py/TS impl)       ── depends on 11d

Phase 12a (Delegation)       ── depends on clean engine API
Phase 12b (Scheduled tasks)  ── depends on engine, benefits from 10a (systemd)
Phase 12c (PII gate v2)      ── extends existing PII gate, could leverage 11e
Phase 12d (Voice)            ── depends on at least one channel existing
```

**Parallelization in Phase 8:** After 8a-i, four streams run concurrently:

- **Stream 1:** 8b → 8e → 8f (engine refactor → lifecycle → standard memory)
- **Stream 2:** 8a-ii (at-rest encryption)
- **Stream 3:** 8c (Telegram webhooks)
- **Stream 4:** 8d + 8d-ii → 8g → 8h (providers → routing → enhanced memory)

Stream 4's 8h also depends on Stream 1's 8f, creating a join point. Stream 2 (8a-ii) can complete at any point without blocking other streams.

8g depends on 8d-ii (minimum — needs at least two provider types for routing). If 8d (Anthropic) is still in progress, 8g can proceed with `openai_compat` + `llamacpp` + `ollama`.

---

## Open Questions

1. **Web UI framework choice:** Vanilla JS, htmx, or Preact? Tradeoff between bundle size, development speed, and maintainability. Needs prototyping.

2. **Discord/Slack library selection:** Go ecosystem has multiple options for each. Evaluate: discordgo vs. arikawa (Discord), slack-go vs. slacker (Slack). Selection criteria: maintenance status, API coverage, WebSocket stability.

3. **Signal bridge reliability:** signal-cli and signald are community-maintained. Evaluate long-term viability and maintenance risk before committing.

4. **PII Gate v2 NER runtime:** Go-native NER libraries are limited. Options: embed a small Python NER model (uses Phase 11e subprocess infrastructure), use a Go port of a lightweight model, or use a cloud NER API. Tradeoff between accuracy, speed, and offline capability.

5. **Voice interface audio stack:** Cross-platform audio capture/playback is complex. Evaluate: PortAudio (CGo), pure Go options, or shelling out to system audio tools. May need to relax the "no CGo" constraint for this feature.

6. **Tiered routing classifier:** Rule-based heuristics are a starting point but may need LLM-based classification for accuracy. The meta-question: is it cost-effective to use a cheap model to classify whether you need an expensive model?

7. **Agent delegation depth:** MVP is single-level (agent → sub-agent). Should the design anticipate multi-level delegation?

8. **Bootstrap re-run strategy:** Merge (keep unchanged fields) vs. regenerate from scratch? Merge is friendlier but more complex.

9. **Reranker model hosting:** Cross-encoder models aren't natively supported as chat models in Ollama. Options: Jina reranker API (simple HTTP call, adds external dependency), run a dedicated reranker service alongside llama.cpp, or evaluate whether Ollama adds native reranker support. Decision affects 8h implementation complexity.

10. **Conversation lifecycle topic drift detection:** Deferred from standard lifecycle (8e) to enhanced (8h). Implementation options: rolling topic embedding with cosine similarity threshold, or LLM-based classification. Worth benchmarking false positive rates before enabling by default.
