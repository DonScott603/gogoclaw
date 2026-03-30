# GoGoClaw Skill Development Guide

## 1. Overview

Skills are sandboxed WASM extensions that add new tools to GoGoClaw. Unlike **core tools** (such as `file_read`, `shell`, `web_fetch`, and `think`), which are compiled into the GoGoClaw binary and are always available, skills are standalone WASM binaries that run inside wazero sandboxes with strictly limited permissions.

Skills never receive direct filesystem or network access. All I/O is mediated by host functions that the capability broker validates against the skill's declared permissions before executing.

### Two-tier discovery system

GoGoClaw uses a two-tier model for tool availability:

1. **Core tools** -- registered at startup and always visible to the LLM.
2. **Skill tools** -- loaded from `~/.gogoclaw/skills.d/` at startup and surfaced through the `discover_tools` meta-tool. The LLM calls `discover_tools` with a natural-language query, receives a list of matching skill tools, and can then invoke them by name.

This keeps the LLM's default tool list small while allowing an unbounded number of skills to be installed.

---

## 2. Manifest Format

Every skill directory must contain a `manifest.yaml` file. This file declares the skill's metadata, the tools it exports, and the permissions it requires.

### Complete reference

```yaml
# Required metadata
name: my-skill               # unique identifier, lowercase with hyphens
version: "1.0.0"             # semver string
description: "What this skill does"
author: "Your Name"

# Binary integrity (recommended)
hash: "sha256:abc123..."     # SHA-256 hex digest of the .wasm file
                             # verified at load time; mismatch = skill rejected

# Tool definitions
tools:
  - name: tool_name          # tool name the LLM will call
    description: "What this tool does"
    parameters: |             # JSON Schema string
      {
        "type": "object",
        "properties": {
          "input": {
            "type": "string",
            "description": "The input value"
          }
        },
        "required": ["input"],
        "additionalProperties": false
      }

# Permissions -- all optional, deny-by-default
permissions:
  filesystem:
    read_paths:               # paths relative to workspace root
      - "."
      - "src/"
    write_paths:
      - "output/"

  network:
    allowed: false            # must be true to make any outbound requests
    domains:                  # if allowed=true, restrict to these domains
      - "api.example.com"

  env_vars:                   # environment variable names the skill may read
    - "MY_API_KEY"

  max_file_size: 1048576      # max bytes per file write (0 = default)
  max_execution_time: 30      # max seconds before timeout (0 = default)
```

### Field notes

- `name`, `version`, and `description` are required. The manifest will fail validation without them.
- Every entry in `tools` must have a `name` and `description`. If `parameters` is omitted, it defaults to an empty JSON Schema object.
- `hash` uses the format `sha256:<hex>` and is matched against the `.wasm` file in the skill directory. If the hash field is present and the digest does not match, the skill is rejected at load time.
- `max_execution_time` is specified in seconds. When the timeout fires, the WASM module is forcibly terminated.

---

## 3. Permission Model

GoGoClaw enforces a **capability broker** pattern. Skills cannot perform I/O directly. Instead, they call host functions exposed by the wazero runtime. The capability broker intercepts every host function call and validates it against the skill's manifest before executing.

### How it works

1. A skill calls a host function, e.g., `host_file_read(path)`.
2. The capability broker looks up the skill's registered permissions.
3. For filesystem operations, the broker resolves the absolute path (including symlink evaluation via `filepath.EvalSymlinks`) and checks that it falls under one of the allowed `read_paths` or `write_paths`.
4. For network operations, the broker checks both the skill manifest (`permissions.network.allowed` and `permissions.network.domains`) and the global network allowlist.
5. For environment variable access, the broker checks that the variable name appears in `permissions.env_vars`.
6. For file writes, the broker enforces `max_file_size` if configured.
7. If the check fails, the host function returns an error to the skill. The operation is never executed.

### Key security properties

- **Deny by default.** A skill with no `permissions` block has no filesystem access, no network access, and no environment variable access.
- **Symlink-aware path validation.** The broker resolves symlinks before checking path boundaries, preventing symlink escape attacks.
- **Double-gated network access.** Even if a skill's manifest allows a domain, the request must also pass the global network allowlist (`security/network.go`).
- **Execution timeout.** The wazero runtime is configured with `WithCloseOnContextDone(true)`, so context cancellation forcibly terminates the WASM module.
- **Permissions enforced by the broker, never by the skill.** A malicious or buggy skill cannot bypass the broker because it has no way to make raw syscalls from inside the WASM sandbox.

---

## 4. Writing a Skill in Go

### Prerequisites

- Go 1.21+ (WASI support via `GOOS=wasip1` was added in Go 1.21)
- GoGoClaw installed and configured

### Step-by-step

#### 4.1. Create the skill directory

```bash
mkdir -p ~/.gogoclaw/skills.d/myskill
cd ~/.gogoclaw/skills.d/myskill
```

#### 4.2. Write manifest.yaml

Create `manifest.yaml` with your skill's metadata, tools, and permissions. See section 2 for the full reference.

#### 4.3. Write main.go

Skills are standard Go programs compiled to WASI. They receive input as JSON on stdin and write output as JSON to stdout. The runtime wraps the tool call in an envelope:

```json
{
  "tool": "tool_name",
  "args": { ... }
}
```

Your `main()` function should:

1. Read JSON from stdin.
2. Parse the envelope to determine which tool was called and extract its arguments.
3. Perform the requested operation (using host functions for any I/O).
4. Write a JSON response to stdout.

The response should have one of these shapes:

```json
{"result": "success message or output"}
```

```json
{"error": "description of what went wrong"}
```

#### 4.4. Implement tool functions

Each tool declared in `manifest.yaml` should have a corresponding handler in your Go code. Route calls based on the `tool` field in the input envelope.

#### 4.5. Build the WASM binary

```bash
GOOS=wasip1 GOARCH=wasm go build -o myskill.wasm .
```

#### 4.6. Generate and record the hash

```bash
sha256sum myskill.wasm
```

Copy the hex digest and update the `hash` field in `manifest.yaml`:

```yaml
hash: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
```

Note: the `sha256:` prefix in the manifest is for human readability. The registry strips it and compares the raw hex digest.

---

## 5. Example: text-counter Skill

This walkthrough builds a simple skill that counts words, characters, and lines in text input.

### 5.1. manifest.yaml

```yaml
name: text-counter
version: "1.0.0"
description: "Count words, characters, and lines in text"
author: "GoGoClaw Examples"

hash: ""   # fill in after building

tools:
  - name: count_text
    description: "Count words, characters, and lines in a given text string"
    parameters: |
      {
        "type": "object",
        "properties": {
          "text": {
            "type": "string",
            "description": "The text to analyze"
          }
        },
        "required": ["text"],
        "additionalProperties": false
      }

permissions:
  max_execution_time: 10
```

This skill needs no filesystem, network, or environment variable access -- it operates purely on the text passed in through the tool arguments.

### 5.2. main.go

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// envelope is the input format sent by the GoGoClaw runtime.
type envelope struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

// countTextArgs matches the JSON Schema in the manifest.
type countTextArgs struct {
	Text string `json:"text"`
}

// countResult is the structured output.
type countResult struct {
	Words      int `json:"words"`
	Characters int `json:"characters"`
	Lines      int `json:"lines"`
}

func main() {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		writeError(fmt.Sprintf("read stdin: %v", err))
		return
	}

	var env envelope
	if err := json.Unmarshal(input, &env); err != nil {
		writeError(fmt.Sprintf("parse envelope: %v", err))
		return
	}

	switch env.Tool {
	case "count_text":
		handleCountText(env.Args)
	default:
		writeError(fmt.Sprintf("unknown tool: %s", env.Tool))
	}
}

func handleCountText(rawArgs json.RawMessage) {
	var args countTextArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		writeError(fmt.Sprintf("parse args: %v", err))
		return
	}

	result := countResult{
		Words:      len(strings.Fields(args.Text)),
		Characters: len(args.Text),
		Lines:      strings.Count(args.Text, "\n") + 1,
	}

	out, _ := json.Marshal(map[string]interface{}{
		"result": fmt.Sprintf("Words: %d, Characters: %d, Lines: %d",
			result.Words, result.Characters, result.Lines),
		"detail": result,
	})
	os.Stdout.Write(out)
}

func writeError(msg string) {
	out, _ := json.Marshal(map[string]string{"error": msg})
	os.Stdout.Write(out)
}
```

### 5.3. Build and install

```bash
cd ~/.gogoclaw/skills.d/text-counter

# Initialize the Go module
go mod init text-counter

# Build for WASI
GOOS=wasip1 GOARCH=wasm go build -o text-counter.wasm .

# Generate the hash and update the manifest
sha256sum text-counter.wasm
# Copy the hex digest and update the hash field in manifest.yaml
```

Edit `manifest.yaml` and replace the empty `hash` value with the `sha256:<hex>` string.

### 5.4. Testing

Restart GoGoClaw. The skill registry scans `~/.gogoclaw/skills.d/` at startup and loads every valid skill directory it finds. You should see the skill load in the startup log.

To verify the skill is available, ask the agent to use `discover_tools` with a query like "count text" -- it should return the `count_text` tool from the `text-counter` skill.

You can then invoke the tool by asking the agent something like: "Count the words and characters in this text: Hello world, this is a test."

---

## 6. Installation

To install a skill:

1. Place the skill directory (containing `manifest.yaml` and the `.wasm` binary) under `~/.gogoclaw/skills.d/`.
2. Restart GoGoClaw.

The registry scans `~/.gogoclaw/skills.d/` at startup. Each subdirectory is expected to contain:

- A `manifest.yaml` file that passes validation.
- Exactly one `.wasm` file.

If a skill directory is malformed or fails hash verification, it is skipped with a log warning. Other skills continue to load normally.

Once loaded, skill tools are registered on the core tool dispatcher with a `[skill:<name>]` prefix in their description. The `discover_tools` meta-tool lists all available skill tools and allows the LLM to find them by keyword search.

To uninstall a skill, delete its directory from `skills.d/` and restart.

---

## 7. Security Notes

- **Hash pinning.** When a `hash` field is present in the manifest, the registry computes the SHA-256 digest of the `.wasm` file and compares it to the declared value. A mismatch causes the skill to be rejected. This prevents tampering with skill binaries after installation.

- **Signing verification.** Cryptographic signing of skill binaries is planned for a future release but is not yet implemented. Until then, hash pinning is the primary integrity mechanism.

- **Untrusted sources.** Never install skills from untrusted sources. A skill has access to whatever its manifest declares, and while the broker enforces those boundaries, a broadly-permissioned skill from a malicious author could read or write files within its allowed paths.

- **Audit logging.** The capability broker supports a log callback that records every skill load and every brokered operation (file reads, file writes, network checks, env var access). Enable audit logging in your GoGoClaw configuration to maintain a record of skill activity.

- **Least privilege.** Declare the minimum permissions your skill needs. A skill that only processes text input needs no filesystem, network, or env var permissions at all. A skill that reads files should list only the specific directories it needs in `read_paths`, not the entire workspace.

- **WASM sandbox guarantees.** The wazero runtime provides memory isolation and instruction-level sandboxing. Skills cannot access host memory, make raw syscalls, or escape the sandbox. All interaction with the outside world goes through explicitly registered host functions that the capability broker controls.
