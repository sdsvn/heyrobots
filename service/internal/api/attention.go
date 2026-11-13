package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/devices"
	"heyrobots/service/internal/notify"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

// Pusher sends push notifications.
type Pusher interface {
	Send(ctx context.Context, cfg policy.Notify, p notify.Push) error
	Kick()
}

// ListAttention lists what waits on the device's person.
func (s *Server) ListAttention(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r)
	owner := d.Role == devices.RoleOwner
	out := gen.AttentionList{Items: []gen.AttentionItem{}}
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		items, err := tx.Attention(owner, appIdentity(d).String())
		if err != nil {
			return err
		}
		for _, it := range items {
			v := gen.AttentionItem{Id: it.Key, Kind: gen.AttentionItemKind(it.Kind), ThreadId: it.ThreadID,
				Text: shorten(it.Text, 500), At: it.At, Dismissable: it.Kind == store.AttentionFailure}
			if it.TicketID != "" {
				tid := it.TicketID
				v.TicketId = &tid
			}
			if it.Kind == store.AttentionApproval {
				aid, action, reach := apiEffectID(it.EffectID), it.Action, it.Reach
				v.ApprovalId, v.Action, v.Reach = &aid, &action, &reach
				if it.Recipients != "" {
					rs := strings.Split(it.Recipients, ", ")
					v.Recipients = &rs
				}
			}
			if len(it.Choices) > 0 {
				cs := it.Choices
				v.Choices = &cs
			}
			if it.TicketID != "" {
				if tk, err := tx.Ticket(it.TicketID); err == nil {
					if m, err := tk.DecodeMandate(); err == nil && m.Scope != "" {
						scope := shorten(m.Scope, 120)
						v.Scope = &scope
					}
				}
			}
			out.Items = append(out.Items, v)
		}
		return nil
	})
	if err != nil {
		s.internal(w, "list attention", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// DismissAttention dismisses a failed send. Owner devices only.
func (s *Server) DismissAttention(w http.ResponseWriter, r *http.Request, itemID string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	id, ok := effectID("ef_" + strings.TrimPrefix(itemID, "failure:"))
	if !ok || !strings.HasPrefix(itemID, "failure:") {
		writeError(w, http.StatusNotFound, "only a failed send can be dismissed")
		return
	}
	err := s.opts.Store.Tx(func(tx *store.Tx) error { return tx.AckEffect(id, s.opts.Now().UTC()) })
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such failed send")
		return
	case err != nil:
		s.internal(w, "dismiss", err)
		return
	}
	s.opts.Broker.Publish(Event{Type: "attention.changed", Data: map[string]string{"id": itemID}})
	w.WriteHeader(http.StatusNoContent)
}
