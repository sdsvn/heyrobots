// Package specialists defines the agents that differ in what the server
// does for them, not only in what they are told: the tools they may reach,
// the shape their result must have, and the checks it must pass before
// anyone sees it. A specialist is a Spec compiled into the server; an
// agent row names it, and a row that names none is a generalist.
package specialists

import (
	"context"
	"slices"
	"strings"

	"heyrobots/service/internal/core"
)

// Skill is one thing a specialist is good at, in the shape of an A2A
// agent card's skill.
type Skill struct {
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples"`
}

// Card tells the router and the owner what a specialist is for.
type Card struct {
	Skills []Skill  `json:"skills"`
	NotFor []string `json:"not_for"`
}

// Runtime is how a specialist's work runs.
type Runtime string

const (
	// Loop is the built-in worker loop.
	Loop Runtime = "loop"
	// ClaudeCode runs the work in the owner's Claude Code when it is set
	// up, and in the built-in loop otherwise.
	ClaudeCode Runtime = "claude-code"
)

// Field is one part of a specialist's structured result.
type Field struct {
	Name  string `json:"name"`
	About string `json:"about"`
}

// Output is a specialist's result contract: what a ready report holds in
// its data, beside the text the person reads.
type Output struct {
	About  string  `json:"about"`
	Fields []Field `json:"fields"`
}

// Report is a ready report as the checks see it.
type Report struct {
	Summary string
	Result  string
	Data    map[string]any
	// Brief is what the task was asked to do, in the person's words.
	Brief string
	// Proposed are the titles of the changes the task proposed.
	Proposed []string
}

// Kind says how a check works.
type Kind string

const (
	// Shape checks the result's structure in code.
	Shape Kind = "shape"
	// Tool checks the result by doing something real: opening a page,
	// running the tests.
	Tool Kind = "tool"
	// Rubric asks a fresh model, with no tools and no history, to judge the
	// result against a checklist.
	Rubric Kind = "rubric"
)

// Verdict is a check's answer.
type Verdict struct {
	Pass bool
	// Reasons say what didn't pass, one fact each.
	Reasons []string
	// Ran says what the check did, for the person ("ran go test ./...").
	Ran string
}

// Fail is a failing verdict.
func Fail(reasons ...string) Verdict { return Verdict{Reasons: reasons} }

// Host is what a check may use. The executor gives it the task's own
// powers, never more.
type Host interface {
	// Fetch reads a page through the quarantined reader, as text.
	Fetch(ctx context.Context, url string) (string, error)
	// RunCommand runs a command in the task folder, when the task may run
	// commands, and returns its output and exit code.
	RunCommand(ctx context.Context, command string) (output string, code int, err error)
	// Proposed lists the titles of the changes this task proposed.
	Proposed() []string
	// AddOnToolsOn returns how many tools of each connected add-on can run.
	AddOnToolsOn() (map[string]int, error)
	// Judge asks a fresh model whether the result meets the checklist.
	Judge(ctx context.Context, checklist string, r Report) (Verdict, error)
}

// Check is one check a ready report must pass.
type Check struct {
	ID    string `json:"id"`
	About string `json:"about"`
	Kind  Kind   `json:"kind"`
	// When says whether the check applies to a report; nil means always.
	When func(Report) bool                                   `json:"-"`
	Run  func(ctx context.Context, h Host, r Report) Verdict `json:"-"`
}

// Case is one real request a specialist must handle better than General.
type Case struct {
	Request string `json:"request"`
	// Rubric is what a good answer does, for the judge.
	Rubric string `json:"rubric"`
}

// Spec defines a specialist.
type Spec struct {
	ID      string
	AgentID string
	Name    string
	Purpose string
	Color   string
	Badge   string
	Card    Card
	// Toolsets are the toolsets the specialist may be given.
	Toolsets []core.Toolset
	// Tools are the built-in tools it may call, by name. The tools every
	// agent keeps (report, memory, sharing) aren't listed.
	Tools []string
	// AppTools lets it use the tools of connected add-ons its task grants.
	AppTools bool
	Runtime  Runtime
	// Instructions are how it works, in its own words.
	Instructions string
	Output       Output
	Checks       []Check
	Cases        []Case
}

// Kept are the tools every agent keeps, specialist or not: reporting,
// handing back, its memory, the team, sharing a file, and proposing a
// rule, a setting or a standing job, each of which waits for the owner.
var Kept = []string{"report", "ack", "hand_back", "remember", "forget", "recall", "follow_up", "close_follow_up", "team", "share_file", "show_ui",
	"make_rule", "set_setting", "make_job"}

// Allows reports whether the specialist may call a built-in tool.
func (s Spec) Allows(tool string) bool {
	return slices.Contains(Kept, tool) || slices.Contains(s.Tools, tool)
}

// Applies returns the checks that apply to a report.
func (s Spec) Applies(r Report) []Check {
	var out []Check
	for _, c := range s.Checks {
		if c.When == nil || c.When(r) {
			out = append(out, c)
		}
	}
	return out
}

var all = []Spec{researcher(), coder(), it()}

// All returns the shipped specialists.
func All() []Spec { return slices.Clone(all) }

// Get returns a specialist by id.
func Get(id string) (Spec, bool) {
	for _, s := range all {
		if s.ID == id {
			return s, true
		}
	}
	return Spec{}, false
}

// list reads a data field as a list of objects.
func list(d map[string]any, key string) []map[string]any {
	raw, _ := d[key].([]any)
	var out []map[string]any
	for _, x := range raw {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// text reads a data field as trimmed text.
func text(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

// strs reads a data field as a list of strings.
func strs(d map[string]any, key string) []string {
	raw, _ := d[key].([]any)
	var out []string
	for _, x := range raw {
		if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}
