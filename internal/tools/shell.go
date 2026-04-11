package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ConfirmFunc is called before executing a shell command.
// It should return true to allow execution, false to deny.
type ConfirmFunc func(command string) bool

// RegisterShellTool registers the shell_exec tool with a confirmation gate and timeout.
func RegisterShellTool(d *Dispatcher, confirm ConfirmFunc, timeout time.Duration) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
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
		Fn: shellExecFn(confirm, timeout),
	})
}

type shellExecArgs struct {
	Command string `json:"command"`
}

func shellExecFn(confirm ConfirmFunc, timeout time.Duration) ToolFunc {
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

		// Create a child context with the configured timeout.
		timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(timeoutCtx, "cmd", "/C", a.Command)
		} else {
			cmd = exec.CommandContext(timeoutCtx, "sh", "-c", a.Command)
		}

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()

		// Check for timeout.
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return fmt.Sprintf("Command timed out after %s and was terminated", timeout), nil
		}

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
	"date":   `Command "date" is interactive on Windows and would hang. Use: powershell -command Get-Date`,
	"time":   `Command "time" is interactive on Windows and would hang. Use: powershell -command Get-Date -Format HH:mm:ss`,
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
