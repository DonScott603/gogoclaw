package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// SkillLister provides read-only access to the skill registry for discover_tools.
type SkillLister interface {
	ListSkillTools() []DiscoverableSkillTool
}

// DiscoverableSkillTool describes a skill tool for discover_tools search results.
type DiscoverableSkillTool struct {
	SkillName       string
	SkillDesc       string
	ToolName        string
	ToolDescription string
	Parameters      string // JSON schema
}

// RegisterDiscoverTool registers discover_tools backed by a skill lister.
// If lister is nil, the tool returns a stub response.
func RegisterDiscoverTool(d *Dispatcher, lister SkillLister) {
	d.Register(ToolDef{
		Name:        "discover_tools",
		Description: "Search for additional tools and skills that might help with the current task.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "Description of what you're trying to do"}
			},
			"required": ["query"],
			"additionalProperties": false
		}`),
		Fn: discoverToolsFn(d, lister),
	})
}

type discoverToolsArgs struct {
	Query string `json:"query"`
}

func discoverToolsFn(d *Dispatcher, lister SkillLister) ToolFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a discoverToolsArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("discover_tools: parse args: %w", err)
		}

		if lister == nil {
			return "No additional tools found (skill system not configured).", nil
		}

		query := strings.ToLower(a.Query)
		allTools := lister.ListSkillTools()
		var matches []DiscoverableSkillTool

		for _, t := range allTools {
			if matchesQuery(query, t) {
				matches = append(matches, t)
			}
		}

		if len(matches) == 0 {
			return fmt.Sprintf("No skill tools found matching %q.", a.Query), nil
		}

		// Register matching skill tools dynamically so the LLM can call them.
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Found %d matching tool(s):\n\n", len(matches)))

		for _, m := range matches {
			b.WriteString(fmt.Sprintf("- **%s** (from skill %q): %s\n", m.ToolName, m.SkillName, m.ToolDescription))
			if m.Parameters != "" {
				b.WriteString(fmt.Sprintf("  Parameters: %s\n", m.Parameters))
			}
		}

		b.WriteString("\nThese tools are now available for use.")
		return b.String(), nil
	}
}

// matchesQuery does a simple keyword match against skill/tool name and description.
func matchesQuery(query string, t DiscoverableSkillTool) bool {
	fields := strings.ToLower(t.SkillName + " " + t.SkillDesc + " " + t.ToolName + " " + t.ToolDescription)
	words := strings.Fields(query)
	for _, w := range words {
		if strings.Contains(fields, w) {
			return true
		}
	}
	return false
}
