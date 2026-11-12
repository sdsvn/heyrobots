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
	"heyrobots/service/internal/vault"
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

// GetNotifications returns the push settings. Owner devices only.
func (s *Server) GetNotifications(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	p, err := s.opts.Store.Policy()
	if err != nil {
		s.internal(w, "load policy", err)
		return
	}
	writeJSON(w, http.StatusOK, s.notifyView(p.Notify))
}

// UpdateNotifications changes the push settings. Owner devices only.
func (s *Server) UpdateNotifications(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.NotifyUpdate
	if !decode(w, r, &req) {
		return
	}
	n := policy.Notify{Enabled: req.Enabled, ServerURL: strings.TrimSpace(req.ServerUrl), Topic: strings.TrimSpace(req.Topic),
		QuietStart: req.QuietStart, QuietEnd: req.QuietEnd, QuietSet: true, PerHour: req.PerHour}
	if n.ServerURL == "" {
		n.ServerURL = policy.DefaultNtfyServer
	}
	switch {
	case n.QuietStart < 0 || n.QuietStart > 23 || n.QuietEnd < 0 || n.QuietEnd > 23:
		writeError(w, http.StatusBadRequest, "quiet hours are hours from 0 to 23")
		return
	case n.PerHour < 1 || n.PerHour > 60:
		writeError(w, http.StatusBadRequest, "pushes per hour must be between 1 and 60")
		return
	case n.Topic == "" && n.Enabled:
		writeError(w, http.StatusBadRequest, "choose a topic to turn on push notifications")
		return
	}
	if n.Topic != "" || n.ServerURL != policy.DefaultNtfyServer {
		if _, err := notify.TopicURL(n.ServerURL, orDefault(n.Topic, "check")); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.Token != nil {
		if err := s.opts.Vault.Set(vault.NtfyToken, strings.TrimSpace(*req.Token)); err != nil {
			s.internal(w, "save ntfy token", err)
			return
		}
	}
	var saved policy.Policy
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		p, err := tx.Policy()
		if err != nil {
			return err
		}
		p.Notify = n
		saved, err = tx.SavePolicy(p, s.opts.Now().UTC())
		return err
	})
	if err != nil {
		s.internal(w, "save policy", err)
		return
	}
	s.opts.Log.Info("notification settings changed", "version", saved.Version, "enabled", n.Enabled)
	s.opts.Broker.Publish(Event{Type: "policy.updated", Data: map[string]int{"version": saved.Version}})
	if s.opts.Pusher != nil {
		s.opts.Pusher.Kick()
	}
	writeJSON(w, http.StatusOK, s.notifyView(saved.Notify))
}

// TestNotifications sends a test push. Owner devices only.
func (s *Server) TestNotifications(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	p, err := s.opts.Store.Policy()
	if err != nil {
		s.internal(w, "load policy", err)
		return
	}
	if s.opts.Pusher == nil || p.Notify.Topic == "" {
		writeError(w, http.StatusBadRequest, "choose a topic first")
		return
	}
	err = s.opts.Pusher.Send(r.Context(), p.Notify, notify.Push{Title: "heyrobots can reach you", Click: "heyrobots://home", Body: "This is a test notification."})
	if err != nil {
		s.opts.Log.Warn("test push", "err", err)
		writeError(w, http.StatusBadGateway, "the push failed: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) notifyView(n policy.Notify) gen.NotifySettings {
	n = n.Effective()
	tok, _ := s.opts.Vault.Get(vault.NtfyToken)
	return gen.NotifySettings{Enabled: n.Enabled, ServerUrl: n.ServerURL, Topic: n.Topic, QuietStart: n.QuietStart,
		QuietEnd: n.QuietEnd, PerHour: n.PerHour, HasToken: tok != ""}
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
