package executor

import (
	"strings"
	"testing"

	"google.golang.org/genai"
)

// An agent shows a card with a button; a UI the chat can't draw is refused
// with a reason the agent can fix.
func TestAnAgentShowsNativeUI(t *testing.T) {
	e := newEnv(t)
	good := `[{"id":"root","component":"Card","child":"col"},{"id":"col","component":"Column","children":["q","yes"]},
		{"id":"q","component":"Text","text":"Book the **7pm** table?"},{"id":"yes","component":"Button","child":"yl","action":{"event":{"name":"book"}}},
		{"id":"yl","component":"Text","text":"Book it"}]`
	e.model.steps = [][]*genai.Part{
		call("show_ui", map[string]any{"text": "Book the 7pm table?", "components": good}),
		call("show_ui", map[string]any{"text": "x", "components": `[{"id":"root","component":"Video","url":"https://x"}]`}),
		call("show_ui", map[string]any{"text": "x", "components": `[{"id":"root","component":"Image","url":"http://x"}]`}),
		call("show_ui", map[string]any{"text": "x", "components": `[{"id":"root","component":"Column","children":["gone"]}]`}),
		call("show_ui", map[string]any{"text": "x", "components": `[{"id":"top","component":"Text","text":"hi"}]`}),
		call("show_ui", map[string]any{"text": "x", "components": `[{"id":"root","component":"Text","text":"hi","variant":"h3"}]`}),
		reportCall("ready", "Showed a card with two times.", "", ""),
	}
	res := e.say(owner, "c1", "book us a table")
	e.evalAll()
	if !e.work() {
		t.Fatal("no assignment ran")
	}
	var shown int
	for _, ef := range e.view(res.ThreadIDs[0]).Effects {
		if ef.Kind == "message" && ef.TicketID != "" && strings.Contains(ef.Body, "Showed a card") {
			t.Errorf("the report repeated the card: %q", ef.Body)
		}
		if m, ok := ef.UI(); ok {
			shown++
			if ef.Body != "Book the 7pm table?" || len(m) != 2 || m[0]["createSurface"] == nil || !strings.Contains(ef.Payload, `"Book it"`) {
				t.Fatalf("ui = %q %s", ef.Body, ef.Payload)
			}
		}
	}
	if tk := e.ticket(res.ThreadIDs[0]); tk.Stage != "ready" {
		t.Fatalf("a ready report after a card needs no result: stage %q", tk.Stage)
	}
	if shown != 1 {
		t.Fatalf("shown %d surfaces, want 1", shown)
	}
	if msg, _ := e.model.results[5]["error"].(string); !strings.Contains(msg, "variant") {
		t.Errorf("a bad property should say which: %q", msg)
	}
	for i, r := range e.model.results[1:6] {
		if r["error"] == nil {
			t.Errorf("bad ui %d was accepted: %v", i+1, r)
		}
	}
}
