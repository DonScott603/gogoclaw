package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DonScott603/gogoclaw/internal/tools"
)

// SkillDispatcher bridges the skill registry and WASM runtime with the
// tool dispatcher. It registers skill tools dynamically and routes
// tool calls to the appropriate WASM module.
type SkillDispatcher struct {
	registry *Registry
	runtime  *Runtime
}

// NewSkillDispatcher creates a dispatcher bridge between skills and the tool system.
func NewSkillDispatcher(reg *Registry, rt *Runtime) *SkillDispatcher {
	return &SkillDispatcher{
		registry: reg,
		runtime:  rt,
	}
}

// RegisterSkillTools loads all skills from the registry into the runtime
// and registers their tools on the core dispatcher.
func (sd *SkillDispatcher) RegisterSkillTools(ctx context.Context, d *tools.Dispatcher) error {
	for _, entry := range sd.registry.ListSkills() {
		if err := sd.runtime.LoadSkill(ctx, entry); err != nil {
			// Log but continue — don't let one bad skill break all skills.
			continue
		}
		for _, t := range entry.Manifest.Tools {
			toolName := t.Name
			skillName := entry.Manifest.Name

			var params json.RawMessage
			if t.Parameters != "" {
				params = json.RawMessage(t.Parameters)
			} else {
				params = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
			}

			d.Register(tools.ToolDef{
				Name:        toolName,
				Description: fmt.Sprintf("[skill:%s] %s", skillName, t.Description),
				Parameters:  params,
				Fn:          sd.makeToolFunc(skillName, toolName),
			})
		}
	}
	return nil
}

// ListSkillTools implements tools.SkillLister for discover_tools.
func (sd *SkillDispatcher) ListSkillTools() []tools.DiscoverableSkillTool {
	var result []tools.DiscoverableSkillTool
	for _, entry := range sd.registry.ListSkills() {
		for _, t := range entry.Manifest.Tools {
			result = append(result, tools.DiscoverableSkillTool{
				SkillName:       entry.Manifest.Name,
				SkillDesc:       entry.Manifest.Description,
				ToolName:        t.Name,
				ToolDescription: t.Description,
				Parameters:      t.Parameters,
			})
		}
	}
	return result
}

// makeToolFunc creates a ToolFunc that routes a tool call to the WASM runtime.
func (sd *SkillDispatcher) makeToolFunc(skillName, toolName string) tools.ToolFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		// Wrap the args with the tool name so the skill knows which tool was called.
		envelope := map[string]json.RawMessage{
			"tool": json.RawMessage(fmt.Sprintf("%q", toolName)),
			"args": args,
		}
		envelopeBytes, err := json.Marshal(envelope)
		if err != nil {
			return "", fmt.Errorf("skill dispatch: marshal envelope: %w", err)
		}

		result, err := sd.runtime.Execute(ctx, skillName, envelopeBytes)
		if err != nil {
			return "", fmt.Errorf("skill dispatch: %w", err)
		}

		// Try to extract a "result" field for cleaner output.
		var parsed map[string]interface{}
		if json.Unmarshal(result, &parsed) == nil {
			if r, ok := parsed["result"]; ok {
				if s, ok := r.(string); ok {
					return s, nil
				}
			}
			if e, ok := parsed["error"]; ok {
				if s, ok := e.(string); ok {
					return "", fmt.Errorf("skill %s: %s", skillName, s)
				}
			}
		}

		return strings.TrimSpace(string(result)), nil
	}
}

// Close unloads all skills and shuts down the runtime.
func (sd *SkillDispatcher) Close(ctx context.Context) error {
	return sd.runtime.Close(ctx)
}
