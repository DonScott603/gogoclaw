package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// mockSkillLister implements SkillLister for testing.
type mockSkillLister struct {
	tools []DiscoverableSkillTool
}

func (m *mockSkillLister) ListSkillTools() []DiscoverableSkillTool {
	return m.tools
}

func TestDiscoverToolsFindsMatch(t *testing.T) {
	lister := &mockSkillLister{
		tools: []DiscoverableSkillTool{
			{
				SkillName:       "text",
				SkillDesc:       "Text manipulation utilities",
				ToolName:        "text_uppercase",
				ToolDescription: "Convert text to uppercase",
				Parameters:      `{"type":"object","properties":{"text":{"type":"string"}}}`,
			},
			{
				SkillName:       "math",
				SkillDesc:       "Math operations",
				ToolName:        "math_add",
				ToolDescription: "Add two numbers",
			},
		},
	}

	d := NewDispatcher(0)
	RegisterDiscoverTool(d, lister)

	args, _ := json.Marshal(map[string]string{"query": "uppercase text"})
	results := d.Dispatch(context.Background(), []ToolCallRequest{
		{ID: "1", Name: "discover_tools", Arguments: args},
	})

	if results[0].IsError {
		t.Fatalf("unexpected error: %s", results[0].Content)
	}
	if !strings.Contains(results[0].Content, "text_uppercase") {
		t.Errorf("expected result to contain text_uppercase, got: %s", results[0].Content)
	}
	if !strings.Contains(results[0].Content, "1 matching tool") {
		t.Errorf("expected 1 matching tool, got: %s", results[0].Content)
	}
}

func TestDiscoverToolsNoMatch(t *testing.T) {
	lister := &mockSkillLister{
		tools: []DiscoverableSkillTool{
			{
				SkillName:       "text",
				SkillDesc:       "Text utilities",
				ToolName:        "text_uppercase",
				ToolDescription: "Convert text to uppercase",
			},
		},
	}

	d := NewDispatcher(0)
	RegisterDiscoverTool(d, lister)

	args, _ := json.Marshal(map[string]string{"query": "database migration"})
	results := d.Dispatch(context.Background(), []ToolCallRequest{
		{ID: "1", Name: "discover_tools", Arguments: args},
	})

	if results[0].IsError {
		t.Fatalf("unexpected error: %s", results[0].Content)
	}
	if !strings.Contains(results[0].Content, "No skill tools found") {
		t.Errorf("expected no match message, got: %s", results[0].Content)
	}
}

func TestDiscoverToolsNilLister(t *testing.T) {
	d := NewDispatcher(0)
	RegisterDiscoverTool(d, nil)

	args, _ := json.Marshal(map[string]string{"query": "anything"})
	results := d.Dispatch(context.Background(), []ToolCallRequest{
		{ID: "1", Name: "discover_tools", Arguments: args},
	})

	if results[0].IsError {
		t.Fatalf("unexpected error: %s", results[0].Content)
	}
	if !strings.Contains(results[0].Content, "not configured") {
		t.Errorf("expected not configured message, got: %s", results[0].Content)
	}
}

func TestDiscoverToolsMultipleMatches(t *testing.T) {
	lister := &mockSkillLister{
		tools: []DiscoverableSkillTool{
			{SkillName: "text", SkillDesc: "Text utils", ToolName: "text_upper", ToolDescription: "Uppercase text"},
			{SkillName: "text", SkillDesc: "Text utils", ToolName: "text_lower", ToolDescription: "Lowercase text"},
			{SkillName: "math", SkillDesc: "Math ops", ToolName: "math_add", ToolDescription: "Add numbers"},
		},
	}

	d := NewDispatcher(0)
	RegisterDiscoverTool(d, lister)

	args, _ := json.Marshal(map[string]string{"query": "text"})
	results := d.Dispatch(context.Background(), []ToolCallRequest{
		{ID: "1", Name: "discover_tools", Arguments: args},
	})

	if results[0].IsError {
		t.Fatalf("unexpected error: %s", results[0].Content)
	}
	if !strings.Contains(results[0].Content, "2 matching tool") {
		t.Errorf("expected 2 matching tools, got: %s", results[0].Content)
	}
}
