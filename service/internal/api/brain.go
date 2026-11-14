package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/store"
)

// GetAgentBrain shows what an agent keeps between tasks. Owner devices
// only. What it keeps for a delegate shows too, labelled with whose it is;
// what went wrong on a delegate's work shows without their words.
func (s *Server) GetAgentBrain(w http.ResponseWriter, r *http.Request, agentID string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	a, err := s.opts.Store.Agent(agentID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such agent")
		return
	}
	if err != nil {
		s.internal(w, "load agent", err)
		return
	}
	out := gen.AgentBrain{WaitingOn: []gen.FollowUp{}, Facts: []gen.BrainFact{}, Diary: []gen.DiaryLine{}, WentWrong: []gen.AgentIncident{}}
	err = s.opts.Store.Tx(func(tx *store.Tx) error {
		fus, err := tx.FollowUps(agentID, "", []string{store.FollowUpOpen, store.FollowUpFired}, 100)
		if err != nil {
			return err
		}
		for _, f := range fus {
			out.WaitingOn = append(out.WaitingOn, followUpView(f))
		}
		facts, err := tx.Facts(agentID, "", true, 300)
		if err != nil {
			return err
		}
		for _, f := range facts {
			v := gen.BrainFact{Id: f.ID, Person: f.Person, Kind: f.Kind, Text: f.Text, Source: f.Source, UpdatedAt: f.UpdatedAt}
			if f.Subject != "" {
				v.Subject = &f.Subject
			}
			v.PutAwayAt = f.OldAt
			out.Facts = append(out.Facts, v)
		}
		lines, err := tx.Diary(agentID, "", "", time.Now().AddDate(0, -3, 0), 100)
		if err != nil {
			return err
		}
		for _, d := range lines {
			v := gen.DiaryLine{Id: d.ID, Person: d.Person, Text: d.Text, At: d.CreatedAt}
			if d.Outcome != "" {
				v.Outcome = &d.Outcome
			}
			if d.ThreadID != "" {
				v.ThreadId = &d.ThreadID
			}
			out.Diary = append(out.Diary, v)
		}
		ins, err := tx.Incidents(agentID, "", time.Now().Add(-store.IncidentWindow), 100)
		if err != nil {
			return err
		}
		for _, in := range ins {
			if (in.Cause == store.CauseSetup && a.Allows(core.Toolset(in.Key))) || in.Cause == store.CauseRouting {
				// A hand-back is routing at work, nothing for the owner to fix.
				continue
			}
			v := gen.AgentIncident{Id: in.ID, Person: in.Person, Cause: in.Cause, What: incidentWhat(in), At: in.At}
			if in.Person == "owner" {
				if in.Detail != "" {
					v.Detail = &in.Detail
				}
				if in.Note != "" {
					v.Note = &in.Note
					keep := in.Cause == store.CauseDeclined
					v.CanKeep = &keep
				}
			}
			out.WentWrong = append(out.WentWrong, v)
		}
		return nil
	})
	if err != nil {
		s.internal(w, "load brain", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func followUpView(f store.BrainFollowUp) gen.FollowUp {
	v := gen.FollowUp{Id: f.ID, Person: f.Person, Kind: f.Kind, What: f.What, DueAt: f.DueAt, State: f.State}
	if f.Why != "" {
		v.Why = &f.Why
	}
	if f.Check != "" {
		v.Check = &f.Check
	}
	if f.ThreadID != "" {
		v.ThreadId = &f.ThreadID
	}
	return v
}

func incidentWhat(in store.Incident) string {
	action := strings.ReplaceAll(in.Key, "_", " ")
	switch in.Cause {
	case store.CauseSetup:
		return "Asked for work that needs " + in.Key + ", which it can't use"
	case store.CauseRouting:
		return "Handed work back as not its own"
	case store.CauseDeclined:
		return "You declined its " + action
	case store.CauseStopped:
		return "You stopped its task"
	case store.CauseSendFailed:
		return "An approved " + action + " couldn't be sent"
	case store.CauseFailed:
		return "A task failed"
	case store.CauseBlocked:
		return "A task got stuck"
	}
	return in.Cause
}

// howToFor starts a how-to made from a declined action's reason.
func howToFor(action string) string {
	switch action {
	case "send_email":
		return "When drafting an email: "
	case "create_event":
		return "When creating a calendar event: "
	case "answer_invite":
		return "When answering an invitation: "
	}
	return "When proposing " + strings.ReplaceAll(action, "_", " ") + ": "
}

// DeleteAgentFact makes an agent forget a fact: it is put away, so the
// owner can undo it, and outside content can't add it again. Owner
// devices only.
func (s *Server) DeleteAgentFact(w http.ResponseWriter, r *http.Request, agentID string, factID int64) {
	now := time.Now().UTC()
	s.setFactOld(w, r, agentID, factID, &now)
}

// RestoreAgentFact brings back a fact the agent forgot or corrected. Owner
// devices only.
func (s *Server) RestoreAgentFact(w http.ResponseWriter, r *http.Request, agentID string, factID int64) {
	s.setFactOld(w, r, agentID, factID, nil)
}

func (s *Server) setFactOld(w http.ResponseWriter, r *http.Request, agentID string, factID int64, at *time.Time) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var found bool
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		facts, err := tx.Facts(agentID, "", true, 10000)
		for _, f := range facts {
			if f.ID == factID {
				found = true
			}
		}
		if err != nil || !found {
			return err
		}
		return tx.SetFactOld(factID, at)
	})
	switch {
	case err != nil:
		s.internal(w, "forget fact", err)
	case !found:
		writeError(w, http.StatusNotFound, "no such fact")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// UpdateFollowUp cancels or moves a follow-up. Owner devices only.
func (s *Server) UpdateFollowUp(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.FollowUpUpdate
	if !decode(w, r, &req) {
		return
	}
	now := time.Now().UTC()
	var out store.BrainFollowUp
	var bad string
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		f, err := tx.FollowUp(id)
		if err != nil {
			return err
		}
		if f.State != store.FollowUpOpen && f.State != store.FollowUpFired {
			bad = "that follow-up is already closed"
			return nil
		}
		switch req.Action {
		case "cancel":
			f.State, f.Outcome = store.FollowUpCancelled, "Cancelled by the owner."
		case "snooze":
			if req.Until == nil || req.Until.Before(now) || req.Until.After(now.AddDate(1, 0, 0)) {
				bad = "snooze needs a time within the next year"
				return nil
			}
			f.State, f.DueAt = store.FollowUpOpen, req.Until.UTC()
		default:
			bad = "action is cancel or snooze"
			return nil
		}
		f.UpdatedAt = now
		out = f
		return tx.UpdateFollowUp(f)
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such follow-up")
	case err != nil:
		s.internal(w, "update follow-up", err)
	case bad != "":
		writeError(w, http.StatusBadRequest, bad)
	default:
		writeJSON(w, http.StatusOK, followUpView(out))
	}
}

// UpdateIncident marks something that went wrong as fixed, or keeps the
// owner's reason for declining as a how-to the agent follows. Owner devices
// only.
func (s *Server) UpdateIncident(w http.ResponseWriter, r *http.Request, id int64) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.IncidentUpdate
	if !decode(w, r, &req) {
		return
	}
	now := time.Now().UTC()
	var bad string
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		in, err := tx.Incident(id)
		if err != nil {
			return err
		}
		switch req.Action {
		case "fixed":
		case "keep":
			if in.Cause != store.CauseDeclined || in.Note == "" || in.Person != "owner" {
				bad = "only your reason for declining can become a how-to"
				return nil
			}
			text := howToFor(in.Key) + in.Note
			if _, err := tx.AddFact(store.BrainFact{AgentID: in.AgentID, Person: in.Person, Kind: store.KindHowTo, Text: text,
				Source: store.FromPerson, ThreadID: in.ThreadID, CreatedAt: now}); err != nil {
				return err
			}
		default:
			bad = "action is fixed or keep"
			return nil
		}
		return tx.ResolveIncident(id, &now)
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such incident")
	case err != nil:
		s.internal(w, "update incident", err)
	case bad != "":
		writeError(w, http.StatusBadRequest, bad)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
