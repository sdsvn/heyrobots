// Package router is the ADK agent that interprets admitted messages and
// proposes Routes. It records the model's tool calls and returns them; it
// never acts on them. Eval checks every Route in plain code before
// anything is stored.
package router

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"slices"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/eval"
	"heyrobots/service/internal/ids"
)

// MaxRoutes caps how many routing calls one Eval may make.
const MaxRoutes = 20

// Router runs the router agent.
type Router struct {
	// Model returns the model to use. It is called for every Eval, so a key
	// saved after start is picked up.
	Model func(ctx context.Context) (model.LLM, error)
	// Prompt is the system prompt.
	Prompt string
}

var _ eval.Router = (*Router)(nil)

// Route asks the model to route a Candidate and returns its proposals.
func (r *Router) Route(ctx context.Context, c eval.Candidate) ([]eval.Route, error) {
	llm, err := r.Model(ctx)
	if err != nil {
		return nil, fmt.Errorf("router model: %w", err)
	}
	rec := newRecorder(c)
	ag, err := newAgent(llm, r.Prompt, rec)
	if err != nil {
		return nil, err
	}
	run, err := runner.New(runner.Config{
		AppName:           "heyrobots-router",
		Agent:             ag,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		return nil, err
	}
	msg, err := render(c)
	if err != nil {
		return nil, err
	}
	// Each Eval is judged on its own: Eval holds no state between batches.
	for _, err := range run.Run(ctx, "assistant", ids.New("rs"), genai.NewContentFromText(msg, genai.RoleUser), agent.RunConfig{}) {
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rec.routes(), nil
}

func newAgent(llm model.LLM, prompt string, rec *recorder) (agent.Agent, error) {
	tools, err := rec.tools()
	if err != nil {
		return nil, err
	}
	return llmagent.New(llmagent.Config{
		Name:                     "router",
		Description:              "Routes new messages to work.",
		Model:                    llm,
		Instruction:              prompt,
		Tools:                    tools,
		DisallowTransferToParent: true,
		DisallowTransferToPeers:  true,
		GenerateContentConfig:    &genai.GenerateContentConfig{Temperature: genai.Ptr[float32](0)},
		// ADK runs a turn's tool calls concurrently. Note the order the model
		// issued them in, so Routes keep that order.
		AfterModelCallbacks: []llmagent.AfterModelCallback{rec.noteOrder},
	})
}

// DevAgent returns the router agent for ADK's development UI. Its tools
// record any ids, since there is no Candidate to check them against; it is
// for trying prompts only and is never used by the server.
func DevAgent(llm model.LLM, prompt string) (agent.Agent, error) {
	return newAgent(llm, prompt, &recorder{})
}

// render turns a Candidate into the user message. Message text is inside
// the JSON as data.
func render(c eval.Candidate) (string, error) {
	type trig struct {
		ID       string        `json:"id"`
		Role     core.Role     `json:"role"`
		Strength core.Strength `json:"strength"`
		Owned    bool          `json:"owned,omitempty"`
		// Group is set for a message in a group chat.
		Group bool `json:"group,omitempty"`
		// HandedBack says why a task returned this message; never attach it back there.
		HandedBack     string         `json:"handed_back,omitempty"`
		HandedBackFrom string         `json:"handed_back_from,omitempty"`
		Toolsets       []core.Toolset `json:"toolsets"`
		Agents         []string       `json:"agents"`
		Job            string         `json:"job,omitempty"`
		At             time.Time      `json:"at"`
		Text           string         `json:"text"`
	}
	type tick struct {
		ID      string           `json:"id"`
		Scope   string           `json:"scope"`
		Open    bool             `json:"open"`
		Stage   core.TicketStage `json:"stage"`
		Stopped bool             `json:"stopped,omitempty"`
	}
	type rule struct {
		Name string `json:"name"`
		Text string `json:"text"`
	}
	type agentView struct {
		ID       string         `json:"id"`
		Name     string         `json:"name"`
		Purpose  string         `json:"purpose"`
		Toolsets []core.Toolset `json:"toolsets,omitempty"`
		Any      bool           `json:"any_toolset,omitempty"`
	}
	type load struct {
		ID      string `json:"id"`
		Working int    `json:"working_on"`
		Waiting int    `json:"waiting"`
	}
	type hist struct {
		Actor string    `json:"from"`
		At    time.Time `json:"at"`
		Text  string    `json:"text"`
	}
	out := struct {
		Source core.SourceKind `json:"source"`
		Mode   string          `json:"accept_mode"`
		Rules  []rule          `json:"rules,omitempty"`
		// Stable parts first, so providers can cache the prefix: rules and
		// the team rarely change; triggers and loads change every time.
		Agents   []agentView `json:"agents,omitempty"`
		Triggers []trig      `json:"triggers"`
		Loads    []load      `json:"agent_load,omitempty"`
		Tickets  []tick      `json:"tickets,omitempty"`
		History  []hist      `json:"history,omitempty"`
	}{Source: c.Source, Mode: string(c.Mode)}
	for _, r := range c.Rules {
		out.Rules = append(out.Rules, rule{r.Name, r.Text})
	}
	for _, t := range c.Triggers {
		out.Triggers = append(out.Triggers, trig{ID: t.ID, Role: t.Role, Strength: t.Strength, Owned: t.Owned, Group: t.Group,
			HandedBack: t.HandedBack, HandedBackFrom: t.HandedBackFrom, Toolsets: t.Toolsets, Agents: t.Agents, Job: t.Job, At: t.At, Text: t.Text})
	}
	for _, a := range c.Agents {
		out.Agents = append(out.Agents, agentView{ID: a.ID, Name: a.Name, Purpose: a.Purpose, Toolsets: a.Toolsets, Any: a.AnyToolset})
		out.Loads = append(out.Loads, load{ID: a.ID, Working: a.Working, Waiting: a.Waiting})
	}
	for _, t := range c.Tickets {
		out.Tickets = append(out.Tickets, tick{t.ID, t.Scope, t.Open, t.Stage, t.Stopped})
	}
	for _, h := range c.History {
		out.History = append(out.History, hist{h.Actor, h.At, h.Text})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return "Route these messages.\n<candidate>\n" + string(b) + "\n</candidate>", nil
}

// recorder collects the model's routing calls. Its tools check ids against
// the Candidate so the model can correct itself, but the binding checks
// happen later in Eval.
type recorder struct {
	mu       sync.Mutex
	got      []recorded
	order    map[string]int
	triggers map[string]bool
	tickets  map[string]bool
}

type recorded struct {
	callID string
	route  eval.Route
}

func (r *recorder) noteOrder(_ agent.Context, resp *model.LLMResponse, err error) (*model.LLMResponse, error) {
	if resp == nil || resp.Content == nil {
		return nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.order == nil {
		r.order = map[string]int{}
	}
	for _, p := range resp.Content.Parts {
		if fc := p.FunctionCall; fc != nil {
			if _, seen := r.order[fc.ID]; !seen {
				r.order[fc.ID] = len(r.order)
			}
		}
	}
	return nil, nil
}

func newRecorder(c eval.Candidate) *recorder {
	r := &recorder{triggers: map[string]bool{}, tickets: map[string]bool{}}
	for _, t := range c.Triggers {
		r.triggers[t.ID] = true
	}
	for _, t := range c.Tickets {
		r.tickets[t.ID] = true
	}
	return r
}

// routes returns the recorded Routes in the order the model issued them.
func (r *recorder) routes() []eval.Route {
	r.mu.Lock()
	defer r.mu.Unlock()
	got := slices.Clone(r.got)
	slices.SortStableFunc(got, func(a, b recorded) int {
		ia, oka := r.order[a.callID]
		ib, okb := r.order[b.callID]
		switch {
		case oka && okb:
			return ia - ib
		case oka:
			return -1
		case okb:
			return 1
		}
		return 0
	})
	out := make([]eval.Route, len(got))
	for i, g := range got {
		out[i] = g.route
	}
	return out
}

// Result is what every routing tool returns to the model.
type Result struct {
	Recorded bool   `json:"recorded"`
	Error    string `json:"error,omitempty"`
}

func (r *recorder) add(callID string, route eval.Route) Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.got) >= MaxRoutes {
		return Result{Error: "too many routing calls; stop now"}
	}
	if len(route.TriggerIDs) == 0 {
		return Result{Error: "name at least one trigger id"}
	}
	for _, id := range route.TriggerIDs {
		if r.triggers != nil && !r.triggers[id] {
			return Result{Error: fmt.Sprintf("unknown trigger id %q; use an id from the candidate", id)}
		}
	}
	if (route.Action == eval.ActionAttach || route.Action == eval.ActionStop) && r.tickets != nil && !r.tickets[route.TicketID] {
		return Result{Error: fmt.Sprintf("unknown ticket id %q; use an id from the candidate's tickets", route.TicketID)}
	}
	r.got = append(r.got, recorded{callID: callID, route: route})
	return Result{Recorded: true}
}

// AcceptArgs are route_accept's arguments.
type AcceptArgs struct {
	TriggerIDs   []string `json:"trigger_ids" jsonschema:"ids of the triggers this new work answers; the first is the request"`
	Scope        string   `json:"scope" jsonschema:"one plain sentence restating what was asked, in the requester's language"`
	Toolsets     []string `json:"toolsets" jsonschema:"the fewest toolsets the work needs, from the trigger's own list"`
	MatchedRules []string `json:"matched_rules,omitempty" jsonschema:"for an inferred trigger, the names of the rules it matches"`
	Agent        string   `json:"agent,omitempty" jsonschema:"id of the agent that should lead the work, from the trigger's agents list"`
	Job          string   `json:"job,omitempty" jsonschema:"the trigger's job, copied as given, when the trigger has one"`
	Note         string   `json:"note,omitempty" jsonschema:"under 30 words to the requester confirming what will happen"`
}

// TicketArgs are route_attach's and route_stop's arguments.
type TicketArgs struct {
	TriggerIDs []string `json:"trigger_ids" jsonschema:"ids of the triggers"`
	TicketID   string   `json:"ticket_id" jsonschema:"id of a ticket from the candidate"`
	Note       string   `json:"note,omitempty" jsonschema:"under 30 words to the requester"`
}

// IgnoreArgs are route_ignore's arguments.
type IgnoreArgs struct {
	TriggerIDs []string `json:"trigger_ids" jsonschema:"ids of the triggers"`
	Reason     string   `json:"reason" jsonschema:"why no work is needed, in a few words"`
	Reply      string   `json:"reply,omitempty" jsonschema:"under 30 words back to the person when they spoke to you: a greeting, thanks or small talk; empty for anything else"`
}

func (r *recorder) tools() ([]tool.Tool, error) {
	accept, err := functiontool.New(functiontool.Config{
		Name:        "route_accept",
		Description: "Start new work for a request.",
	}, func(tc agent.Context, a AcceptArgs) (Result, error) {
		ts := make([]core.Toolset, len(a.Toolsets))
		for i, t := range a.Toolsets {
			ts[i] = core.Toolset(t)
		}
		return r.add(tc.FunctionCallID(), eval.Route{Action: eval.ActionAccept, TriggerIDs: a.TriggerIDs, Scope: a.Scope, Toolsets: ts, MatchedRules: a.MatchedRules, Agent: a.Agent, Job: a.Job, Note: a.Note}), nil
	})
	if err != nil {
		return nil, err
	}
	attach, err := functiontool.New(functiontool.Config{
		Name:        "route_attach",
		Description: "Add messages to an existing task they clearly point at.",
	}, func(tc agent.Context, a TicketArgs) (Result, error) {
		return r.add(tc.FunctionCallID(), eval.Route{Action: eval.ActionAttach, TriggerIDs: a.TriggerIDs, TicketID: a.TicketID, Note: a.Note}), nil
	})
	if err != nil {
		return nil, err
	}
	stop, err := functiontool.New(functiontool.Config{
		Name:        "route_stop",
		Description: "Stop a task because its requester or the owner asked to stop it.",
	}, func(tc agent.Context, a TicketArgs) (Result, error) {
		return r.add(tc.FunctionCallID(), eval.Route{Action: eval.ActionStop, TriggerIDs: a.TriggerIDs, TicketID: a.TicketID, Note: a.Note}), nil
	})
	if err != nil {
		return nil, err
	}
	ignore, err := functiontool.New(functiontool.Config{
		Name:        "route_ignore",
		Description: "Record that messages need no work.",
	}, func(tc agent.Context, a IgnoreArgs) (Result, error) {
		return r.add(tc.FunctionCallID(), eval.Route{Action: eval.ActionIgnore, TriggerIDs: a.TriggerIDs, Reason: a.Reason, Note: a.Reply}), nil
	})
	if err != nil {
		return nil, err
	}
	return []tool.Tool{accept, attach, stop, ignore}, nil
}

// Gemini returns a Model function that builds a Gemini model with the key
// keyFn returns, rebuilding it only when the key changes.
func Gemini(name string, keyFn func() (string, bool)) func(ctx context.Context) (model.LLM, error) {
	var (
		mu     sync.Mutex
		lastK  string
		cached model.LLM
	)
	return func(ctx context.Context) (model.LLM, error) {
		key, ok := keyFn()
		if !ok || key == "" {
			return nil, fmt.Errorf("no model API key is saved; finish setup first")
		}
		mu.Lock()
		defer mu.Unlock()
		if cached != nil && key == lastK {
			return cached, nil
		}
		m, err := gemini.NewModel(ctx, name, &genai.ClientConfig{APIKey: key, Backend: genai.BackendGeminiAPI})
		if err != nil {
			return nil, err
		}
		cached, lastK = m, key
		return m, nil
	}
}

// Metered wraps a Model function so every call's token use is recorded.
func Metered(fn func(ctx context.Context) (model.LLM, error), record func(ctx context.Context, model string, in, out, cached int64)) func(ctx context.Context) (model.LLM, error) {
	return func(ctx context.Context) (model.LLM, error) {
		m, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		return meteredLLM{LLM: m, record: record}, nil
	}
}

type meteredLLM struct {
	model.LLM
	record func(ctx context.Context, model string, in, out, cached int64)
}

func (m meteredLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		var last *genai.GenerateContentResponseUsageMetadata
		defer func() {
			// Streamed responses repeat running totals; the last one counts.
			if last != nil {
				in := int64(last.PromptTokenCount) + int64(last.ToolUsePromptTokenCount)
				out := int64(last.CandidatesTokenCount) + int64(last.ThoughtsTokenCount)
				m.record(ctx, m.Name(), in, out, int64(last.CachedContentTokenCount))
			}
		}()
		for resp, err := range m.LLM.GenerateContent(ctx, req, stream) {
			if resp != nil && resp.UsageMetadata != nil {
				last = resp.UsageMetadata
			}
			if !yield(resp, err) {
				return
			}
		}
	}
}
