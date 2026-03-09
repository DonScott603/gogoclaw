package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
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
			"required": ["command"]
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
