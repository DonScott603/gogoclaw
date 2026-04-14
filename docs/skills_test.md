# GoGoClaw Skill Development Guide

## 1. Overview

Skills are sandboxed WASM extensions that add new tools to GoGoClaw. Unlike **core tools** (such as `file_read`, `shell`, `web_fetch`, and `think`), which are compiled into the GoGoClaw binary and are always available, skills are standalone WASM binaries that run inside wazero sandboxes with strictly limited permissions.

Skills never receive direct filesystem or network access. All I/O is mediated by host functions that the capability broker validates against the skill's declared permissions before executing.

### Two-tier discovery system

GoGoClaw uses a two-tier model for tool availability:

1. **Core tools** -- registered at startup and always visible to the LLM.
2. **Skill tools** -- loaded from `~/.gogoclaw/skills.d/` at startup and surfaced through the `discover_tools` meta-tool. The LLM calls `discover_tools` with a natural-language query, receives a list of matching skill tools, and can then invoke them by name.

This keeps the LLM's default tool list small while allowing an unbounded number of skills to be installed.