package jobs

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/workflow"
	"google.golang.org/genai"
)

const release = `---
name: release
description: Ship a release.
agent: ag_eng
rule: ship
toolsets: [research]
schedule: {days: weekdays, time: "16:00"}
steps:
  - {name: tests, kind: job, job: run-tests}
  - {name: notes, kind: model, instruction: Write the release notes.}
  - {name: ok, kind: ask, question: "Post them to #eng?"}
  - {name: post, kind: tool, tool: slack_post, args: {channel: eng}, retry: 2}
---
Ships whatever is on main.
`

const runTests = `---
name: run-tests
agent: ag_eng
rule: ship
steps:
  - {name: go, kind: tool, tool: run_tests}
---
`

// echo answers every model step with its instruction's last line.
type echo struct{}

func (echo) Name() string { return "echo" }
func (echo) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		sys := ""
		if req.Config != nil && req.Config.SystemInstruction != nil {
			for _, p := range req.Config.SystemInstruction.Parts {
				sys += p.Text
			}
		}
		lines := strings.Split(strings.TrimSpace(sys), "\n")
		yield(&model.LLMResponse{Content: genai.NewContentFromText("did: "+lines[len(lines)-1], genai.RoleModel), TurnComplete: true}, nil)
	}
}

type host struct {
	calls []string
	fail  map[string]int
}

func (h *host) ModelAgent(s Step) (agent.Agent, error) {
	return llmagent.New(llmagent.Config{Name: strings.ReplaceAll(s.Name, "/", "_"), Model: echo{}, Instruction: s.Instruction})
}

func (h *host) CallTool(_ context.Context, name string, args map[string]any) (map[string]any, error) {
	h.calls = append(h.calls, name)
	if h.fail[name] > 0 {
		h.fail[name]--
		return nil, errors.New(name + " is flaky")
	}
	return map[string]any{"ok": true, "args": args}, nil
}

func lib(t *testing.T, files map[string]string) Library {
	t.Helper()
	l := Library{Dir: t.TempDir()}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(l.Dir, name+".md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

// run drives one turn and returns the events, with any pause's interrupt id.
func run(t *testing.T, r *runner.Runner, content *genai.Content) (texts []string, pausedAt string) {
	t.Helper()
	for ev, err := range r.Run(context.Background(), "u", "s", content, agent.RunConfig{}) {
		if err != nil {
			t.Fatal(err)
		}
		if ev.RequestedInput != nil {
			pausedAt = ev.RequestedInput.InterruptID
		}
		if ev.Content != nil {
			for _, p := range ev.Content.Parts {
				if p.Text != "" {
					texts = append(texts, p.Text)
				}
			}
		}
	}
	return
}

func TestAJobRunsItsStepsPausesAndResumes(t *testing.T) {
	l := lib(t, map[string]string{"release": release, "run-tests": runTests})
	j, all, err := l.Get("release")
	if err != nil {
		t.Fatal(err)
	}
	if j.Schedule.Time != "16:00" || j.Steps[0].Kind != KindJob || !strings.Contains(j.Body, "main") {
		t.Fatalf("job = %+v", j)
	}
	h := &host{fail: map[string]int{"slack_post": 1}}
	ag, err := Build(j, all, h)
	if err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(runner.Config{AppName: "t", Agent: ag, SessionService: session.InMemoryService(), AutoCreateSession: true})
	if err != nil {
		t.Fatal(err)
	}

	// First turn: the sub-job's tool runs, the model step runs, then the
	// ask step pauses.
	texts, paused := run(t, r, genai.NewContentFromText("go", genai.RoleUser))
	if paused != InterruptPrefix+"ok" {
		t.Fatalf("paused at %q, texts %v", paused, texts)
	}
	if strings.Join(h.calls, ",") != "run_tests" {
		t.Fatalf("calls before the pause = %v", h.calls)
	}
	if !strings.Contains(strings.Join(texts, "\n"), "did: Write the release notes.") {
		t.Fatalf("model step didn't run: %v", texts)
	}

	// Second turn: the owner's answer resumes it; the flaky tool is retried.
	answer := &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
		ID: paused, Name: workflow.WorkflowInputFunctionCallName, Response: map[string]any{"response": "yes"},
	}}}}
	_, paused = run(t, r, answer)
	if paused != "" {
		t.Fatalf("still paused at %q", paused)
	}
	if strings.Join(h.calls, ",") != "run_tests,slack_post,slack_post" {
		t.Fatalf("calls = %v", h.calls)
	}
}

func TestTheLibraryRefusesBadJobs(t *testing.T) {
	l := lib(t, map[string]string{
		"a":      "---\nname: a\nagent: x\nrule: r\nsteps: [{name: s, kind: job, job: b}]\n---\n",
		"b":      "---\nname: b\nagent: x\nrule: r\nsteps: [{name: s, kind: job, job: a}]\n---\n",
		"orphan": "---\nname: orphan\nagent: x\nrule: r\nsteps: [{name: s, kind: job, job: nope}]\n---\n",
		"fine":   "---\nname: fine\nagent: x\nrule: r\nsteps: [{name: s, kind: tool, tool: t}]\n---\n",
	})
	all, errs := l.List()
	if len(all) != 1 || all["fine"].Name != "fine" {
		t.Fatalf("loaded %v, errs %v", Names(all), errs)
	}
	joined := fmt.Sprint(errs)
	for _, want := range []string{"calls itself", "doesn't exist"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors lack %q: %v", want, errs)
		}
	}
	if err := l.Save("fine", []byte("---\nname: fine\nagent: x\nrule: r\nsteps: [{name: s, kind: job, job: fine}]\n---\n")); err == nil {
		t.Fatal("saved a job that runs itself")
	}
	if err := l.Save("caller", []byte("---\nname: caller\nagent: x\nrule: r\nsteps: [{name: s, kind: job, job: fine}]\n---\n")); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete("fine"); err == nil || !strings.Contains(err.Error(), "caller") {
		t.Fatalf("deleted a job in use: %v", err)
	}
	for name, body := range map[string]string{
		"no-rule":    "---\nname: no-rule\nagent: x\nsteps: [{name: s, kind: tool, tool: t}]\n---\n",
		"bad-kind":   "---\nname: bad-kind\nagent: x\nrule: r\nsteps: [{name: s, kind: dance}]\n---\n",
		"no-trigger": "---\nname: no-trigger\nagent: x\nrule: r\ntrigger: {tool: linear/issues}\nsteps: [{name: s, kind: tool, tool: t}]\n---\n",
	} {
		if _, err := Parse(name, []byte(body)); err == nil {
			t.Errorf("%s parsed", name)
		}
	}
}
