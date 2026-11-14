package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"heyrobots/service/internal/core"
)

// Limits on a command an agent runs.
const (
	commandTime    = 2 * time.Minute
	commandMaxTime = 10 * time.Minute
	commandOutput  = 20_000
)

// commandsNote tells an agent it may run commands.
const commandsNote = "You may run commands in your task folder to get work done, such as a Python or Node script that makes a PDF, a chart or a spreadsheet. Install what you need there. When the result is a file, share it with share_file."

// mayRun reports whether the task may run commands on this machine. It
// runs as the owner's user with no sandbox yet, so only the owner's own
// tasks may, or a task given the code or data toolset.
//
// ponytail: no sandbox. Commands run as the server's user on the owner's
// own machine, which the owner accepted for now; a container or VM
// sandbox comes with cloud hosting, where the machine isn't the owner's.
func (r *run) mayRun() bool {
	return r.mandate.Role == core.RoleOwner || r.hasToolset(core.ToolsetCode) || r.hasToolset(core.ToolsetData)
}

type commandArgs struct {
	Command string `json:"command" jsonschema:"a shell command, run in your task folder"`
	Seconds int    `json:"seconds,omitempty" jsonschema:"how long it may run, up to 600; 120 if left out"`
}

// CommandResult is what run_command answers.
type CommandResult struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
	TimedOut bool   `json:"timed_out,omitempty"`
	Error    string `json:"error,omitempty"`
}

// commandTool runs shell commands in the task folder, for agents on a model
// API; the owner's own agent (Claude Code, omp) has its own.
func (r *run) commandTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "run_command",
		Description: "Run a shell command in your task folder, such as python3 make_pdf.py or npm install. You get its output and exit code. " + commandsNote,
	}, func(tc agent.Context, a commandArgs) (CommandResult, error) {
		ws, err := filepath.Abs(filepath.Join(r.e.Workspaces, r.c.Ticket.ID))
		if err != nil {
			return CommandResult{}, err
		}
		if err := os.MkdirAll(ws, 0o700); err != nil {
			return CommandResult{}, err
		}
		limit := commandTime
		if a.Seconds > 0 {
			limit = min(time.Duration(a.Seconds)*time.Second, commandMaxTime)
		}
		ctx, cancel := context.WithTimeout(tc, limit)
		defer cancel()
		shell, flag := "/bin/sh", "-c"
		if runtime.GOOS == "windows" {
			shell, flag = "cmd", "/C"
		}
		cmd := exec.CommandContext(ctx, shell, flag, a.Command)
		cmd.Dir = ws
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err = cmd.Run()
		res := CommandResult{Output: tail(out.String(), commandOutput)}
		var exit *exec.ExitError
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			res.TimedOut, res.ExitCode = true, -1
			res.Error = fmt.Sprintf("stopped after %s", limit)
		case errors.As(err, &exit):
			res.ExitCode = exit.ExitCode()
		case err != nil:
			res.ExitCode, res.Error = -1, err.Error()
		}
		return res, nil
	})
}

// tail keeps the end of a long output, where errors usually are.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…(earlier output cut)\n" + s[len(s)-n:]
}
