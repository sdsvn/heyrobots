package executor

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/genai"

	"heyrobots/service/internal/core"
)

// An agent asked what the team is doing sees its teammates' open tasks.
func TestAnAgentSeesWhatItsTeammatesAreDoing(t *testing.T) {
	e := newEnv(t)
	e.addResearcher()
	e.eval.Router = agentRouter{agent: "ag_research", toolsets: []core.Toolset{core.ToolsetResearch}}
	e.model.steps = [][]*genai.Part{reportCall("waiting", "Need dates.", "", "Which dates?")}
	e.say(owner, "c1", "find flights to Lisbon")
	e.evalAll()
	e.work()

	e.eval.Router = agentRouter{agent: "ag_general"}
	e.model.steps = [][]*genai.Part{call("team", map[string]any{}), reportCall("ready", "Told them.", "Researcher waits on dates.", "")}
	e.model.n = 0
	e.say(owner, "c2", "what is the team doing?")
	e.evalAll()
	e.work()
	if len(e.model.results) == 0 {
		t.Fatal("the team tool gave no answer")
	}
	b, _ := json.Marshal(e.model.results[0])
	got := string(b)
	for _, want := range []string{"Researcher", "find flights to Lisbon", "awaiting_answer", `"you":true`} {
		if !strings.Contains(got, want) {
			t.Errorf("team = %s, lacks %q", got, want)
		}
	}
	if strings.Contains(got, "what is the team doing") {
		t.Error("the asking task listed itself")
	}
}
