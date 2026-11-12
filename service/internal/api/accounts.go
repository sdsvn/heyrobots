package api

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/google"
	"heyrobots/service/internal/ids"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/sources/schedule"
	"heyrobots/service/internal/store"
)

// ListAccounts lists connected accounts. Owner devices only.
func (s *Server) ListAccounts(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	out := gen.AccountList{Accounts: []gen.Account{}, GoogleRedirectUrl: s.opts.GoogleRedirect}
	if g := s.opts.Google; g != nil {
		_, err := g.Client()
		out.GoogleClientSet = err == nil
	}
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		as, err := tx.Accounts()
		for _, a := range as {
			out.Accounts = append(out.Accounts, accountView(a))
		}
		return err
	})
	if err != nil {
		s.internal(w, "list accounts", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func accountView(a store.Account) gen.Account {
	v := gen.Account{Provider: a.Provider, Email: a.Email, AssistantAddress: a.AssistantAddress, Status: gen.AccountStatus(a.Status),
		CanSend:     google.HasScope(a, google.ScopeGmailSend) && google.HasScope(a, google.ScopeCalendarEdit),
		ConnectedAt: a.ConnectedAt, CheckedAt: a.CheckedAt}
	if a.StatusError != "" {
		e := a.StatusError
		v.StatusError = &e
	}
	return v
}

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

// StartGoogleConnect begins signing in to Google. Owner devices only.
func (s *Server) StartGoogleConnect(w http.ResponseWriter, r *http.Request) {
	g, ok := s.needGoogle(w, r)
	if !ok {
		return
	}
	var req gen.GoogleConnect
	if !decode(w, r, &req) {
		return
	}
	tag := ""
	if req.AddressTag != nil {
		tag = strings.TrimSpace(*req.AddressTag)
	}
	u, _, err := g.Start(s.opts.GoogleRedirect, req.AllowSending, tag)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, gen.GoogleConnectStart{AuthUrl: u, RedirectUrl: s.opts.GoogleRedirect})
}

// FinishGoogleConnect finishes signing in with the address the browser
// ended on. Owner devices only.
func (s *Server) FinishGoogleConnect(w http.ResponseWriter, r *http.Request) {
	g, ok := s.needGoogle(w, r)
	if !ok {
		return
	}
	var req gen.GoogleConnectFinish
	if !decode(w, r, &req) {
		return
	}
	u, err := url.Parse(strings.TrimSpace(req.RedirectUrl))
	if err != nil {
		writeError(w, http.StatusBadRequest, "paste the whole address from the browser")
		return
	}
	acc, err := s.finishGoogle(r.Context(), g, u.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, accountView(acc))
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

// ListSchedules lists schedules. Owner devices only.
func (s *Server) ListSchedules(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	p, err := s.opts.Store.Policy()
	if err != nil {
		s.internal(w, "load policy", err)
		return
	}
	out := gen.ScheduleList{Schedules: []gen.Schedule{}, Timezone: p.Limits.Location().String()}
	err = s.opts.Store.Tx(func(tx *store.Tx) error {
		ss, err := tx.Schedules()
		for _, sc := range ss {
			out.Schedules = append(out.Schedules, scheduleView(sc))
		}
		return err
	})
	if err != nil {
		s.internal(w, "list schedules", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func scheduleView(sc store.Schedule) gen.Schedule {
	return gen.Schedule{Id: sc.ID, Name: sc.Name, Note: sc.Note, Days: sc.Days, Enabled: sc.Enabled,
		Time:      time.Date(0, 1, 1, sc.Minute/60, sc.Minute%60, 0, 0, time.UTC).Format("15:04"),
		CreatedAt: sc.CreatedAt, LastRunAt: sc.LastRunAt}
}

// CreateSchedule adds a schedule. Owner devices only.
func (s *Server) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	d, ok := requireOwner(w, r)
	if !ok {
		return
	}
	var req gen.ScheduleRequest
	if !decode(w, r, &req) {
		return
	}
	days := ""
	if req.Days != nil {
		days = *req.Days
	}
	sc, msg := newSchedule(req.Name, req.Note, days, req.Time, appIdentity(d), s.opts.Now())
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	err := s.opts.Store.Tx(func(tx *store.Tx) error { return tx.CreateSchedule(sc) })
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, "a schedule with that name exists")
			return
		}
		s.internal(w, "create schedule", err)
		return
	}
	writeJSON(w, http.StatusCreated, scheduleView(sc))
}

func newSchedule(name, note, days, at string, by core.Identity, now time.Time) (store.Schedule, string) {
	name, note = strings.TrimSpace(name), strings.TrimSpace(note)
	if name == "" || len(name) > 60 {
		return store.Schedule{}, "a schedule needs a name of up to 60 characters"
	}
	if note == "" || len(note) > 1000 {
		return store.Schedule{}, "say what should happen, in up to 1000 characters"
	}
	d, err := schedule.ParseDays(days)
	if err != nil {
		return store.Schedule{}, err.Error()
	}
	min, err := schedule.ParseTime(at)
	if err != nil {
		return store.Schedule{}, err.Error()
	}
	return store.Schedule{ID: ids.New("sc"), Name: name, Note: note, Days: d, Minute: min,
		CreatedBy: by.String(), CreatedAt: now.UTC(), Enabled: true}, ""
}

// DeleteSchedule removes a schedule. Owner devices only.
func (s *Server) DeleteSchedule(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	err := s.opts.Store.Tx(func(tx *store.Tx) error { return tx.DeleteSchedule(id) })
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such schedule")
	case err != nil:
		s.internal(w, "delete schedule", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// ListRuleTemplates lists the starter rules. Owner devices only.
func (s *Server) ListRuleTemplates(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	p, err := s.opts.Store.Policy()
	if err != nil {
		s.internal(w, "load policy", err)
		return
	}
	connected := s.connected()
	out := gen.RuleTemplateList{Mode: gen.AcceptMode(p.Mode), Templates: []gen.RuleTemplate{}}
	for _, t := range policy.RuleTemplates {
		v := gen.RuleTemplate{Name: t.Name, Text: t.Text, Available: true,
			Added: slices.ContainsFunc(p.Rules, func(x policy.Rule) bool { return x.Name == t.Name })}
		if len(t.Needs) > 0 {
			needs := t.Needs
			v.Needs = &needs
			for _, n := range t.Needs {
				v.Available = v.Available && connected[n]
			}
		}
		if t.ScheduleTime != "" {
			d, tm := t.ScheduleDays, t.ScheduleTime
			v.ScheduleDays, v.ScheduleTime = &d, &tm
		}
		out.Templates = append(out.Templates, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) connected() map[string]bool {
	out := map[string]bool{}
	_ = s.opts.Store.Tx(func(tx *store.Tx) error {
		as, err := tx.Accounts()
		for _, a := range as {
			out[a.Provider] = a.Status == store.AccountConnected
		}
		return err
	})
	return out
}

// AddRuleTemplate adds a starter rule, and its schedule if it has one.
// Owner devices only.
func (s *Server) AddRuleTemplate(w http.ResponseWriter, r *http.Request, name string) {
	d, ok := requireOwner(w, r)
	if !ok {
		return
	}
	i := slices.IndexFunc(policy.RuleTemplates, func(t policy.RuleTemplate) bool { return t.Name == name })
	if i < 0 {
		writeError(w, http.StatusNotFound, "no such template")
		return
	}
	t := policy.RuleTemplates[i]
	var saved policy.Policy
	var status int
	var msg string
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		p, err := tx.Policy()
		if err != nil {
			return err
		}
		if slices.ContainsFunc(p.Rules, func(x policy.Rule) bool { return x.Name == t.Name }) {
			status, msg = http.StatusConflict, "that rule is already added"
			return nil
		}
		p.Rules = append(p.Rules, t.Rule)
		if saved, err = tx.SavePolicy(p, s.opts.Now().UTC()); err != nil {
			return err
		}
		if t.ScheduleTime == "" {
			return nil
		}
		sc, bad := newSchedule(t.Name, t.Text, t.ScheduleDays, t.ScheduleTime, appIdentity(d), s.opts.Now())
		if bad != "" {
			return errors.New(bad)
		}
		return tx.CreateSchedule(sc)
	})
	if status != 0 {
		writeError(w, status, msg)
		return
	}
	if err != nil {
		s.internal(w, "add rule template", err)
		return
	}
	s.opts.Broker.Publish(Event{Type: "policy.updated", Data: map[string]int{"version": saved.Version}, Audience: policy.AppOwner.String()})
	writeJSON(w, http.StatusOK, policyView(saved))
}
