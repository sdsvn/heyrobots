package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/eval"
	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/jobs"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/sources/schedule"
	"heyrobots/service/internal/store"
)

// jobRouter accepts a job trigger as its job, the way the prompt tells the
// real router to.
type jobRouter struct{ agent string }

func (j jobRouter) Route(_ context.Context, c eval.Candidate) ([]eval.Route, error) {
	var out []eval.Route
	for _, t := range c.Triggers {
		out = append(out, eval.Route{Action: eval.ActionAccept, TriggerIDs: []string{t.ID}, Scope: "run " + t.Job,
			Toolsets: []core.Toolset{core.ToolsetWriting}, MatchedRules: []string{"brief"}, Agent: j.agent, Job: t.Job})
	}
	return out, nil
}

const briefJob = `---
name: brief
description: The morning brief.
agent: ag_general
rule: brief
toolsets: [writing]
schedule: {time: "08:00"}
steps:
  - {name: draft, kind: model, instruction: Draft the brief.}
  - {name: check, kind: ask, question: "Send it?"}
  - {name: save, kind: tool, tool: write_file, args: {path: brief.md, content: sent}}
---
What happened overnight.
`

func (e *env) withJobs(t *testing.T, files map[string]string) *schedule.Source {
	t.Helper()
	lib := &jobs.Library{Dir: t.TempDir()}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(lib.Dir, name+".md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e.exec.Jobs = lib
	e.eval.Jobs = func(name string) (eval.JobSpec, error) {
		j, _, err := lib.Get(name)
		if err != nil {
			return eval.JobSpec{}, err
		}
		return eval.JobSpec{Agent: j.Agent, Rule: j.Rule, Toolsets: j.Toolsets}, nil
	}
	e.setPolicy(func(p *policy.Policy) {
		p.Mode = policy.ModeAuto
		p.Rules = []policy.Rule{{Name: "brief", Text: "Write the morning brief.", Enabled: true}}
		p.Limits.Timezone = "UTC"
	})
	return &schedule.Source{Store: e.store, Intake: e.intake, Log: e.exec.Log, Now: func() time.Time { return e.clock }, Jobs: lib}
}

func TestAScheduledJobRunsPausesAndResumes(t *testing.T) {
	e := newEnv(t)
	src := e.withJobs(t, map[string]string{"brief": briefJob})
	e.eval.Router = jobRouter{agent: core.GeneralAgent}
	// The job was first seen before 08:00, so this morning's run is due.
	e.clock = time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC)
	if err := src.Fire(); err != nil {
		t.Fatal(err)
	}
	e.clock = time.Date(2026, 11, 11, 8, 5, 0, 0, time.UTC)
	if err := src.Fire(); err != nil {
		t.Fatal(err)
	}
	e.evalAll()

	// The task runs as the job: the model step, then the ask step pauses.
	e.model.steps = [][]*genai.Part{{{Text: "Drafted: three things overnight."}}}
	if !e.work() {
		t.Fatal("no assignment ran")
	}
	// The firing's conversation is keyed by the job and the time it fired.
	var th store.Thread
	if err := e.store.Tx(func(tx *store.Tx) error {
		var err error
		th, err = tx.ThreadByKey(string(core.SourceSchedule), "job:brief@2026-11-11T08:00:00Z")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	tk := e.ticket(th.ID)
	m, _ := tk.DecodeMandate()
	if m.Job != "brief" || tk.Stage != string(core.StageQuestion) {
		t.Fatalf("ticket %+v mandate %+v", tk, m)
	}
	msgs := messages(e.view(tk.OriginThreadID))
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1], "Send it?") {
		t.Fatalf("messages = %q", msgs)
	}
	if _, err := os.Stat(filepath.Join(e.exec.Workspaces, tk.ID, "brief.md")); err == nil {
		t.Fatal("the tool step ran before the owner answered")
	}

	// The owner's reply, in the app on that conversation, resumes the job
	// at the paused step.
	if _, err := e.intake.Ingest(intake.Message{
		Trigger:   core.Trigger{DeliveryID: "reply-1", Source: core.SourceApp, Actor: owner, Authenticity: core.Verified, Addressed: true, Text: "yes", At: e.clock},
		ThreadIDs: []string{tk.OriginThreadID}, CreatedBy: owner,
	}); err != nil {
		t.Fatal(err)
	}
	e.evalAll()
	e.model.n = 0
	if !e.work() {
		t.Fatal("the reply didn't start a follow-up")
	}
	tk = e.ticket(tk.OriginThreadID)
	if tk.Open || tk.Stage != string(core.StageReady) {
		t.Fatalf("after the reply: %+v", tk)
	}
	if b, err := os.ReadFile(filepath.Join(e.exec.Workspaces, tk.ID, "brief.md")); err != nil || string(b) != "sent" {
		t.Fatalf("tool step: %q %v", b, err)
	}
	// Firing again the same morning starts nothing.
	if err := src.Fire(); err != nil {
		t.Fatal(err)
	}
	e.evalAll()
	if e.work() {
		t.Fatal("the job ran twice")
	}
}

func TestAJobRunsOnlyAsItsFileSays(t *testing.T) {
	e := newEnv(t)
	src := e.withJobs(t, map[string]string{"brief": briefJob})
	e.addResearcher()
	// The router names another agent for the job.
	e.eval.Router = jobRouter{agent: "ag_research"}
	e.clock = time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC)
	_ = src.Fire()
	e.clock = time.Date(2026, 11, 11, 8, 5, 0, 0, time.UTC)
	_ = src.Fire()
	e.evalAll()
	if e.work() {
		t.Fatal("a job ran as the wrong agent")
	}
}
