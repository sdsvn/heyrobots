package api

import (
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/devices"
	"heyrobots/service/internal/ids"
	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

// appIdentity is who a device speaks as on the app Source. Every owner
// device is the owner; a delegate device is that delegate.
func appIdentity(d store.Device) core.Identity {
	if d.Role == devices.RoleOwner {
		return policy.AppOwner
	}
	return policy.AppDelegate(d.DelegateName)
}

// canSee reports whether a device may see a Thread: the owner sees every
// Thread; a delegate sees only the app Threads they started.
func canSee(d store.Device, th store.Thread) bool {
	if d.Role == devices.RoleOwner {
		return true
	}
	return th.Source == string(core.SourceApp) && th.CreatedBy == appIdentity(d).String()
}

// SendMessage stores a chat message as a Trigger on the app Source.
func (s *Server) SendMessage(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r)
	var req gen.SendMessageRequest
	if !decode(w, r, &req) {
		return
	}
	text := strings.TrimSpace(req.Text)
	switch {
	case req.ClientMessageId == "" || len(req.ClientMessageId) > 64:
		writeError(w, http.StatusBadRequest, "client_message_id must be 1 to 64 characters")
		return
	case text == "" || utf8.RuneCountInString(text) > 8000:
		writeError(w, http.StatusBadRequest, "text must be 1 to 8000 characters")
		return
	}
	who := appIdentity(d)
	key := "chat:" + ids.New("c")
	// A reply to an existing conversation goes to it by id, whatever Source
	// started it: a person answers a schedule's question in the app. A
	// message to an agent or a group goes to that chat.
	var threadIDs []string
	var (
		th     store.Thread
		status int
		msg    string
	)
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		var err error
		th, status, msg, err = s.chatThread(tx, d, req)
		return err
	})
	if err != nil {
		s.internal(w, "load thread", err)
		return
	}
	if status != 0 {
		writeError(w, status, msg)
		return
	}
	if th.ID != "" {
		key, threadIDs = "", []string{th.ID}
	}
	res, err := s.opts.Intake.Ingest(intake.Message{
		Trigger: core.Trigger{
			// The device and its own message id make the delivery unique, so
			// a retried send is stored once.
			DeliveryID:   d.ID + ":" + req.ClientMessageId,
			Source:       core.SourceApp,
			Actor:        who,
			Authenticity: core.Verified,
			Addressed:    true,
			Text:         text,
			At:           s.opts.Now().UTC(),
		},
		ThreadKeys: keysOf(key),
		ThreadIDs:  threadIDs,
		CreatedBy:  who,
		// The owner's chats are shared: the machines agree who routes them.
		// A direct chat needs no agreement: only its agent's machine answers.
		Hold: s.opts.Cluster != nil && d.Role == devices.RoleOwner && th.AgentID == "" && s.opts.Cluster.HasPeers(),
	})
	if err != nil {
		s.internal(w, "store message", err)
		return
	}
	if !res.Duplicate {
		for _, id := range res.ThreadIDs {
			s.opts.Broker.Publish(Event{Type: "thread.updated", Data: map[string]string{"id": id}, Audience: who.String()})
		}
		if s.opts.OnMessage != nil {
			s.opts.OnMessage()
		}
		if s.opts.Cluster != nil {
			for _, id := range res.ThreadIDs {
				s.opts.Cluster.Publish(id)
			}
		}
	}
	writeJSON(w, http.StatusAccepted, gen.SendMessageResponse{TriggerId: res.TriggerID, ThreadId: res.ThreadIDs[0], Duplicate: res.Duplicate})
}

// ThreadUpdated tells apps that a Thread changed, for example after Eval.
func (s *Server) ThreadUpdated(threadID string) {
	var th store.Thread
	if err := s.opts.Store.Tx(func(tx *store.Tx) error {
		var err error
		th, err = tx.Thread(threadID)
		return err
	}); err != nil {
		s.opts.Log.Warn("thread updated event", "thread", threadID, "err", err)
		return
	}
	s.opts.Broker.Publish(Event{Type: "thread.updated", Data: map[string]string{"id": threadID}, Audience: th.CreatedBy})
	if s.opts.Cluster != nil {
		s.opts.Cluster.Publish(threadID)
	}
}

// ListThreads lists conversations the device may see.
func (s *Server) ListThreads(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r)
	createdBy := ""
	if d.Role != devices.RoleOwner {
		createdBy = appIdentity(d).String()
	}
	team, err := s.teamByID()
	if err != nil {
		s.internal(w, "load team", err)
		return
	}
	who := appIdentity(d)
	out := gen.ThreadList{Threads: []gen.ThreadSummary{}}
	err = s.opts.Store.Tx(func(tx *store.Tx) error {
		ths, err := tx.Threads(createdBy, 100)
		if err != nil {
			return err
		}
		waiting, err := tx.Attention(d.Role == devices.RoleOwner, createdBy)
		if err != nil {
			return err
		}
		needs := map[string]string{}
		for _, it := range waiting {
			if _, ok := needs[it.ThreadID]; !ok {
				needs[it.ThreadID] = it.Text
			}
		}
		for _, th := range ths {
			if !canSee(d, th) {
				continue
			}
			sum := gen.ThreadSummary{Id: th.ID, CreatedBy: th.CreatedBy, UpdatedAt: th.UpdatedAt}
			if a, ok := team[th.AgentID]; ok {
				sum.Agent, sum.Title = s.agentRef(a), a.Name
			}
			if th.GroupID != "" {
				g, err := tx.Group(th.GroupID)
				if err != nil {
					return err
				}
				sum.Group, sum.Title = s.groupView(g, team), g.Name
			}
			if sum.Title == "" {
				title, err := tx.FirstTriggerText(th.ID)
				if err != nil {
					return err
				}
				sum.Title = shorten(title, 80)
			}
			if text, ok := needs[th.ID]; ok {
				sum.NeedsYou = &text
			}
			act, err := tx.Activity(th.ID, d.ID, who)
			if err != nil {
				return err
			}
			sum.Unread, sum.Typing = act.Unread, act.Typing
			if t := act.LastTrigger; t != nil {
				sum.Last = &gen.LastMessage{From: t.ActorID, Text: shorten(t.Text, 140), At: t.At}
				if t.ActorSource == string(who.Source) && t.ActorID == who.ID {
					seen := act.Seen
					sum.Last.From, sum.Last.Seen = "you", &seen
				}
			}
			if e := act.LastReply; e != nil && (sum.Last == nil || !e.CreatedAt.Before(sum.Last.At)) {
				from := team[core.GeneralAgent].Name
				if tk, err := tx.Ticket(e.TicketID); err == nil {
					if a, ok := team[tk.AgentID]; ok {
						from = a.Name
					}
				} else if a, ok := team[th.AgentID]; ok {
					from = a.Name
				}
				sum.Last = &gen.LastMessage{From: from, Text: shorten(e.Body, 140), At: e.CreatedAt}
			}
			out.Threads = append(out.Threads, sum)
		}
		return nil
	})
	if err != nil {
		s.internal(w, "list threads", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// GetThread returns a conversation with everything that happened on it.
func (s *Server) GetThread(w http.ResponseWriter, r *http.Request, threadID string) {
	d := deviceFrom(r)
	var v store.ThreadView
	var g store.Group
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		var err error
		if v, err = tx.ThreadView(threadID); err != nil || v.Thread.GroupID == "" {
			return err
		}
		g, err = tx.Group(v.Thread.GroupID)
		return err
	})
	if errors.Is(err, store.ErrNotFound) || (err == nil && !canSee(d, v.Thread)) {
		writeError(w, http.StatusNotFound, "no such conversation")
		return
	}
	if err != nil {
		s.internal(w, "load thread", err)
		return
	}
	team, err := s.teamByID()
	if err != nil {
		s.internal(w, "load team", err)
		return
	}
	out := gen.ThreadDetail{Id: v.Thread.ID, CreatedBy: v.Thread.CreatedBy, Items: []gen.ThreadItem{}, Tickets: []gen.TicketView{}}
	if a, ok := team[v.Thread.AgentID]; ok {
		out.Agent = s.agentRef(a)
	}
	if g.ID != "" {
		out.Group = s.groupView(g, team)
	}
	// Who is on each task, so the conversation can show their avatar.
	onTicket := map[string]*gen.AgentRef{}
	for _, t := range v.Tickets {
		agentID := t.AgentID
		if agentID == "" {
			m, _ := t.DecodeMandate()
			agentID = m.LeadAgent()
		}
		if a, ok := team[agentID]; ok {
			onTicket[t.ID] = s.agentRef(a)
		}
	}
	for _, t := range v.Triggers {
		if t.Source == string(core.SourceAgent) || t.Source == string(core.SourceFollowUp) {
			// A member's request to another shows as that member's reply,
			// and a follow-up coming due speaks only through its result.
			continue
		}
		from, text := t.ActorSource+":"+t.ActorID, t.Text
		out.Items = append(out.Items, gen.ThreadItem{Kind: gen.Message, Id: t.ID, At: t.At, From: &from, Text: &text})
	}
	for _, dc := range v.Decisions {
		kind, reason := dc.Kind, dc.Reason
		item := gen.ThreadItem{Kind: gen.Decision, Id: dc.ID, At: dc.CreatedAt, Decision: &kind, Reason: &reason}
		if dc.TicketID != "" {
			tid := dc.TicketID
			item.TicketId = &tid
		}
		out.Items = append(out.Items, item)
	}
	chat := v.Thread.AgentID != "" || v.Thread.GroupID != ""
	for _, e := range v.Effects {
		if chat && e.Kind == "digest" && e.Action == store.FollowUpDigest {
			// What a quiet follow-up found goes to the digest, not the chat.
			continue
		}
		id, text, status := apiEffectID(e.ID), e.Body, e.Status
		item := gen.ThreadItem{Kind: gen.Reply, Id: id, At: e.CreatedAt, Text: &text, Status: &status, Agent: onTicket[e.TicketID]}
		if a, ok := team[e.AgentID]; ok && e.Origin != "" {
			item.Agent = s.agentRef(a)
		}
		if e.TicketID != "" {
			tid := e.TicketID
			item.TicketId = &tid
		}
		if e.Kind == "action" {
			action, recipients := e.Action, e.Recipients
			item.Kind, item.Action, item.Recipients = gen.Action, &action, &recipients
			if e.Approval != "" {
				approval := e.Approval
				item.Approval = &approval
			}
		}
		if e.LastError != "" {
			le := e.LastError
			item.LastError = &le
		}
		cardItem(e, &item)
		item.File = fileView(e)
		out.Items = append(out.Items, item)
	}
	order := map[gen.ThreadItemKind]int{gen.Message: 0, gen.Decision: 1, gen.Reply: 2, gen.Action: 2, gen.Card: 2}
	sort.SliceStable(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return order[a.Kind] < order[b.Kind]
	})
	var tids []string
	for _, t := range v.Tickets {
		tids = append(tids, t.ID)
	}
	var spend map[string]int64
	if d.Role == devices.RoleOwner && len(tids) > 0 {
		spend, _ = s.opts.Store.TicketSpend(time.Time{}, tids, 0)
	}
	for _, t := range v.Tickets {
		m, _ := t.DecodeMandate()
		tv := gen.TicketView{Id: t.ID, Scope: m.Scope, Open: t.Open, Stage: t.Stage, Stopped: t.StoppedBy != ""}
		if cs := t.ChoiceList(); len(cs) > 0 && t.Stage == string(core.StageQuestion) {
			tv.Choices = &cs
		}
		if micros, ok := spend[t.ID]; ok {
			usd := float64(micros) / 1e6
			tv.SpentUsd = &usd
		}
		tv.Agent = onTicket[t.ID]
		out.Tickets = append(out.Tickets, tv)
	}
	writeJSON(w, http.StatusOK, out)
}

// GetPolicy returns the current Policy. Owner devices only.
func (s *Server) GetPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	p, err := s.opts.Store.Policy()
	if err != nil {
		s.internal(w, "load policy", err)
		return
	}
	writeJSON(w, http.StatusOK, policyView(p))
}

// UpdatePolicy stores a new Policy version. Owner devices only. The owner's
// identities and the bot list are kept as they are.
func (s *Server) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.PolicyUpdate
	if !decode(w, r, &req) {
		return
	}
	var saved policy.Policy
	var bad string
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		cur, err := tx.Policy()
		if err != nil {
			return err
		}
		next, msg := fromUpdate(cur, req)
		if msg != "" {
			bad = msg
			return nil
		}
		// What the agents kept for a delegate who was removed goes too.
		for _, d := range cur.Delegates {
			if !slices.ContainsFunc(next.Delegates, func(n policy.Delegate) bool { return n.Name == d.Name }) {
				if err := tx.ForgetPerson(core.Mandate{Role: core.RoleDelegate, Delegate: d.Name}.Person()); err != nil {
					return err
				}
			}
		}
		saved, err = tx.SavePolicy(next, s.opts.Now().UTC())
		return err
	})
	if bad != "" {
		writeError(w, http.StatusBadRequest, bad)
		return
	}
	if err != nil {
		s.internal(w, "save policy", err)
		return
	}
	s.opts.Log.Info("policy updated", "version", saved.Version, "mode", saved.Mode)
	s.opts.Broker.Publish(Event{Type: "policy.updated", Data: map[string]int{"version": saved.Version}})
	writeJSON(w, http.StatusOK, policyView(saved))
}

func fromUpdate(cur policy.Policy, req gen.PolicyUpdate) (policy.Policy, string) {
	next := policy.Policy{Owner: cur.Owner, Bots: cur.Bots}
	var lmsg string
	if next.Limits, lmsg = limitsFrom(cur.Limits, req.Limits); lmsg != "" {
		return policy.Policy{}, lmsg
	}
	switch policy.AcceptMode(req.Mode) {
	case policy.ModeOff, policy.ModeObserve, policy.ModeAuto:
		next.Mode = policy.AcceptMode(req.Mode)
	default:
		return policy.Policy{}, "mode must be off, observe or auto"
	}
	var msg string
	if next.Toolsets, msg = toolsets(req.Toolsets); msg != "" {
		return policy.Policy{}, msg
	}
	for _, dp := range req.Delegates {
		name := strings.TrimSpace(dp.Name)
		if name == "" || len(name) > 80 {
			return policy.Policy{}, "each delegate needs a name of up to 80 characters"
		}
		if _, dup := next.DelegateNamed(name); dup {
			return policy.Policy{}, "delegate names must be unique"
		}
		ts, msg := toolsets(dp.Toolsets)
		if msg != "" {
			return policy.Policy{}, msg
		}
		d := policy.Delegate{Name: name, Identities: []core.Identity{policy.AppDelegate(name)}, Toolsets: ts}
		// Which agents a delegate may use is set on the agents; a policy
		// update that leaves it out keeps it.
		if dp.Agents != nil {
			d.Agents = slices.Clone(*dp.Agents)
		} else if old, ok := cur.DelegateNamed(name); ok {
			d.Agents = old.Agents
		}
		next.Delegates = append(next.Delegates, d)
	}
	seen := map[string]bool{}
	for _, rp := range req.Rules {
		name, text := strings.TrimSpace(rp.Name), strings.TrimSpace(rp.Text)
		if name == "" || len(name) > 60 || text == "" || utf8.RuneCountInString(text) > 1000 {
			return policy.Policy{}, "each rule needs a name of up to 60 characters and text of up to 1000"
		}
		if seen[name] {
			return policy.Policy{}, "rule names must be unique"
		}
		seen[name] = true
		rule := policy.Rule{Name: name, Text: text, Enabled: rp.Enabled}
		if rp.Agents != nil {
			rule.Agents = slices.Clone(*rp.Agents)
		} else {
			for _, old := range cur.Rules {
				if old.Name == name {
					rule.Agents = old.Agents
				}
			}
		}
		next.Rules = append(next.Rules, rule)
	}
	return next, ""
}

func toolsets(in []string) ([]core.Toolset, string) {
	var out []core.Toolset
	for _, t := range in {
		ts := core.Toolset(t)
		if !ts.IsToolset() {
			return nil, "unknown toolset " + t
		}
		if !slices.Contains(out, ts) {
			out = append(out, ts)
		}
	}
	return out, ""
}

func keysOf(key string) []string {
	if key == "" {
		return nil
	}
	return []string{key}
}

func policyView(p policy.Policy) gen.PolicyView {
	v := gen.PolicyView{Version: p.Version, Mode: gen.AcceptMode(p.Mode), Toolsets: []string{}, Delegates: []gen.DelegatePolicy{}, Rules: []gen.RulePolicy{}}
	for _, t := range p.Toolsets {
		v.Toolsets = append(v.Toolsets, string(t))
	}
	for _, d := range p.Delegates {
		agents := append([]string{}, d.Agents...)
		dp := gen.DelegatePolicy{Name: d.Name, Toolsets: []string{}, Agents: &agents}
		for _, t := range d.Toolsets {
			dp.Toolsets = append(dp.Toolsets, string(t))
		}
		v.Delegates = append(v.Delegates, dp)
	}
	for _, r := range p.Rules {
		agents := append([]string{}, r.Agents...)
		v.Rules = append(v.Rules, gen.RulePolicy{Name: r.Name, Text: r.Text, Enabled: r.Enabled, Agents: &agents})
	}
	v.Limits = limitsView(p.Limits)
	return v
}

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// DeleteThread deletes a conversation for good. Owner devices only.
func (s *Server) DeleteThread(w http.ResponseWriter, r *http.Request, threadID string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	err := s.opts.Store.Tx(func(tx *store.Tx) error { return tx.DeleteThread(threadID, s.opts.Now().UTC()) })
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such conversation")
	case errors.Is(err, store.ErrBusy):
		writeError(w, http.StatusConflict, "an agent is still working in this chat; stop its task or wait until it finishes")
	case err != nil:
		s.internal(w, "delete thread", err)
	default:
		s.opts.Log.Info("conversation deleted", "thread", threadID)
		w.WriteHeader(http.StatusNoContent)
	}
}
