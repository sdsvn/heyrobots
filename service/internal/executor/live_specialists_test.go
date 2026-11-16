//go:build live

// Does a specialist beat General? Each of its cases runs twice with the
// same real model, Claude Code over ACP signed in as the owner: once on
// the specialist and once on General. A fresh judge grades each reply
// against the case's rubric. The specialist ships only while it wins.
// It spends the owner's model use and, for the Coder, runs commands on
// this machine. Run with
//
//	LIVE_SPECIALIST=researcher LIVE_CASES=4 go test -tags live -run TestLiveSpecialistBeatsGeneral -v -timeout 120m ./internal/executor
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"

	"heyrobots/service/internal/acp"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/models"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/quarantine"
	"heyrobots/service/internal/specialists"
	"heyrobots/service/internal/store"
)

func TestLiveSpecialistBeatsGeneral(t *testing.T) {
	spec, ok := specialists.Get(os.Getenv("LIVE_SPECIALIST"))
	if !ok {
		t.Skip("set LIVE_SPECIALIST to researcher, coder or it")
	}
	cases := spec.Cases
	if n, err := strconv.Atoi(os.Getenv("LIVE_CASES")); err == nil && n > 0 && n < len(cases) {
		cases = cases[:n]
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := &acp.Worker{Log: log, Model: os.Getenv("LIVE_MODEL")}
	var judge model.LLM = models.ClaudeCodeModel{Ask: w.Ask, Search: w.Search}

	type result struct {
		pass    bool
		why     string
		elapsed time.Duration
	}
	try := func(agentID string, c specialists.Case) result {
		e := newEnv(t)
		e.exec.ACP = w
		e.exec.UsesACP = func(store.Agent) bool { return true }
		e.exec.Model = func(context.Context) (model.LLM, error) { return judge, nil }
		e.exec.Reader = &quarantine.Reader{Model: func(context.Context) (model.LLM, error) { return judge, nil }}
		e.exec.Web = &http.Client{Timeout: 30 * time.Second}
		e.exec.RunLimit = 15 * time.Minute
		e.setPolicy(func(p *policy.Policy) { p.Toolsets = append(p.Toolsets, spec.Toolsets...) })
		if agentID != core.GeneralAgent {
			ts, _ := json.Marshal(spec.Toolsets)
			if err := e.store.SaveAgent(store.Agent{ID: spec.AgentID, Name: spec.Name, Purpose: spec.Purpose, Toolsets: string(ts), Skills: `[]`,
				Capacity: 1, Enabled: true, Specialist: spec.ID, CreatedAt: e.clock, UpdatedAt: e.clock}); err != nil {
				t.Fatal(err)
			}
		}
		e.eval.Router = agentRouter{agent: agentID, toolsets: spec.Toolsets}
		start := time.Now()
		res := e.say(owner, "c1", c.Request)
		e.evalAll()
		e.work()
		msgs := messages(e.view(res.ThreadIDs[0]))
		reply := strings.Join(msgs, "\n\n")
		v, err := (&run{e: e.exec, agent: store.Agent{}}).judgeCase(context.Background(), c, reply)
		if err != nil {
			return result{why: "judge failed: " + err.Error(), elapsed: time.Since(start)}
		}
		return result{pass: v.Pass, why: strings.Join(v.Reasons, "; "), elapsed: time.Since(start)}
	}

	var sp, gp int
	for i, c := range cases {
		s, g := try(spec.AgentID, c), try(core.GeneralAgent, c)
		if s.pass {
			sp++
		}
		if g.pass {
			gp++
		}
		t.Logf("case %d %q\n  %s: pass=%v %s (%s)\n  General: pass=%v %s (%s)", i+1, c.Request, spec.Name, s.pass, s.why, s.elapsed.Round(time.Second), g.pass, g.why, g.elapsed.Round(time.Second))
	}
	t.Logf("%s passed %d of %d; General passed %d of %d", spec.Name, sp, len(cases), gp, len(cases))
	if sp <= gp {
		t.Errorf("%s doesn't beat General on its cases (%d vs %d): it shouldn't ship as a specialist", spec.Name, sp, gp)
	}
}

// judgeCase grades a reply against a case's rubric with a fresh call.
func (r *run) judgeCase(ctx context.Context, c specialists.Case, reply string) (specialists.Verdict, error) {
	r.e.init()
	return r.Judge(ctx, fmt.Sprintf("A good answer to this request does this: %s", c.Rubric), specialists.Report{Brief: c.Request, Result: reply})
}
