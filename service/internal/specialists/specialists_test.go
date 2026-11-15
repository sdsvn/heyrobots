package specialists

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeHost struct {
	pages    map[string]string
	ran      []string
	code     int
	out      string
	runErr   error
	proposed []string
	on       map[string]int
}

func (h *fakeHost) Fetch(_ context.Context, u string) (string, error) {
	p, ok := h.pages[u]
	if !ok {
		return "", errors.New("the site answered 404 Not Found")
	}
	return p, nil
}

func (h *fakeHost) RunCommand(_ context.Context, cmd string) (string, int, error) {
	h.ran = append(h.ran, cmd)
	return h.out, h.code, h.runErr
}

func (h *fakeHost) Proposed() []string                    { return h.proposed }
func (h *fakeHost) AddOnToolsOn() (map[string]int, error) { return h.on, nil }
func (h *fakeHost) Judge(context.Context, string, Report) (Verdict, error) {
	return Verdict{Pass: true}, nil
}

func claims(cs ...[3]string) map[string]any {
	var l []any
	for _, c := range cs {
		l = append(l, map[string]any{"claim": c[0], "url": c[1], "quote": c[2]})
	}
	return map[string]any{"claims": l}
}

func TestTheResearchersSourcesMustSayWhatIsQuoted(t *testing.T) {
	h := &fakeHost{pages: map[string]string{
		"https://a.example/crm": "Pricing:  HubSpot Starter costs $20 per seat—billed monthly.",
	}}
	ok := Report{Data: claims([3]string{"HubSpot is $20 a seat", "https://a.example/crm", "hubspot starter costs $20 per seat-billed monthly"})}
	if v := checkSourcesSayIt(context.Background(), h, ok); !v.Pass {
		t.Fatalf("a quote the page holds, up to case, spaces and dashes, passes: %+v", v)
	}
	bad := Report{Data: claims(
		[3]string{"HubSpot is free", "https://a.example/crm", "HubSpot is free for everyone forever"},
		[3]string{"Pipedrive is $14", "https://gone.example/", "Pipedrive Essential is $14"},
	)}
	v := checkSourcesSayIt(context.Background(), h, bad)
	if v.Pass || len(v.Reasons) != 2 || !strings.Contains(v.Reasons[0], "doesn't say") || !strings.Contains(v.Reasons[1], "couldn't open") {
		t.Fatalf("a page that doesn't say it and one that won't open are told apart: %+v", v)
	}
}

func TestAResearchAnswerNeedsItsClaimsSourced(t *testing.T) {
	r, _ := Get("researcher")
	long := Report{Result: strings.Repeat("HubSpot is the best CRM. ", 20)}
	if len(r.Applies(long)) == 0 {
		t.Fatal("a long answer with no claims is still checked")
	}
	if v := checkSourced(context.Background(), nil, long); v.Pass {
		t.Fatal("an answer with no claims fails")
	}
	if got := r.Applies(Report{Result: "You're welcome!"}); len(got) != 0 {
		t.Fatalf("a quick reply isn't checked: %v", got)
	}
	noQuote := Report{Data: claims([3]string{"X costs $5", "https://x.example", ""})}
	if v := checkSourced(context.Background(), nil, noQuote); v.Pass || !strings.Contains(v.Reasons[0], "no passage") {
		t.Fatalf("a claim with no quote fails: %+v", v)
	}
}

func TestTheCodersTestsAreRunAgain(t *testing.T) {
	r := Report{Data: map[string]any{"changed": []any{"split.go"}, "tests_command": "go test ./..."}}
	h := &fakeHost{code: 1, out: "--- FAIL: TestSplit\nwant 33.34"}
	v := checkTestsPass(context.Background(), h, r)
	if v.Pass || len(h.ran) != 1 || h.ran[0] != "go test ./..." || !strings.Contains(v.Reasons[0], "want 33.34") {
		t.Fatalf("failing tests fail the report with their output: %+v ran %v", v, h.ran)
	}
	h = &fakeHost{}
	if v := checkTestsPass(context.Background(), h, r); !v.Pass || v.Ran != "ran go test ./..." {
		t.Fatalf("passing tests pass: %+v", v)
	}
	h = &fakeHost{runErr: ErrCannotRun}
	if v := checkTestsPass(context.Background(), h, r); !v.Pass || !strings.Contains(v.Ran, "not run") {
		t.Fatalf("a task that may not run commands says the tests weren't run: %+v", v)
	}
	if v := checkHasTests(context.Background(), nil, Report{Data: map[string]any{"changed": []any{"a.go"}}}); v.Pass {
		t.Fatal("a change without a test command fails")
	}
}

func TestITListsExactlyWhatItProposed(t *testing.T) {
	h := &fakeHost{proposed: []string{"Connect Linear", "Add the skill pptx"}}
	if v := checkProposals(context.Background(), h, Report{Data: map[string]any{"proposed": []any{"Connect Linear", "Add the skill pptx"}}}); !v.Pass {
		t.Fatalf("matching lists pass: %+v", v)
	}
	v := checkProposals(context.Background(), h, Report{Data: map[string]any{"proposed": []any{"Connect Linear", "Connect Notion"}}})
	if v.Pass || len(v.Reasons) != 2 {
		t.Fatalf("a made-up proposal and a missing one both fail: %+v", v)
	}
	h.on = map[string]int{"linear": 0, "github": 12}
	v = checkReady(context.Background(), h, Report{Data: map[string]any{"ready": []any{"github", "linear", "notion"}}})
	if v.Pass || len(v.Reasons) != 2 {
		t.Fatalf("an add-on with no tools on and one not connected aren't ready: %+v", v)
	}
}

func TestEverySpecIsComplete(t *testing.T) {
	ids := map[string]bool{}
	for _, s := range All() {
		if ids[s.ID] || ids[s.AgentID] {
			t.Fatalf("%s: duplicate id", s.ID)
		}
		ids[s.ID], ids[s.AgentID] = true, true
		if s.Name == "" || s.Purpose == "" || s.Instructions == "" || len(s.Card.Skills) == 0 || len(s.Card.NotFor) == 0 || len(s.Checks) == 0 || len(s.Cases) < 8 {
			t.Fatalf("%s is missing its card, checks or cases", s.ID)
		}
		for _, sk := range s.Card.Skills {
			if len(sk.Examples) == 0 {
				t.Fatalf("%s: a skill without examples", s.ID)
			}
		}
		for _, c := range s.Checks {
			if c.Run == nil || c.ID == "" || c.About == "" {
				t.Fatalf("%s: check %q is incomplete", s.ID, c.ID)
			}
		}
	}
}
