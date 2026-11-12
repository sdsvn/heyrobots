package executor

import (
	"strings"
	"unicode/utf8"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"heyrobots/service/internal/core"
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
			return err
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
		TicketID: r.c.Ticket.ID, ThreadID: r.c.Ticket.OriginThreadID, Source: string(r.mandate.Received.Source),
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
	return r.e.Store.Tx(func(tx *store.Tx) error {
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
			// send nothing more.
			return nil
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
			stage = core.StageWaiting
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
		if stage == core.StageQuestion && len(rep.Choices) > 0 {
			if err := tx.SetTicketChoices(r.c.Ticket.ID, rep.Choices); err != nil {
				return err
			}
		}
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return r.message(tx, "message", text)
	})
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
