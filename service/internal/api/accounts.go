package api

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/google"
	"heyrobots/service/internal/store"
)

func (s *Server) needGoogle(w http.ResponseWriter, r *http.Request) (*google.Google, bool) {
	if _, ok := requireOwner(w, r); !ok {
		return nil, false
	}
	if s.opts.Google == nil {
		writeError(w, http.StatusBadRequest, "Google accounts aren't available on this server")
		return nil, false
	}
	return s.opts.Google, true
}

// SetGoogleClient saves the owner's OAuth client. Owner devices only.
func (s *Server) SetGoogleClient(w http.ResponseWriter, r *http.Request) {
	g, ok := s.needGoogle(w, r)
	if !ok {
		return
	}
	var req gen.GoogleClient
	if !decode(w, r, &req) {
		return
	}
	if err := g.SetClient(google.ClientConfig{ClientID: req.ClientId, ClientSecret: req.ClientSecret}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.opts.Log.Info("google client saved")
	w.WriteHeader(http.StatusNoContent)
}

var callbackTmpl = template.Must(template.New("cb").Parse(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>heyrobots</title><body style="font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:3rem auto;padding:0 1rem">
{{if .Error}}<h1>Not connected</h1><p>{{.Error}}</p>{{else}}<h1>Connected</h1><p>{{.Email}} is connected.{{if .Address}} The Assistant's address is <b>{{.Address}}</b>.{{end}} You can close this page.</p>{{end}}</body>`))

// GoogleCallback is where Google sends the browser back to, on the
// computer running heyrobots. The one-time state in the address ties it to
// a sign-in an owner device started.
func (s *Server) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	view := struct{ Error, Email, Address string }{}
	if s.opts.Google == nil {
		view.Error = "Google accounts aren't available on this server."
	} else if acc, err := s.finishGoogle(r.Context(), s.opts.Google, r.URL.Query()); err != nil {
		view.Error = err.Error()
	} else {
		view.Email, view.Address = acc.Email, acc.AssistantAddress
	}
	if view.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	callbackTmpl.Execute(w, view)
}

func (s *Server) finishGoogle(ctx context.Context, g *google.Google, q url.Values) (store.Account, error) {
	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			return store.Account{}, errors.New("you declined access in Google; nothing was connected")
		}
		return store.Account{}, errors.New("google answered: " + e)
	}
	if q.Get("state") == "" || q.Get("code") == "" {
		return store.Account{}, errors.New("that address has no sign-in code; paste the whole address the browser ended on")
	}
	acc, err := g.Finish(ctx, q.Get("state"), q.Get("code"))
	if err != nil {
		return store.Account{}, err
	}
	// Connecting a mailbox proves the owner holds its address.
	if err := s.linkOwner(acc.Email, true); err != nil {
		return store.Account{}, err
	}
	s.opts.Log.Info("google account connected", "send", google.HasScope(acc, google.ScopeGmailSend))
	if s.opts.OnAccounts != nil {
		s.opts.OnAccounts()
	}
	return acc, nil
}

// linkOwner adds or removes the account's address as the owner's identity
// on the mail and calendar Sources.
func (s *Server) linkOwner(email string, add bool) error {
	return s.opts.Store.Tx(func(tx *store.Tx) error {
		p, err := tx.Policy()
		if err != nil {
			return err
		}
		changed := false
		for _, src := range []core.SourceKind{core.SourceMail, core.SourceCalendar} {
			id := core.Identity{Source: src, ID: strings.ToLower(email)}
			i := slices.Index(p.Owner, id)
			switch {
			case add && i < 0:
				p.Owner = append(p.Owner, id)
				changed = true
			case !add && i >= 0:
				p.Owner = slices.Delete(p.Owner, i, i+1)
				changed = true
			}
		}
		if !changed {
			return nil
		}
		_, err = tx.SavePolicy(p, s.opts.Now().UTC())
		return err
	})
}

// DisconnectGoogle stops reading the account and revokes its token.
func (s *Server) DisconnectGoogle(w http.ResponseWriter, r *http.Request) {
	g, ok := s.needGoogle(w, r)
	if !ok {
		return
	}
	acc, err := g.Account()
	if errors.Is(err, google.ErrNotConnected) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		s.internal(w, "load account", err)
		return
	}
	if err := g.Disconnect(r.Context()); err != nil {
		s.internal(w, "disconnect", err)
		return
	}
	if err := s.linkOwner(acc.Email, false); err != nil {
		s.internal(w, "unlink owner", err)
		return
	}
	s.opts.Log.Info("google account disconnected")
	w.WriteHeader(http.StatusNoContent)
}
