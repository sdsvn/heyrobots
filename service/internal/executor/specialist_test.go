package executor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"google.golang.org/genai"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/specialists"
	"heyrobots/service/internal/store"
)

func (e *env) addSpecialistResearcher() *httptest.Server {
	e.t.Helper()
	err := e.store.SaveAgent(store.Agent{ID: "ag_researcher", Name: "Researcher", Purpose: "Finds things out.",
		Toolsets: `["research","writing"]`, Skills: `[]`, Capacity: 1, Enabled: true, Specialist: "researcher",
		CreatedAt: e.clock, UpdatedAt: e.clock})
	if err != nil {
		e.t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("HubSpot Starter costs $20 per seat, billed monthly."))
	}))
	e.t.Cleanup(srv.Close)
	e.exec.Web = srv.Client()
	e.eval.Router = agentRouter{agent: "ag_researcher", toolsets: []core.Toolset{core.ToolsetResearch, core.ToolsetWriting}}
	return srv
}

var longAnswer = strings.Repeat("HubSpot is the CRM to pick for a small team that lives in Gmail. ", 6)

func researchReport(url, quote string) []*genai.Part {
	return call("report", map[string]any{"state": "ready", "summary": "Compared CRMs.", "result": longAnswer,
		"data": map[string]any{"claims": []any{map[string]any{"claim": "HubSpot Starter is $20 a seat", "url": url, "quote": quote}}}})
}

func TestASpecialistsReportIsCheckedBeforeAnyoneSeesIt(t *testing.T) {
	e := newEnv(t)
	srv := e.addSpecialistResearcher()
	e.model.steps = [][]*genai.Part{
		// No claims at all: the shape check refuses it.
		call("report", map[string]any{"state": "ready", "summary": "Compared CRMs.", "result": longAnswer}),
		// A quote the page doesn't hold: the source check refuses it.
		researchReport(srv.URL+"/crm", "HubSpot is free forever"),
		// The real passage: it passes, and the judge is asked.
		researchReport(srv.URL+"/crm", "hubspot starter costs $20 per seat"),
		{{Text: `{"pass": true, "reasons": []}`}},
	}
	res := e.say(owner, "c1", "compare three CRMs for a five-person team")
	e.evalAll()
	if !e.work() {
		t.Fatal("no assignment ran")
	}
	tools := e.model.tools[0]
	if !slices.Contains(tools, "web_fetch") || !slices.Contains(tools, "report") || !slices.Contains(tools, "remember") || slices.Contains(tools, "make_rule") {
		t.Fatalf("a specialist gets its own tools and the ones every agent keeps, nothing else: %v", tools)
	}
	if !strings.Contains(e.model.systems[0], "How your work is checked") {
		t.Fatal("the specialist is told how its work is checked")
	}
	var refusals []string
	for _, r := range e.model.results {
		if msg, _ := r["error"].(string); strings.Contains(msg, "didn't pass its checks") {
			refusals = append(refusals, msg)
		}
	}
	if len(refusals) != 2 || !strings.Contains(refusals[0], "lists no claims") || !strings.Contains(refusals[1], "doesn't say") {
		t.Fatalf("two refusals, with their reasons: %q", refusals)
	}
	msgs := messages(e.view(res.ThreadIDs[0]))
	last := msgs[len(msgs)-1]
	if !strings.Contains(last, "✓ Checked:") || !strings.Contains(last, "found every quote") || !strings.Contains(last, "judged against its checklist") {
		t.Fatalf("the reply says what was checked, in the host's words: %q", last)
	}
	o := e.outcomes("ag_researcher")
	if len(o) != 1 || !o[0].Checked || o[0].FirstTry || o[0].Refusals != 2 || o[0].Failed != "null" {
		t.Fatalf("outcome = %+v", o)
	}
}

func TestAfterTwoRefusalsTheHostSaysWhatDidntPass(t *testing.T) {
	e := newEnv(t)
	srv := e.addSpecialistResearcher()
	bad := researchReport(srv.URL+"/crm", "HubSpot is free forever")
	other := researchReport(srv.URL+"/crm", "HubSpot is free for teams")
	e.model.steps = [][]*genai.Part{bad, other, bad}
	res := e.say(owner, "c1", "compare three CRMs")
	e.evalAll()
	e.work()
	msgs := messages(e.view(res.ThreadIDs[0]))
	last := msgs[len(msgs)-1]
	if !strings.Contains(last, "⚠ Didn't pass its checks:") || !strings.Contains(last, "doesn't say") || strings.Contains(last, "✓") {
		t.Fatalf("the reply goes out saying plainly what didn't pass: %q", last)
	}
	o := e.outcomes("ag_researcher")
	if len(o) != 1 || o[0].FirstTry || o[0].Refusals != 3 {
		t.Fatalf("outcome = %+v", o)
	}
	var failed []string
	_ = json.Unmarshal([]byte(o[0].Failed), &failed)
	if len(failed) != 1 {
		t.Fatalf("failed = %q", o[0].Failed)
	}
}

func TestTheSameFailureTwiceStopsEarly(t *testing.T) {
	e := newEnv(t)
	srv := e.addSpecialistResearcher()
	bad := researchReport(srv.URL+"/crm", "HubSpot is free forever")
	e.model.steps = [][]*genai.Part{bad, bad}
	res := e.say(owner, "c1", "compare three CRMs")
	e.evalAll()
	e.work()
	msgs := messages(e.view(res.ThreadIDs[0]))
	if last := msgs[len(msgs)-1]; !strings.Contains(last, "⚠ Didn't pass its checks:") {
		t.Fatalf("the same reasons twice end the refusals: %q", last)
	}
}

func TestAQuickReplyFromASpecialistIsntChecked(t *testing.T) {
	e := newEnv(t)
	e.addSpecialistResearcher()
	e.model.steps = [][]*genai.Part{reportCall("ready", "Said thanks.", "You're welcome!", "")}
	res := e.say(owner, "c1", "thanks!")
	e.evalAll()
	e.work()
	msgs := messages(e.view(res.ThreadIDs[0]))
	if last := msgs[len(msgs)-1]; strings.Contains(last, "Checked") || strings.Contains(last, "Didn't pass") {
		t.Fatalf("a thank-you goes out as is: %q", last)
	}
}

func (e *env) outcomes(agentID string) []store.AgentOutcome {
	e.t.Helper()
	var out []store.AgentOutcome
	if err := e.store.Tx(func(tx *store.Tx) error {
		var err error
		out, err = tx.AgentOutcomes(agentID)
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func TestEveryToolASpecNamesExists(t *testing.T) {
	e := newEnv(t)
	e.setPolicy(func(p *policy.Policy) { p.Toolsets = append(p.Toolsets, core.ToolsetSetup) })
	e.eval.Router = agentRouter{agent: core.GeneralAgent, toolsets: []core.Toolset{core.ToolsetResearch, core.ToolsetWriting, core.ToolsetSetup}}
	e.model.steps = [][]*genai.Part{reportCall("ready", "Looked.", "Nothing to do.", "")}
	e.say(owner, "c1", "look around")
	e.evalAll()
	if !e.work() {
		t.Fatal("no assignment ran")
	}
	have := e.model.tools[0]
	for _, s := range specialists.All() {
		for _, name := range append(slices.Clone(s.Tools), specialists.Kept...) {
			if !slices.Contains(have, name) {
				t.Errorf("%s names a tool that doesn't exist: %s (have %v)", s.ID, name, have)
			}
		}
	}
}
