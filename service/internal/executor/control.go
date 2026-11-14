package executor

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/ids"
	"heyrobots/service/internal/store"
)

// ReportArgs are the report tool's arguments.
type ReportArgs struct {
	State       string   `json:"state" jsonschema:"one of progress, waiting, ready, blocked, failed, dropped"`
	Summary     string   `json:"summary" jsonschema:"what you did, or why you stopped; plain and honest"`
	Result      string   `json:"result,omitempty" jsonschema:"for ready: the result itself, or the file you wrote and what it holds"`
	HumanAction string   `json:"human_action,omitempty" jsonschema:"one concrete question the person must answer, if any"`
	Choices     []string `json:"choices,omitempty" jsonschema:"with human_action: 2 to 5 short answers the person can tap, when the question has a few clear answers; each under 60 characters"`
}

// AckArgs are the ack tool's arguments.
type AckArgs struct {
	Text string `json:"text" jsonschema:"a short confirmation to the person, under 40 words"`
}

// HandBackArgs are the hand_back tool's arguments.
type HandBackArgs struct {
	MessageIDs []string `json:"message_ids" jsonschema:"ids of messages that ask for something beyond this task"`
	Reason     string   `json:"reason" jsonschema:"one sentence on why they don't belong to this task"`
}

// ControlResult is what the control tools return.
type ControlResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (r *run) controlTools() ([]tool.Tool, error) {
	rep, err := functiontool.New(functiontool.Config{
		Name:        "report",
		Description: "Report on the task. Every turn ends with one report whose state is waiting, ready, blocked, failed or dropped. progress is an internal note and does not end the turn.",
	}, func(tc agent.Context, a ReportArgs) (ControlResult, error) {
		state := strings.TrimSpace(a.State)
		if state == "progress" {
			_ = r.e.Store.Tx(func(tx *store.Tx) error { return tx.NoteProgress(r.c.Assignment.ID, shorten(a.Summary, 2000)) })
			return ControlResult{OK: true}, nil
		}
		if !finalStates[state] {
			return ControlResult{Error: "state must be progress, waiting, ready, blocked, failed or dropped"}, nil
		}
		if state == "ready" && strings.TrimSpace(a.Result) == "" {
			return ControlResult{Error: "a ready report needs the result"}, nil
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.final != nil {
			return ControlResult{Error: "you already reported"}, nil
		}
		r.final = &report{State: state, Summary: shorten(a.Summary, 2000), Result: shorten(a.Result, 8000), HumanAction: shorten(a.HumanAction, 500)}
		if strings.TrimSpace(a.HumanAction) != "" {
			r.final.Choices = cleanChoices(a.Choices)
		}
		// End the turn here: the model gets no further call.
		tc.Actions().SkipSummarization = true
		return ControlResult{OK: true}, nil
	})
	if err != nil {
		return nil, err
	}
	ack, err := functiontool.New(functiontool.Config{
		Name:        "ack",
		Description: "Briefly confirm to the person that you're doing what they asked in a message.",
	}, func(_ agent.Context, a AckArgs) (ControlResult, error) {
		text := strings.Join(strings.Fields(a.Text), " ")
		if text == "" || len(strings.Fields(text)) > 40 {
			return ControlResult{Error: "an ack is 1 to 40 words"}, nil
		}
		r.mu.Lock()
		r.acks++
		n := r.acks
		r.mu.Unlock()
		if n > 3 {
			return ControlResult{Error: "too many acks; get on with the work"}, nil
		}
		return ControlResult{OK: true}, r.e.Store.Tx(func(tx *store.Tx) error { return r.message(tx, "message", text) })
	})
	if err != nil {
		return nil, err
	}
	hb, err := functiontool.New(functiontool.Config{
		Name:        "hand_back",
		Description: "Return messages that ask for something beyond this task, so they are routed elsewhere.",
	}, func(_ agent.Context, a HandBackArgs) (ControlResult, error) {
		var valid []string
		for _, id := range a.MessageIDs {
			if r.inputs[id] {
				valid = append(valid, id)
			}
		}
		if len(valid) == 0 {
			return ControlResult{Error: "name messages from this turn's input"}, nil
		}
		var n int64
		err := r.e.Store.Tx(func(tx *store.Tx) error {
			var err error
			n, err = tx.HandBack(r.c.Ticket.ID, valid, shorten(a.Reason, 300))
			if err != nil || n == 0 {
				return err
			}
			return tx.AddIncident(store.Incident{AgentID: r.agent.ID, Person: r.person(), Cause: store.CauseRouting, Key: "handback",
				TicketID: r.c.Ticket.ID, ThreadID: r.c.Ticket.OriginThreadID, Detail: shorten(a.Reason, 300), Tainted: r.isTainted(), At: r.e.Now().UTC()})
		})
		if err != nil {
			return ControlResult{}, err
		}
		if n == 0 {
			return ControlResult{Error: "those messages can't be handed back"}, nil
		}
		if r.e.OnHandBack != nil {
			r.e.OnHandBack()
		}
		return ControlResult{OK: true}, nil
	})
	if err != nil {
		return nil, err
	}
	return []tool.Tool{rep, ack, hb}, nil
}

// message queues a message to the person who asked, in the Ticket's
// conversation. Proactive work goes to the owner's digest instead.
func (r *run) message(tx *store.Tx, kind, text string) error {
	reach := core.ReachRequester
	if r.mandate.Role == core.RoleOwner {
		reach = core.ReachOwner
	}
	status := store.EffectPending
	if r.mandate.Strength == core.Inferred {
		kind, reach, status = "digest", core.ReachOwner, store.EffectHeld
	}
	return tx.CreateEffect(store.AddressReply(store.Effect{
		TicketID: r.c.Ticket.ID, ThreadID: r.c.Ticket.OriginThreadID, Source: string(core.ReplySource(r.mandate.Received.Source)),
		Kind: kind, Reach: string(reach), Body: text, Status: status, CreatedAt: r.e.Now().UTC(),
	}, r.mandate.Requester, r.mandate.Role == core.RoleOwner))
}

// finish stores the report: the Assignment's end, the Ticket's new state,
// and the message to the person, all in one transaction.
func (r *run) finish(rep report) error {
	now := r.e.Now().UTC()
	if rep.State == store.StateInterrupted {
		// The server is stopping; the lease will expire and the next start
		// recovers the Assignment.
		return nil
	}
	asked, answered := false, false
	err := r.e.Store.Tx(func(tx *store.Tx) error {
		if err := tx.EndAssignment(r.c.Assignment.ID, rep.State, rep.Summary, rep.HumanAction, rep.Result, now); err != nil {
			return err
		}
		if rep.PausedAt != "" {
			if err := tx.SetAssignmentPause(r.c.Assignment.ID, rep.PausedAt); err != nil {
				return err
			}
		}
		cur, err := tx.Ticket(r.c.Ticket.ID)
		if err != nil {
			return err
		}
		if cur.StoppedBy != "" {
			// A person stopped the task while it ran: keep it stopped and
			// send nothing more, but remember that it was stopped.
			return r.stoppedBrain(tx, now)
		}
		open, stage := true, core.StageWaiting
		var text string
		switch rep.State {
		case "ready":
			open, stage = false, core.StageReady
			text = rep.Result
			if s := strings.TrimSpace(rep.Summary); s != "" && s != strings.TrimSpace(rep.Result) {
				text = s + "\n\n" + rep.Result
			}
		case "failed":
			open, stage = false, core.StageFailed
			text = "I couldn't finish this: " + rep.Summary
		case "dropped":
			open, stage = false, core.StageEnded
		case "blocked":
			stage = core.StageBlocked
			text = rep.Summary
		case "waiting":
			// Waiting on a teammate, say: what it wrote still goes out, so
			// an @mention in it reaches them.
			stage = core.StageWaiting
			text = rep.Result
		}
		if q := strings.TrimSpace(rep.HumanAction); q != "" && open {
			stage = core.StageQuestion
			if text != "" {
				text += "\n\n"
			}
			text += q
		}
		if err := tx.SetTicketState(r.c.Ticket.ID, open, stage, "", now); err != nil {
			return err
		}
		footer, err := r.endBrain(tx, rep, now)
		if err != nil {
			return err
		}
		if strings.TrimSpace(text) != "" {
			text += footer
		}
		if r.mandate.Received.Source == core.SourceFollowUp && stage != core.StageQuestion && stage != core.StageBlocked {
			// A follow-up that needed nothing from the person closes
			// quietly: what it found goes to the digest, not the chat.
			if n, err := tx.TicketAwaiting(r.c.Ticket.ID); err != nil || n == 0 {
				if strings.TrimSpace(text) == "" || err != nil {
					return err
				}
				return tx.CreateEffect(store.Effect{TicketID: r.c.Ticket.ID, ThreadID: r.c.Ticket.OriginThreadID, Source: string(core.SourceApp),
					Kind: "digest", Reach: string(core.ReachOwner), Body: r.agent.Name + ": " + text, Status: store.EffectHeld, CreatedAt: now})
			}
		}
		if stage == core.StageQuestion && len(rep.Choices) > 0 {
			if err := tx.SetTicketChoices(r.c.Ticket.ID, rep.Choices); err != nil {
				return err
			}
		}
		if strings.TrimSpace(text) == "" {
			return nil
		}
		if err := r.message(tx, "message", text); err != nil {
			return err
		}
		if answered, err = r.answerAsker(tx, text, now); err != nil {
			return err
		}
		if answered {
			// The answer goes back to the asker already; an @mention of it
			// in the answer would only ask it twice.
			return nil
		}
		asked, err = r.askMembers(tx, text, now)
		return err
	})
	if err == nil && asked && r.e.OnHandBack != nil {
		r.e.OnHandBack()
	}
	if err == nil && answered {
		r.e.Kick()
	}
	return err
}

// askPrefix starts the delivery id of a member's request; the asking task
// follows, so the answer can find its way back.
const askPrefix = "ask:"

// answerAsker sends a member's answer back to the task that asked for it,
// which then carries on with it.
func (r *run) answerAsker(tx *store.Tx, text string, now time.Time) (bool, error) {
	if r.mandate.Received.Source != core.SourceAgent {
		return false, nil
	}
	for _, in := range r.c.Inputs {
		rest, ok := strings.CutPrefix(in.DeliveryID, askPrefix)
		if !ok || in.Source != string(core.SourceAgent) {
			continue
		}
		asker, _, _ := strings.Cut(rest, ":")
		tk, err := tx.Ticket(asker)
		if err != nil || !tk.Open || tk.StoppedBy != "" {
			continue
		}
		t := store.Trigger{
			ID: ids.New("tr"), Source: string(core.SourceAgent), DeliveryID: "answer:" + r.c.Assignment.ID + ":" + asker,
			ActorSource: in.ActorSource, ActorID: in.ActorID, Authenticity: in.Authenticity, Addressed: true, ReceivedAll: true,
			Text: r.agent.Name + " answers: " + text, At: now, CreatedAt: now, EvalState: store.EvalDone, Owned: true,
		}
		if err := tx.CreateTrigger(t, []string{r.c.Ticket.OriginThreadID}); err != nil {
			return false, err
		}
		if err := tx.AddTicketInput(asker, t.ID, now); err != nil {
			return false, err
		}
		if err := tx.SetTicketState(asker, true, core.StageWorking, "", now); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// maxMemberTurns is how many requests between members may follow one
// another in a group before it waits for a person.
const maxMemberTurns = 6

// askMembers lets a group member ask another for a piece of work by
// @mentioning it in its reply. The mentioned member gets the request with
// the authority of this task, never more: it can't change jobs, rules or
// settings, and anything outward still waits for the owner.
func (r *run) askMembers(tx *store.Tx, text string, now time.Time) (bool, error) {
	if r.mandate.Strength != core.Explicit {
		return false, nil
	}
	th, err := tx.Thread(r.c.Ticket.OriginThreadID)
	if err != nil || th.GroupID == "" {
		return false, nil
	}
	g, err := tx.Group(th.GroupID)
	if err != nil {
		return false, nil
	}
	lower := strings.ToLower(text)
	var to []store.Agent
	for _, id := range g.MemberList() {
		if id == r.agent.ID {
			continue
		}
		if a, err := tx.Agent(id); err == nil && strings.Contains(lower, "@"+strings.ToLower(a.Name)) {
			to = append(to, a)
		}
	}
	if len(to) == 0 {
		return false, nil
	}
	// Members talking among themselves stop after a few turns and wait.
	recent, err := tx.RecentTriggers(th.ID, maxMemberTurns)
	if err != nil {
		return false, err
	}
	if len(recent) >= maxMemberTurns && !slices.ContainsFunc(recent, func(t store.Trigger) bool { return t.Source != string(core.SourceAgent) }) {
		return false, r.message(tx, "message", "We've passed this back and forth a few times; I'll wait for you before going on.")
	}
	req := r.mandate.Requester
	for i, a := range to {
		t := store.Trigger{
			ID: ids.New("tr"), Source: string(core.SourceAgent), DeliveryID: fmt.Sprintf("%s%s:%s:%s:%d", askPrefix, r.c.Ticket.ID, r.c.Assignment.ID, a.ID, i),
			ActorSource: string(req.Source), ActorID: req.ID, Authenticity: int(core.Verified), Addressed: true, ReceivedAll: true,
			Text: r.agent.Name + " asks: " + text, At: now, CreatedAt: now, EvalState: store.EvalPending, LeadAgent: a.ID,
		}
		if err := tx.CreateTrigger(t, []string{th.ID}); err != nil {
			return false, err
		}
	}
	return true, tx.TouchThread(th.ID, now)
}

// Answer choices are short single lines; a question offers 2 to 5.
const (
	maxChoices   = 5
	maxChoiceLen = 60
)

// cleanChoices keeps the answers a person can tap: single-line, short and
// distinct. Fewer than two leaves none, since the person then just types.
func cleanChoices(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range in {
		c = strings.Join(strings.Fields(c), " ")
		k := strings.ToLower(c)
		if c == "" || utf8.RuneCountInString(c) > maxChoiceLen || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
		if len(out) == maxChoices {
			break
		}
	}
	if len(out) < 2 {
		return nil
	}
	return out
}
