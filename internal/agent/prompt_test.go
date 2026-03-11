package agent

import (
	"strings"
	"testing"
	"time"
)

func TestResolveTemplateVarsBuiltins(t *testing.T) {
	prompt := "Today is {{current_date}} at {{current_time}}."
	result := ResolveTemplateVars(prompt, nil)

	today := time.Now().Format("2006-01-02")
	if !strings.Contains(result, today) {
		t.Errorf("result = %q, should contain today's date %q", result, today)
	}
	if strings.Contains(result, "{{current_date}}") {
		t.Error("{{current_date}} was not resolved")
	}
	if strings.Contains(result, "{{current_time}}") {
		t.Error("{{current_time}} was not resolved")
	}
}

func TestResolveTemplateVarsCustom(t *testing.T) {
	prompt := "Hello {{user_name}}, you are {{agent_name}}."
	vars := map[string]string{
		"user_name":  "Alice",
		"agent_name": "GoGoClaw",
	}
	result := ResolveTemplateVars(prompt, vars)

	if result != "Hello Alice, you are GoGoClaw." {
		t.Errorf("result = %q", result)
	}
}

func TestResolveTemplateVarsMissingLeftAsIs(t *testing.T) {
	prompt := "Hello {{unknown_var}}, today is {{current_date}}."
	result := ResolveTemplateVars(prompt, nil)

	if !strings.Contains(result, "{{unknown_var}}") {
		t.Error("missing var should be left as-is")
	}
	if strings.Contains(result, "{{current_date}}") {
		t.Error("{{current_date}} should be resolved")
	}
}

func TestResolveTemplateVarsOverrideBuiltin(t *testing.T) {
	prompt := "Date: {{current_date}}"
	vars := map[string]string{
		"current_date": "2099-12-31",
	}
	result := ResolveTemplateVars(prompt, vars)

	if result != "Date: 2099-12-31" {
		t.Errorf("result = %q, caller vars should override builtins", result)
	}
}

func TestResolveTemplateVarsNoPlaceholders(t *testing.T) {
	prompt := "No placeholders here."
	result := ResolveTemplateVars(prompt, nil)
	if result != prompt {
		t.Errorf("result = %q, want unchanged", result)
	}
}
