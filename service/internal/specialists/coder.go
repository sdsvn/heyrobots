package specialists

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"heyrobots/service/internal/core"
)

// ErrCannotRun is what Host.RunCommand returns when the task may not run
// commands.
var ErrCannotRun = errors.New("this task may not run commands")

func coder() Spec {
	return Spec{
		ID: "coder", AgentID: "ag_coder", Name: "Coder",
		Purpose: "Writes and fixes code, and runs the tests before saying it works.",
		Color:   "violet", Badge: "code",
		Card: Card{
			Skills: []Skill{
				{Description: "Writes a small program or script and proves it runs", Tags: []string{"script", "program"},
					Examples: []string{"Write a script that renames my photos by the date they were taken", "Make a small tool that turns this CSV into a chart"}},
				{Description: "Fixes a bug or a failing test and shows the tests passing", Tags: []string{"bug", "fix", "tests"},
					Examples: []string{"This function returns the wrong total; fix it", "Why does this test fail?"}},
			},
			NotFor: []string{"research without code", "mail and calendar", "setting up apps"},
		},
		Toolsets: []core.Toolset{core.ToolsetCode, core.ToolsetData, core.ToolsetResearch},
		Tools:    []string{"read_file", "write_file", "list_files", "run_command", "web_search", "web_fetch", "save_note", "read_notes"},
		Runtime:  ClaudeCode,
		Instructions: `You write and fix code in the task folder. Work in small steps and run what you write.
When you change code, there are tests that show it works: the project's own, or ones you add. In your report's data, put the one command that runs them in "tests_command" (such as "go test ./..." or "python3 -m pytest -q") and the files you changed in "changed".
Your report is checked: that command is run again in the task folder and must pass. Never say it works if you haven't run it.
For a reply that changes no code, such as an explanation or a question back, leave both out.`,
		Output: Output{
			About: "A code change names the files it changed and the command that runs its tests.",
			Fields: []Field{
				{Name: "changed", About: "the files you changed or added, as paths in the task folder"},
				{Name: "tests_command", About: "one command, run in the task folder, that runs the tests and exits 0 when they pass"},
			},
		},
		Checks: []Check{
			{ID: "has-tests", Kind: Shape, About: "A code change says how to test it", When: changedCode, Run: checkHasTests},
			{ID: "tests-pass", Kind: Tool, About: "Its test command is run again and passes", When: hasTestCommand, Run: checkTestsPass},
			{ID: "did-it", Kind: Rubric, About: "The change does what was asked, and nothing else", When: changedCode,
				Run: judge(`- The change does what the person asked, as they asked it.
- It doesn't add features or edits nobody asked for.
- The report says what was changed and how it was tested, in plain words.
- Anything left undone or uncertain is said plainly.
Fail it if any of these is missing; say which.`)},
		},
		Cases: coderCases,
	}
}

func changedCode(r Report) bool { return len(strs(r.Data, "changed")) > 0 || hasTestCommand(r) }

func hasTestCommand(r Report) bool { return text(r.Data, "tests_command") != "" }

func checkHasTests(_ context.Context, _ Host, r Report) Verdict {
	if !hasTestCommand(r) {
		return Fail(`you changed code but named no test command: put the command that runs its tests in data.tests_command, adding a test if there is none`)
	}
	return Verdict{Pass: true, Ran: "names " + text(r.Data, "tests_command")}
}

func checkTestsPass(ctx context.Context, h Host, r Report) Verdict {
	cmd := text(r.Data, "tests_command")
	out, code, err := h.RunCommand(ctx, cmd)
	if errors.Is(err, ErrCannotRun) {
		return Verdict{Pass: true, Ran: "tests not run: " + err.Error()}
	}
	if err != nil {
		return Fail(fmt.Sprintf("couldn't run %q: %s", cmd, short(err.Error())))
	}
	if code != 0 {
		return Fail(fmt.Sprintf("%q failed (exit %d): %s", cmd, code, tail(out, 600)))
	}
	return Verdict{Pass: true, Ran: "ran " + cmd}
}

// tail is the end of a command's output, where failures are reported.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n:])
	}
	return s
}

var coderCases = []Case{
	{"Write a Python script that renames the JPEGs in a folder by the date in their file names (IMG_20240101_...)", "Script works on the sample names, has a test, and the test command passes."},
	{"Write a Go function that splits a bill among people by their shares, rounding to the cent, with tests", "Totals always add up to the bill; tests cover rounding; tests pass."},
	{"This function should return the median but is wrong for even lengths: def median(xs): xs=sorted(xs); return xs[len(xs)//2]. Fix it", "Fixes even-length handling, adds a test for it, tests pass, changes nothing else."},
	{"Make a script that turns a CSV of expenses (date,amount,category) into totals per category", "Handles the header, sums correctly, has a test with a small CSV, tests pass."},
	{"Explain what a goroutine leak is", "Explains clearly without changing or writing code, so no test command."},
	{"Write a shell script that backs up ~/Documents to a dated tar.gz in ~/Backups", "Script is safe with spaces in names, has a test against a temp folder, tests pass."},
	{"Add input validation to this function: def age(born_year): return 2026 - born_year", "Rejects non-integers and future years with clear errors, tests cover both, tests pass."},
	{"Write a JavaScript function that formats a number of seconds as 1h 02m 03s, with tests", "Handles zero, under a minute and over a day; tests pass."},
	{"Convert this Python 2 print statement code to Python 3: print \"hello\", name", "Converts correctly, runs it, nothing extra."},
	{"Write a small Go program that prints the five largest files under a folder", "Walks the folder, handles errors, has a test with a temp folder, tests pass."},
}
