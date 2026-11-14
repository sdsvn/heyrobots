package executor

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/genai"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
)

// The owner's own tasks can run commands in their folder; a task for
// someone the owner invited can't.
func TestOwnerTasksRunCommands(t *testing.T) {
	e := newEnv(t)
	e.model.steps = [][]*genai.Part{
		call("run_command", map[string]any{"command": "printf '%s' made > out.txt && cat out.txt && exit 3"}),
		reportCall("ready", "Ran it.", "made", ""),
	}
	e.say(owner, "c1", "make a file")
	e.evalAll()
	if !e.work() {
		t.Fatal("no assignment ran")
	}
	if len(e.model.results) == 0 || e.model.results[0]["output"] != "made" || e.model.results[0]["exit_code"] != float64(3) {
		t.Fatalf("run_command answered %v", e.model.results)
	}

	e.setPolicy(func(p *policy.Policy) {
		p.Delegates[0].Toolsets = []core.Toolset{core.ToolsetWriting}
	})
	e.model.steps = [][]*genai.Part{reportCall("ready", "Done.", "ok", "")}
	e.model.n = 0
	e.say(delegate, "d1", "make a file")
	e.evalAll()
	e.work()
	if tools := e.model.tools[len(e.model.tools)-1]; slices.Contains(tools, "run_command") {
		t.Fatalf("a delegate's task got run_command: %s", strings.Join(tools, ","))
	}
}
