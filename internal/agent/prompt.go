package agent

import (
	"strings"
	"time"
)

// ResolveTemplateVars replaces {{key}} placeholders in prompt with values
// from vars. Built-in variables (current_date, current_time, agent_name)
// are resolved automatically. Missing keys are left as-is.
func ResolveTemplateVars(prompt string, vars map[string]string) string {
	now := time.Now()

	builtins := map[string]string{
		"current_date": now.Format("2006-01-02"),
		"current_time": now.Format("15:04"),
	}

	// Caller vars override builtins.
	merged := make(map[string]string, len(builtins)+len(vars))
	for k, v := range builtins {
		merged[k] = v
	}
	for k, v := range vars {
		merged[k] = v
	}

	result := prompt
	for key, val := range merged {
		result = strings.ReplaceAll(result, "{{"+key+"}}", val)
	}
	return result
}
