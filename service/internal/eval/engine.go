package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/gate"
	"heyrobots/service/internal/ids"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/telemetry"
)

// Defaults from the brief.
const (
	// Deadline is how long one Eval may take.
	Deadline = 10 * time.Minute
	// MaxAttempts is how many times a batch is tried before it is recorded
	// as unavailable: two retries, then the third failure records it.
	MaxAttempts = 3
	// MaxNoteWords caps the router's notes.
	MaxNoteWords = 30
	historyLen   = 20
)

// Engine runs Evals.
// JobSpec is what Eval checks a job's Mandate against.
type JobSpec struct {
	Agent    string
	Rule     string
	Toolsets []core.Toolset
}

type Engine struct {
	Store  *store.Store
	Router Router
	Log    *slog.Logger
	Now    func() time.Time
	// Lease is how long an Eval holds its Thread. Defaults to Deadline.
	Lease time.Duration
	// Workers is how many Evals run at once, each on its own Thread.
	Workers int
	// OnDecided is called after an Eval stores its decisions.
	OnDecided func(threadID string)
	// Jobs looks up a standing job by name, to check a job's Mandate.
	Jobs func(name string) (JobSpec, error)
	// Available reports whether another machine's agent can take work now.
	Available func(a store.Agent) bool
	// HandOff passes accepted work to an agent on another machine, in the
	// same transaction as the Decision.
	HandOff func(tx *store.Tx, a store.Agent, t store.Trigger, threadID string) error

	once sync.Once
	kick chan struct{}
}

func (e *Engine) init() {
	e.once.Do(func() {
		e.kick = make(chan struct{}, 1)
		if e.Lease == 0 {
			e.Lease = Deadline
		}
		if e.Workers == 0 {
			e.Workers = 2
		}
		if e.Now == nil {
			e.Now = time.Now
		}
	})
}

// Kick wakes the workers after a new Trigger arrives.
func (e *Engine) Kick() {
	e.init()
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

// Run evaluates Threads until ctx ends.
func (e *Engine) Run(ctx context.Context) {
	e.init()
	var wg sync.WaitGroup
	for range e.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tick := time.NewTicker(5 * time.Second)
			defer tick.Stop()
			for ctx.Err() == nil {
				did, err := e.RunOnce(ctx)
				if err != nil {
					e.Log.Error("eval", "err", err)
				}
				if did {
					continue
				}
				select {
				case <-ctx.Done():
				case <-e.kick:
				case <-tick.C:
				}
			}
		}()
	}
	wg.Wait()
}

type judged struct {
	trigger   store.Trigger
	input     gate.Input
	admission gate.Admission
	// chat is, in a direct chat or a group, the agents this message's work
	// may go to; nil elsewhere.
	chat []string
}

// RunOnce claims one Thread with waiting Triggers and evaluates its batch.
// It reports whether it found one.
func (e *Engine) RunOnce(ctx context.Context) (bool, error) {
	e.init()
	evalID := ids.New("ev")
	now := e.Now().UTC()
	var (
		th      store.Thread
		batch   []store.Trigger
		pol     policy.Policy
		tickets []store.Ticket
		history []store.Trigger
		chat    []string
		isChat  bool
	)
	err := e.Store.Tx(func(tx *store.Tx) error {
		var err error
		if th, batch, err = tx.ClaimEval(evalID, now, e.Lease); err != nil {
			return err
		}
		if pol, err = tx.Policy(); err != nil {
			return err
		}
		if tickets, _, err = tx.SubscribedTickets(th.ID); err != nil {
			return err
		}
		if history, err = tx.RecentTriggers(th.ID, historyLen+len(batch)); err != nil {
			return err
		}
		chat, isChat, err = tx.ChatAgents(th)
		if errors.Is(err, store.ErrNotFound) {
			// The chat's group is gone: nobody may lead its work.
			chat, isChat, err = nil, true, nil
		}
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if th.EvalFailures >= MaxAttempts {
		// Earlier attempts ran out their lease, for example in a crash.
		return true, e.unavailable(th, evalID, batch, pol, "eval did not finish after repeated attempts")
	}

	ctx = telemetry.Share(ctx, telemetry.SessionID.String(th.ID))
	ctx, span := telemetry.Tracer().Start(ctx, "heyrobots.eval", trace.WithAttributes(telemetry.ObservationType.String("chain"),
		telemetry.ThreadID.String(th.ID), telemetry.Source.String(th.Source), attribute.Int("heyrobots.batch", len(batch)),
	))
	defer span.End()

	hasTicket := slices.ContainsFunc(tickets, func(t store.Ticket) bool { return t.StoppedBy == "" })
	var judgedAll []judged
	for _, t := range batch {
		in := gate.Input{Trigger: t.Core([]string{th.Key}), ThreadHasTicket: hasTicket}
		judgedAll = append(judgedAll, judged{trigger: t, input: in, admission: gate.Admit(pol, in)})
	}

	team, err := e.team()
	if err != nil {
		return false, err
	}
	for i := range judgedAll {
		switch {
		case judgedAll[i].trigger.LeadAgent != "":
			// Another machine's lead already chose this agent.
			judgedAll[i].chat = []string{judgedAll[i].trigger.LeadAgent}
		case isChat:
			judgedAll[i].chat = chatLead(chat, team, judgedAll[i].trigger.Text)
		}
	}
	cand := e.candidate(th, pol, judgedAll, tickets, history)
	cand.Agents = team
	enabled := make([]string, 0, len(team))
	for _, a := range team {
		enabled = append(enabled, a.ID)
	}
	for i := range cand.Triggers {
		cand.Triggers[i].Agents = allowedAgents(pol, cand.Triggers[i], enabled, judgedAll)
		led := slices.ContainsFunc(judgedAll, func(j judged) bool { return j.trigger.ID == cand.Triggers[i].ID && j.trigger.LeadAgent != "" })
		if isChat || led {
			// A chat's lead is fixed, so the router may only pick toolsets
			// that lead can use; offering more just ends in a refusal.
			cand.Triggers[i].Toolsets = withinAgents(cand.Triggers[i].Toolsets, cand.Triggers[i].Agents, team)
		}
	}
	// A person talking to one agent, or work another machine already
	// handed to one, goes straight to that agent: there is nothing to route.
	routes, rest := direct(cand, judgedAll)
	if len(rest.Triggers) > 0 {
		var more []Route
		rctx, cancel := context.WithTimeout(ctx, e.Lease-e.Lease/20)
		more, err = e.Router.Route(rctx, rest)
		routes = append(routes, more...)
		cancel()
		if err != nil {
			telemetry.Fail(span, err)
			e.Log.Warn("router failed", "thread", th.ID, "err", err)
			return true, e.fail(th, evalID, batch, pol, err)
		}
	}
	span.SetAttributes(attribute.Int("heyrobots.routes", len(routes)))
	return true, e.apply(ctx, th, evalID, pol, judgedAll, routes)
}

func (e *Engine) candidate(th store.Thread, pol policy.Policy, js []judged, tickets []store.Ticket, history []store.Trigger) Candidate {
	c := Candidate{ThreadID: th.ID, Source: core.SourceKind(th.Source), Mode: pol.Mode}
	for _, r := range pol.Rules {
		if r.Enabled {
			c.Rules = append(c.Rules, r)
		}
	}
	inBatch := map[string]bool{}
	for _, j := range js {
		inBatch[j.trigger.ID] = true
		if j.admission.Strength == core.NotAdmitted {
			continue
		}
		// Proactive work needs a matching enabled rule. With none enabled,
		// a new candidate can't be accepted, so the router isn't asked:
		// otherwise every email in a connected inbox would cost a model
		// call. A follow-up on a task's thread still goes to the router,
		// which may only attach it or stop.
		if j.admission.Strength == core.Inferred && len(c.Rules) == 0 && !j.admission.FollowUp {
			continue
		}
		c.Triggers = append(c.Triggers, CandidateTrigger{
			ID: j.trigger.ID, Strength: j.admission.Strength, Role: j.admission.Role, Job: jobOf(j.trigger),
			Actor: j.input.Trigger.Actor.String(), Text: j.trigger.Text, At: j.trigger.At,
			Owned: j.trigger.Owned, Toolsets: allowedToolsets(pol, j.admission),
			HandedBack: j.trigger.HandbackReason, HandedBackFrom: j.trigger.HandedBackFrom,
		})
	}
	for _, t := range tickets {
		m, _ := t.DecodeMandate()
		c.Tickets = append(c.Tickets, TicketSummary{ID: t.ID, Scope: m.Scope, Open: t.Open, Stage: core.TicketStage(t.Stage), Stopped: t.StoppedBy != ""})
	}
	for _, t := range history {
		if inBatch[t.ID] {
			continue
		}
		c.History = append(c.History, HistoryItem{Actor: t.ActorSource + ":" + t.ActorID, Text: t.Text, At: t.At})
	}
	if len(c.History) > historyLen {
		c.History = c.History[len(c.History)-historyLen:]
	}
	return c
}

// JobTag starts the text of a trigger that fires a standing job; the job's
// name follows, then a newline. Only a Source the server runs writes it.
const JobTag = "job:"

// jobOf returns the job a trigger fires, if it is one.
func jobOf(t store.Trigger) string {
	if t.Source != string(core.SourceSchedule) {
		return ""
	}
	if rest, ok := strings.CutPrefix(t.Text, JobTag); ok {
		name, _, _ := strings.Cut(rest, "\n")
		return strings.TrimSpace(name)
	}
	return ""
}

// team lists the enabled agents with how busy each is.
func (e *Engine) team() ([]AgentSummary, error) {
	agents, err := e.Store.Agents()
	if err != nil {
		return nil, err
	}
	loads, err := e.Store.AgentLoads()
	if err != nil {
		return nil, err
	}
	var out []AgentSummary
	for _, a := range agents {
		if !a.Enabled || (a.Home != "" && (e.Available == nil || !e.Available(a))) {
			continue
		}
		l := loads[a.ID]
		out = append(out, AgentSummary{ID: a.ID, Name: a.Name, Purpose: a.Purpose, Toolsets: a.ToolsetList(),
			AnyToolset: a.AnyToolset, Working: len(l.Running), Waiting: l.Waiting})
	}
	return out, nil
}

// allowedAgents lists the enabled agents a trigger's work may go to. For
// proactive work it is every agent some enabled rule names; Accept checks
// again against the rules the router matched.
func allowedAgents(p policy.Policy, t CandidateTrigger, enabled []string, js []judged) []string {
	var adm gate.Admission
	var chat []string
	for _, j := range js {
		if j.trigger.ID == t.ID {
			adm, chat = j.admission, j.chat
		}
	}
	var rules []string
	for _, r := range p.Rules {
		if r.Enabled {
			rules = append(rules, r.Name)
		}
	}
	ids, any := gate.AllowedAgents(p, adm, rules)
	var out []string
	for _, id := range enabled {
		if (any || slices.Contains(ids, id)) && (chat == nil || slices.Contains(chat, id)) {
			out = append(out, id)
		}
	}
	return out
}

// chatLead returns the agents a chat message's work may go to: the member
// an @mention in it names, or else every member.
// withinAgents keeps the toolsets at least one of the agents can use. An
// agent that takes any toolset keeps them all.
func withinAgents(ts []core.Toolset, ids []string, team []AgentSummary) []core.Toolset {
	var out []core.Toolset
	for _, t := range ts {
		for _, a := range team {
			if slices.Contains(ids, a.ID) && (a.AnyToolset || slices.Contains(a.Toolsets, t)) {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

func chatLead(members []string, team []AgentSummary, text string) []string {
	lower := strings.ToLower(text)
	for _, a := range team {
		if slices.Contains(members, a.ID) && strings.Contains(lower, "@"+strings.ToLower(a.Name)) {
			return []string{a.ID}
		}
	}
	return append([]string{}, members...)
}

func allowedToolsets(p policy.Policy, a gate.Admission) []core.Toolset {
	var out []core.Toolset
	all := slices.Clone(core.KnownToolsets)
	for _, ts := range p.Toolsets {
		if _, ok := ts.MCPServer(); ok {
			all = append(all, ts)
		}
	}
	for _, ts := range all {
		if gate.CheckToolsets(p, a, []core.Toolset{ts}) == nil {
			out = append(out, ts)
		}
	}
	return out
}

// fail ends an Eval whose router failed. The batch waits for another
// attempt; after MaxAttempts failures in a row it is recorded as
// unavailable instead. Both happen in one transaction, so no other Eval can
// pick up the batch in between.
func (e *Engine) fail(th store.Thread, evalID string, batch []store.Trigger, pol policy.Policy, cause error) error {
	gaveUp := false
	err := e.Store.Tx(func(tx *store.Tx) error {
		cur, err := tx.Thread(th.ID)
		if err != nil {
			return err
		}
		if cur.EvalID != evalID {
			return nil // the lease ran out and another Eval took over
		}
		if cur.EvalFailures+1 >= MaxAttempts {
			gaveUp = true
			return e.recordUnavailable(tx, th.ID, evalID, batch, pol, cause.Error())
		}
		_, err = tx.ReleaseEval(th.ID, evalID, true)
		return err
	})
	if err != nil {
		return err
	}
	if gaveUp && e.OnDecided != nil {
		e.OnDecided(th.ID)
	} else if !gaveUp {
		e.Kick()
	}
	return nil
}

// unavailable records a batch as unavailable in its own transaction.
func (e *Engine) unavailable(th store.Thread, evalID string, batch []store.Trigger, pol policy.Policy, reason string) error {
	err := e.Store.Tx(func(tx *store.Tx) error {
		return e.recordUnavailable(tx, th.ID, evalID, batch, pol, reason)
	})
	if err == nil && e.OnDecided != nil {
		e.OnDecided(th.ID)
	}
	return err
}

// recordUnavailable records the batch as unavailable so a person can ask
// for it to be evaluated again, and frees the Thread with its failure
// count reset.
func (e *Engine) recordUnavailable(tx *store.Tx, threadID, evalID string, batch []store.Trigger, pol policy.Policy, reason string) error {
	var idsAll []string
	for _, t := range batch {
		idsAll = append(idsAll, t.ID)
	}
	if err := tx.CreateDecision(store.Decision{
		ID: ids.New("de"), ThreadID: threadID, EvalID: evalID, Kind: store.DecisionUnavailable,
		TriggerIDs: jsonIDs(idsAll), PolicyVersion: pol.Version, Reason: reason, CreatedAt: e.Now().UTC(),
	}); err != nil {
		return err
	}
	if err := tx.SetTriggersState(idsAll, store.EvalDone); err != nil {
		return err
	}
	return tx.FinishEval(threadID, evalID)
}

// apply checks each Route and stores the results in one transaction.
func (e *Engine) apply(ctx context.Context, th store.Thread, evalID string, evalPolicy policy.Policy, js []judged, routes []Route) error {
	now := e.Now().UTC()
	requeued := false
	var accepted []acceptedTask
	err := e.Store.Tx(func(tx *store.Tx) error {
		owns, err := tx.EvalStillOwns(th.ID, evalID, now)
		if err != nil {
			return err
		}
		if !owns {
			// The lease ran out; another Eval owns the Thread now.
			return nil
		}
		pol, err := tx.Policy()
		if err != nil {
			return err
		}
		if pol.Version != evalPolicy.Version {
			// The Policy changed while the router worked: judge the batch
			// again under the new version.
			requeued = true
			_, err := tx.ReleaseEval(th.ID, evalID, false)
			return err
		}
		a := &applier{tx: tx, jobs: e.Jobs, handOff: e.HandOff, th: th, evalID: evalID, pol: pol, now: now, byID: map[string]*judged{}, decided: map[string]bool{}}
		for i := range js {
			a.byID[js[i].trigger.ID] = &js[i]
		}
		for _, r := range routes {
			if err := a.route(r); err != nil {
				return err
			}
		}
		accepted = a.accepted
		if err := a.leftovers(js); err != nil {
			return err
		}
		var all []string
		for _, j := range js {
			all = append(all, j.trigger.ID)
		}
		if err := tx.SetTriggersState(all, store.EvalDone); err != nil {
			return err
		}
		if err := tx.TouchThread(th.ID, now); err != nil {
			return err
		}
		return tx.FinishEval(th.ID, evalID)
	})
	if err != nil {
		return err
	}
	if requeued {
		e.Kick()
		return nil
	}
	for _, t := range accepted {
		telemetry.StartTask(ctx, t.id,
			telemetry.Role.String(string(t.m.Role)), telemetry.Strength.String(string(t.m.Strength)),
			telemetry.Toolsets.StringSlice(toolsetNames(t.m.Toolsets)), telemetry.Rules.Int(len(t.m.MatchedRules)),
			telemetry.ThreadID.String(th.ID), telemetry.Source.String(string(t.m.Received.Source)))
	}
	if e.OnDecided != nil {
		e.OnDecided(th.ID)
	}
	return nil
}

// acceptedTask is a Ticket an Eval created, for its trace.
type acceptedTask struct {
	id string
	m  core.Mandate
}

func toolsetNames(ts []core.Toolset) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t)
	}
	return out
}

type applier struct {
	handOff  func(tx *store.Tx, a store.Agent, t store.Trigger, threadID string) error
	accepted []acceptedTask
	jobs     func(name string) (JobSpec, error)
	tx       *store.Tx
	th       store.Thread
	evalID   string
	pol      policy.Policy
	now      time.Time
	byID     map[string]*judged
	decided  map[string]bool
}

func (a *applier) decision(kind string, triggerIDs []string, ticketID, note, reason string) error {
	for _, id := range triggerIDs {
		a.decided[id] = true
	}
	return a.tx.CreateDecision(store.Decision{
		ID: ids.New("de"), ThreadID: a.th.ID, EvalID: a.evalID, Kind: kind, TicketID: ticketID,
		TriggerIDs: jsonIDs(triggerIDs), PolicyVersion: a.pol.Version, Note: note, Reason: reason, CreatedAt: a.now,
	})
}

// reply queues a short message to the requester in this Thread. It never
// acts as the owner, so its reach is the owner or the requester.
func (a *applier) reply(j *judged, ticketID, text string) error {
	if text == "" || j.admission.Strength != core.Explicit {
		return nil
	}
	reach := core.ReachRequester
	if j.admission.Role == core.RoleOwner {
		reach = core.ReachOwner
	}
	return a.tx.CreateEffect(store.AddressReply(store.Effect{
		TicketID: ticketID, ThreadID: a.th.ID, Source: a.th.Source, Kind: "message",
		Reach: string(reach), Body: text, Status: store.EffectPending, CreatedAt: a.now,
	}, j.input.Trigger.Actor, j.admission.Role == core.RoleOwner))
}

// refuse records a Route that failed its checks. An explicit requester is
// told plainly, so a refused request never ends in silence.
func (a *applier) refuse(ids []string, j *judged, ticketID, reason string) error {
	if err := a.decision(store.DecisionRefused, ids, ticketID, "", reason); err != nil {
		return err
	}
	if j != nil && !j.trigger.Owned {
		return a.reply(j, ticketID, "I can't do that: "+reason+".")
	}
	return nil
}

func (a *applier) route(r Route) error {
	if len(r.TriggerIDs) == 0 {
		return nil
	}
	// Every Trigger must be in this batch, admitted, and not yet decided.
	var valid []string
	for _, id := range r.TriggerIDs {
		j, ok := a.byID[id]
		if !ok || a.decided[id] || j.admission.Strength == core.NotAdmitted || slices.Contains(valid, id) {
			continue
		}
		valid = append(valid, id)
	}
	if len(valid) == 0 {
		return nil
	}
	primary := a.byID[valid[0]]
	note := trimWords(r.Note, MaxNoteWords)

	// A follow-up a Ticket already owns may only stop that Ticket.
	if primary.trigger.Owned && r.Action != ActionStop {
		return a.refuse(valid, nil, "", "a follow-up owned by a Ticket can only stop it")
	}

	switch r.Action {
	case ActionAccept:
		return a.accept(valid, primary, r, note)
	case ActionAttach:
		return a.attach(valid, primary, r, note)
	case ActionStop:
		return a.stop(valid, primary, r, note)
	case ActionIgnore:
		// Nothing to do, but a person who spoke to Bit still gets an answer.
		if err := a.decision(store.DecisionIgnore, valid, "", note, r.Reason); err != nil {
			return err
		}
		return a.reply(primary, "", note)
	default:
		return a.refuse(valid, nil, "", fmt.Sprintf("unknown action %q", r.Action))
	}
}

func (a *applier) accept(valid []string, primary *judged, r Route, note string) error {
	scope := strings.TrimSpace(r.Scope)
	if scope == "" || len(scope) > 300 {
		return a.refuse(valid, primary, "", "the work needs a scope of up to 300 characters")
	}
	m, err := gate.Accept(a.pol, primary.input, primary.admission, gate.AcceptRequest{
		Scope: scope, MatchedRules: r.MatchedRules, Toolsets: r.Toolsets, Agent: r.Agent, Job: r.Job, At: a.now,
	})
	if errors.Is(err, gate.ErrObserveOnly) {
		return a.decision(store.DecisionWouldAccept, valid, "", note, "observe mode: "+scope)
	}
	if err != nil {
		return a.refuse(valid, primary, "", err.Error())
	}
	// A direct chat or a group fixes who may lead the work.
	if primary.chat != nil && !slices.Contains(primary.chat, m.LeadAgent()) {
		return a.refuse(valid, primary, "", "this chat's work goes only to its own agents")
	}
	// A job only runs as its file says: the agent, the rule and the
	// toolsets come from the file, not from the router.
	if m.Job != "" {
		if a.jobs == nil {
			return a.refuse(valid, primary, "", "jobs aren't set up on this server")
		}
		j, err := a.jobs(m.Job)
		if err != nil {
			return a.refuse(valid, primary, "", "job "+m.Job+": "+err.Error())
		}
		if m.LeadAgent() != j.Agent || !slices.Contains(m.MatchedRules, j.Rule) {
			return a.refuse(valid, primary, "", "job "+m.Job+" runs as "+j.Agent+" under rule "+j.Rule)
		}
		for _, ts := range m.Toolsets {
			if !slices.Contains(j.Toolsets, ts) {
				return a.refuse(valid, primary, "", "job "+m.Job+" doesn't use the "+string(ts)+" toolset")
			}
		}
	}
	// The agent must exist, be on, and be able to use what the work needs.
	ag, err := a.tx.Agent(m.LeadAgent())
	switch {
	case errors.Is(err, store.ErrNotFound) || (err == nil && !ag.Enabled):
		return a.refuse(valid, primary, "", "that agent isn't available")
	case err != nil:
		return err
	}
	for _, ts := range m.Toolsets {
		if !ag.Allows(ts) {
			return a.refuse(valid, primary, "", fmt.Sprintf("%s can't use the %s toolset", ag.Name, ts))
		}
	}
	if ag.Home != "" {
		// Its own machine runs it, under that machine's rules.
		if a.handOff == nil || m.Job != "" {
			return a.refuse(valid, primary, "", "that agent isn't available")
		}
		if err := a.handOff(a.tx, ag, primary.trigger, a.th.ID); err != nil {
			return err
		}
		if err := a.decision(store.DecisionAccept, valid, "", note, "handed to "+ag.Name); err != nil {
			return err
		}
		return a.reply(primary, "", note)
	}
	ticketID := ids.New("tk")
	if err := a.tx.CreateTicket(store.Ticket{ID: ticketID, Open: true, Stage: string(core.StageWorking), OriginThreadID: a.th.ID}, m, a.th.ID, a.now); err != nil {
		return err
	}
	for _, id := range valid {
		if err := a.tx.AddTicketInput(ticketID, id, a.now); err != nil {
			return err
		}
	}
	if err := a.decision(store.DecisionAccept, valid, ticketID, note, r.Reason); err != nil {
		return err
	}
	a.accepted = append(a.accepted, acceptedTask{id: ticketID, m: m})
	if primary.trigger.LeadAgent != "" {
		// The machine that handed it over already told the person.
		return nil
	}
	return a.reply(primary, ticketID, note)
}

// mayActOn reports whether the requester may attach to or stop a Ticket:
// the owner may act on any Ticket, anyone else only on Tickets they asked for.
func mayActOn(j *judged, m core.Mandate) bool {
	return j.admission.Role == core.RoleOwner || j.input.Trigger.Actor == m.Requester
}

func (a *applier) attach(valid []string, primary *judged, r Route, note string) error {
	if primary.admission.Strength != core.Explicit {
		return a.refuse(valid, nil, r.TicketID, "only an explicit request can attach to a Ticket")
	}
	tk, err := a.tx.Ticket(r.TicketID)
	if errors.Is(err, store.ErrNotFound) {
		return a.refuse(valid, primary, "", "there is no such task")
	}
	if err != nil {
		return err
	}
	m, err := tk.DecodeMandate()
	if err != nil {
		return err
	}
	switch {
	case primary.trigger.HandedBackFrom == tk.ID:
		return a.refuse(valid, nil, tk.ID, "that task handed this message back")
	case tk.StoppedBy != "":
		return a.refuse(valid, primary, tk.ID, "that task was stopped; ask again to start new work")
	case !mayActOn(primary, m):
		return a.refuse(valid, primary, tk.ID, "you can only add to tasks you asked for")
	}
	if !tk.Open {
		if err := gate.Revalidate(a.pol, m); err != nil {
			return a.refuse(valid, primary, tk.ID, "that task can't resume under the current policy")
		}
		if err := a.tx.SetTicketState(tk.ID, true, core.StageWorking, "", a.now); err != nil {
			return err
		}
	}
	if err := a.tx.Subscribe(tk.ID, a.th.ID, false, a.now); err != nil {
		return err
	}
	for _, id := range valid {
		if err := a.tx.AddTicketInput(tk.ID, id, a.now); err != nil {
			return err
		}
	}
	if err := a.decision(store.DecisionAttach, valid, tk.ID, note, r.Reason); err != nil {
		return err
	}
	return a.reply(primary, tk.ID, note)
}

func (a *applier) stop(valid []string, primary *judged, r Route, note string) error {
	if primary.admission.Strength != core.Explicit {
		return a.refuse(valid, nil, r.TicketID, "only an explicit request can stop a Ticket")
	}
	tk, err := a.tx.Ticket(r.TicketID)
	if errors.Is(err, store.ErrNotFound) {
		return a.refuse(valid, primary, "", "there is no such task")
	}
	if err != nil {
		return err
	}
	m, err := tk.DecodeMandate()
	if err != nil {
		return err
	}
	if !mayActOn(primary, m) {
		return a.refuse(valid, primary, tk.ID, "you can only stop tasks you asked for")
	}
	if tk.StoppedBy == "" {
		if err := a.tx.SetTicketState(tk.ID, false, core.StageEnded, primary.input.Trigger.Actor.String(), a.now); err != nil {
			return err
		}
		// Unsent actions go with the task; the stop is still confirmed.
		if _, err := a.tx.CancelTicketActions(tk.ID, "the task was stopped"); err != nil {
			return err
		}
	}
	if err := a.decision(store.DecisionStop, valid, tk.ID, note, r.Reason); err != nil {
		return err
	}
	return a.reply(primary, tk.ID, note)
}

// leftovers records Decisions for Triggers no Route decided: the gate's
// refusals and anything the router skipped. A follow-up a Ticket owns
// needs no Decision: it stays with its Ticket.
func (a *applier) leftovers(js []judged) error {
	var refused, skipped, norule []string
	var reasons []string
	for _, j := range js {
		id := j.trigger.ID
		if a.decided[id] || j.trigger.Owned {
			continue
		}
		if j.admission.Strength == core.NotAdmitted {
			refused = append(refused, id)
			if !slices.Contains(reasons, j.admission.Reason) {
				reasons = append(reasons, j.admission.Reason)
			}
		} else if j.admission.Strength == core.Inferred && !j.admission.FollowUp && !a.hasRules() {
			norule = append(norule, id)
		} else {
			skipped = append(skipped, id)
		}
	}
	if len(norule) > 0 {
		if err := a.decision(store.DecisionIgnore, norule, "", "", "no enabled rule covers proactive work"); err != nil {
			return err
		}
	}
	if len(refused) > 0 {
		if err := a.decision(store.DecisionIgnore, refused, "", "", "not admitted: "+strings.Join(reasons, "; ")); err != nil {
			return err
		}
	}
	if len(skipped) > 0 {
		return a.decision(store.DecisionIgnore, skipped, "", "", "the router gave no decision")
	}
	return nil
}

func (a *applier) hasRules() bool {
	for _, r := range a.pol.Rules {
		if r.Enabled {
			return true
		}
	}
	return false
}

func trimWords(s string, n int) string {
	w := strings.Fields(s)
	if len(w) <= n {
		return strings.Join(w, " ")
	}
	return strings.Join(w[:n], " ") + "…"
}

func jsonIDs(ids []string) string {
	b, _ := json.Marshal(ids)
	return string(b)
}

// direct accepts, without the router, each explicit request whose work can
// only go to one agent: a direct chat, an @mention, or a hand-off from
// another machine. The rest is left for the router.
func direct(c Candidate, js []judged) ([]Route, Candidate) {
	rest := c
	rest.Triggers = nil
	var routes []Route
	for _, t := range c.Triggers {
		var chat []string
		for _, j := range js {
			if j.trigger.ID == t.ID {
				chat = j.chat
			}
		}
		if len(chat) != 1 || t.Owned || t.Job != "" || t.HandedBack != "" || t.Strength != core.Explicit || !slices.Contains(t.Agents, chat[0]) {
			rest.Triggers = append(rest.Triggers, t)
			continue
		}
		routes = append(routes, Route{Action: ActionAccept, TriggerIDs: []string{t.ID}, Scope: trimRunes(t.Text, 300), Toolsets: t.Toolsets, Agent: chat[0]})
	}
	return routes, rest
}

func trimRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
