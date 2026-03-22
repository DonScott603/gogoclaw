package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ConfirmFunc is called before executing a shell command.
// It should return true to allow execution, false to deny.
type ConfirmFunc func(command string) bool

// RegisterShellTool registers the shell_exec tool with a confirmation gate.
func RegisterShellTool(d *Dispatcher, confirm ConfirmFunc) {
	d.Register(ToolDef{
		Name:        "shell_exec",
		Description: "Execute a shell command. Requires confirmation before running.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"command": {"type": "string", "description": "The shell command to execute"}
			},
			"required": ["command"],
			"additionalProperties": false
		}`),
		Fn: shellExecFn(confirm),
	})
}

type shellExecArgs struct {
	Command string `json:"command"`
}

func shellExecFn(confirm ConfirmFunc) ToolFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var a shellExecArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("shell_exec: parse args: %w", err)
		}
		if a.Command == "" {
			return "", fmt.Errorf("shell_exec: empty command")
		}

		if runtime.GOOS == "windows" {
			if reason, blocked := isWindowsBlockedCommand(a.Command); blocked {
				return "", fmt.Errorf("shell_exec: %s", reason)
			}
		}

		if confirm != nil && !confirm(a.Command) {
			return "Command execution denied by user.", nil
		}

		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "cmd", "/C", a.Command)
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-c", a.Command)
		}

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		result := stdout.String()
		if stderr.Len() > 0 {
			result += "\nSTDERR:\n" + stderr.String()
		}
		if err != nil {
			result += fmt.Sprintf("\nExit: %v", err)
		}
		return result, nil
	}
}

// windowsBlockedCommands maps commands that hang interactively on Windows
// to their suggested alternatives.
var windowsBlockedCommands = map[string]string{
	"date":   `Command "date" is interactive on Windows and would hang. Use PowerShell: Get-Date instead.`,
	"time":   `Command "time" is interactive on Windows and would hang. Use PowerShell: Get-Date instead.`,
	"set /p": `Command "set /p" is interactive on Windows and would hang.`,
	"pause":  `Command "pause" is interactive on Windows and would hang.`,
}

// isWindowsBlockedCommand checks if the command starts with a known
// interactive Windows command. Returns the reason and true if blocked.
func isWindowsBlockedCommand(command string) (string, bool) {
	trimmed := strings.TrimSpace(command)
	lower := strings.ToLower(trimmed)
	for blocked, reason := range windowsBlockedCommands {
		if lower == blocked || strings.HasPrefix(lower, blocked+" ") || strings.HasPrefix(lower, blocked+"\t") {
			return reason, true
		}
	}
	return "", false
}
