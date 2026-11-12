package cluster

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/store"
)

// Kinds of shared log entries.
const (
	kindChat     = "chat"
	kindMessage  = "message"
	kindClaim    = "claim"
	kindRouted   = "routed"
	kindReply    = "reply"
	kindApproval = "approval"
)

type groupBody struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
	Removed bool     `json:"removed,omitempty"`
}

type chatBody struct {
	Key       string     `json:"key"`
	Agent     string     `json:"agent,omitempty"`
	Group     *groupBody `json:"group,omitempty"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
}

type messageBody struct {
	DeliveryID string    `json:"delivery_id"`
	Actor      string    `json:"actor"`
	Text       string    `json:"text"`
	At         time.Time `json:"at"`
	// Held says the machines still have to agree who routes it; history
	// that its own machine already handled is never routed again.
	Held bool `json:"held,omitempty"`
}

type claimBody struct {
	DeliveryID string `json:"delivery_id"`
	// Owned says one of this machine's tasks already took the message.
	Owned bool `json:"owned,omitempty"`
}

type routedBody struct {
	DeliveryID string `json:"delivery_id"`
	Agent      string `json:"agent"`
}

type replyBody struct {
	ID         uint      `json:"id"`
	Agent      string    `json:"agent,omitempty"`
	Kind       string    `json:"kind"`
	Reach      string    `json:"reach"`
	Body       string    `json:"body"`
	Status     string    `json:"status"`
	Approval   string    `json:"approval,omitempty"`
	Action     string    `json:"action,omitempty"`
	Recipients string    `json:"recipients,omitempty"`
	Details    string    `json:"details,omitempty"`
	Findings   string    `json:"findings,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type approvalBody struct {
	Home     string `json:"home"`
	EffectID uint   `json:"effect_id"`
	Approve  bool   `json:"approve"`
	Note     string `json:"note,omitempty"`
	Verified bool   `json:"verified,omitempty"`
}

// shared reports whether a Thread is one of the owner's chats, which every
// machine keeps a copy of.
func (n *Node) shared(th store.Thread) bool {
	return th.Source == string(core.SourceApp) && th.CreatedBy == n.Owner && th.Key != store.NoticesKey
}

// globalKey turns a chat's key into one every machine reads the same way.
func (n *Node) globalKey(key string) string {
	if a, ok := strings.CutPrefix(key, "agent:"); ok {
		return "agent:" + n.Global(a)
	}
	return key
}

func (n *Node) localKey(key string) string {
	if a, ok := strings.CutPrefix(key, "agent:"); ok {
		return "agent:" + n.Local(a)
	}
	return key
}

// PublishAll shares every chat with the other machines, after a join.
func (n *Node) PublishAll() {
	ids, err := n.Store.SharedThreadIDs(n.Owner)
	if err != nil {
		n.Log.Warn("cluster: list chats", "err", err)
		return
	}
	for _, id := range ids {
		n.Publish(id)
	}
}

// Publish writes what happened on this machine in a shared chat to its log:
// the chat itself, people's messages, and this machine's replies.
func (n *Node) Publish(threadID string) {
	if !n.HasPeers() {
		return
	}
	now := n.Now().UTC()
	added := false
	err := n.Store.Tx(func(tx *store.Tx) error {
		v, err := tx.ThreadView(threadID)
		if err != nil || !n.shared(v.Thread) {
			return err
		}
		th := v.Thread
		chat := chatBody{Key: n.globalKey(th.Key), Agent: n.Global(th.AgentID), CreatedBy: th.CreatedBy, CreatedAt: th.CreatedAt}
		if th.GroupID != "" {
			g, err := tx.Group(th.GroupID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if err == nil {
				gb := &groupBody{ID: g.ID, Name: g.Name, Removed: g.RemovedAt != nil}
				for _, m := range g.MemberList() {
					gb.Members = append(gb.Members, n.Global(m))
				}
				chat.Group = gb
			}
		}
		raw, _ := json.Marshal(chat)
		sum := sha256.Sum256(raw)
		ok, err := tx.AppendEvent(n.id, th.ID, kindChat, "chat:"+th.ID+":"+hex.EncodeToString(sum[:8]), chat, now)
		if err != nil {
			return err
		}
		added = added || ok
		for _, t := range v.Triggers {
			if t.Origin != "" || t.Source != string(core.SourceApp) {
				continue
			}
			ok, err := tx.AppendEvent(n.id, th.ID, kindMessage, "msg:"+t.DeliveryID,
				messageBody{DeliveryID: t.DeliveryID, Actor: t.ActorSource + ":" + t.ActorID, Text: t.Text, At: t.At, Held: t.EvalState == store.EvalHeld}, now)
			if err != nil {
				return err
			}
			added = added || ok
			if t.Owned {
				// A task here took it: the other machines stand down.
				if _, err := tx.AppendEvent(n.id, th.ID, kindClaim, "claim:"+t.DeliveryID, claimBody{DeliveryID: t.DeliveryID, Owned: true}, now); err != nil {
					return err
				}
			}
		}
		for _, e := range v.Effects {
			if e.Origin != "" || e.Status == store.EffectPending || e.Status == store.EffectSending {
				continue
			}
			agent := ""
			if e.TicketID != "" {
				if tk, err := tx.Ticket(e.TicketID); err == nil {
					agent = tk.AgentID
					if agent == "" {
						m, _ := tk.DecodeMandate()
						agent = m.LeadAgent()
					}
				}
			}
			b := replyBody{ID: e.ID, Agent: n.Global(agent), Kind: e.Kind, Reach: e.Reach, Body: e.Body, Status: e.Status, Approval: e.Approval,
				Action: e.Action, Recipients: e.Recipients, Details: e.Details, Findings: e.Findings, LastError: e.LastError, CreatedAt: e.CreatedAt}
			ok, err := tx.AppendEvent(n.id, th.ID, kindReply, "fx:"+itoa(e.ID)+":"+e.Status+":"+e.Approval, b, now)
			if err != nil {
				return err
			}
			added = added || ok
		}
		return nil
	})
	if err != nil {
		n.Log.Warn("cluster: publish", "thread", threadID, "err", err)
		return
	}
	if added {
		n.Kick()
	}
}

func itoa(u uint) string { return strconv.FormatUint(uint64(u), 10) }

// HandOff passes a message to an agent on another machine. That machine's
// own lead takes it from there, under that machine's rules.
func (n *Node) HandOff(tx *store.Tx, a store.Agent, t store.Trigger, threadID string) error {
	_, err := tx.AppendEvent(n.id, threadID, kindRouted, "routed:"+t.DeliveryID, routedBody{DeliveryID: t.DeliveryID, Agent: a.ID}, n.Now().UTC())
	n.Kick()
	return err
}

// ForwardApproval sends the owner's answer on a copied action to the
// machine that holds it.
func (n *Node) ForwardApproval(e store.Effect, approve bool, note string, verified bool) error {
	err := n.Store.Tx(func(tx *store.Tx) error {
		_, err := tx.AppendEvent(n.id, e.ThreadID, kindApproval, "ap:"+e.Origin+":"+itoa(e.RemoteID),
			approvalBody{Home: e.Origin, EffectID: e.RemoteID, Approve: approve, Note: note, Verified: verified}, n.Now().UTC())
		return err
	})
	n.Kick()
	return err
}

// apply stores events from other machines and acts on the new ones.
func (n *Node) apply(events []store.ChatEvent) error {
	changed := map[string]bool{}
	for _, e := range events {
		if e.Origin == n.id {
			continue
		}
		e.CreatedAt = n.Now().UTC()
		isNew, err := n.Store.AddEvent(e)
		if err != nil {
			return err
		}
		if !isNew {
			continue
		}
		if err := n.act(e); err != nil {
			n.Log.Warn("cluster: apply event", "origin", e.Origin, "seq", e.Seq, "kind", e.Kind, "err", err)
		}
		changed[e.ThreadID] = true
	}
	for id := range changed {
		if n.Hooks.ThreadChanged != nil {
			n.Hooks.ThreadChanged(id)
		}
	}
	return nil
}

func (n *Node) act(e store.ChatEvent) error {
	switch e.Kind {
	case kindChat:
		var b chatBody
		if err := e.Decode(&b); err != nil {
			return err
		}
		th := store.Thread{ID: e.ThreadID, Source: string(core.SourceApp), Key: n.localKey(b.Key), CreatedBy: b.CreatedBy, CreatedAt: b.CreatedAt, UpdatedAt: b.CreatedAt, AgentID: n.Local(b.Agent)}
		if b.Group != nil {
			g := store.Group{ID: b.Group.ID, Name: b.Group.Name, ThreadID: e.ThreadID, CreatedAt: b.CreatedAt}
			var members []string
			for _, m := range b.Group.Members {
				members = append(members, n.Local(m))
			}
			raw, _ := json.Marshal(members)
			g.Members = string(raw)
			if b.Group.Removed {
				g.RemovedAt = &b.CreatedAt
			}
			if err := n.Store.SaveCopiedGroup(g); err != nil {
				return err
			}
			th.GroupID = g.ID
		}
		_, err := n.Store.EnsureCopiedThread(th)
		return err
	case kindMessage:
		var b messageBody
		if err := e.Decode(&b); err != nil {
			return err
		}
		if err := n.ensureThread(e.ThreadID); err != nil {
			return err
		}
		src, id, _ := strings.Cut(b.Actor, ":")
		res, err := n.Intake.Ingest(intake.Message{
			Trigger: core.Trigger{DeliveryID: b.DeliveryID, Source: core.SourceApp, Actor: core.Identity{Source: core.SourceKind(src), ID: id},
				Authenticity: core.Verified, Addressed: true, Text: b.Text, At: b.At},
			ThreadIDs: []string{e.ThreadID},
			Hold:      b.Held,
			Done:      !b.Held,
		})
		if err != nil || res.Duplicate {
			return err
		}
		if err := n.Store.SetTriggerOrigin(res.TriggerID, e.Origin); err != nil {
			return err
		}
		if err := n.takeHandOff(e.ThreadID, b.DeliveryID); err != nil {
			return err
		}
		if res.Owned {
			// One of this machine's tasks took it: nobody else routes it.
			return n.Store.Tx(func(tx *store.Tx) error {
				_, err := tx.AppendEvent(n.id, e.ThreadID, kindClaim, "claim:"+b.DeliveryID, claimBody{DeliveryID: b.DeliveryID, Owned: true}, n.Now().UTC())
				return err
			})
		}
		return nil
	case kindReply:
		var b replyBody
		if err := e.Decode(&b); err != nil {
			return err
		}
		if err := n.ensureThread(e.ThreadID); err != nil {
			return err
		}
		_, err := n.Store.CopyEffect(store.Effect{ThreadID: e.ThreadID, Source: string(core.SourceApp), Kind: b.Kind, Reach: b.Reach, Body: b.Body,
			Status: b.Status, Approval: b.Approval, Action: b.Action, Recipients: b.Recipients, Details: b.Details, Findings: b.Findings,
			LastError: b.LastError, CreatedAt: b.CreatedAt, Origin: e.Origin, RemoteID: b.ID, AgentID: n.Local(b.Agent)})
		return err
	case kindApproval:
		var b approvalBody
		if err := e.Decode(&b); err != nil || b.Home != n.id || n.Hooks.Decide == nil {
			return err
		}
		return n.Hooks.Decide(b.EffectID, b.Approve, b.Note, b.Verified, e.Origin)
	case kindRouted:
		var b routedBody
		if err := e.Decode(&b); err != nil {
			return err
		}
		return n.takeHandOff(e.ThreadID, b.DeliveryID)
	}
	// Claims are read when deciding who routes.
	return nil
}

// takeHandOff gives a message to Eval when another machine handed it to one
// of this machine's agents, with that agent as its lead.
func (n *Node) takeHandOff(threadID, deliveryID string) error {
	routed, err := n.Store.ThreadEvents(threadID, kindRouted)
	if err != nil {
		return err
	}
	for _, e := range routed {
		var b routedBody
		if e.Decode(&b) != nil || b.DeliveryID != deliveryID || !strings.HasPrefix(b.Agent, n.id+".") {
			continue
		}
		t, err := n.Store.TriggerByDelivery(string(core.SourceApp), deliveryID)
		if errors.Is(err, store.ErrNotFound) {
			return nil // the message itself comes later
		}
		if err != nil {
			return err
		}
		ok, err := n.Store.ReleaseHandedOff(t.ID, n.Local(b.Agent))
		if ok && n.Hooks.KickEval != nil {
			n.Hooks.KickEval()
		}
		return err
	}
	return nil
}

// ensureThread makes a plain copy of a chat whose own entry hasn't arrived yet.
func (n *Node) ensureThread(id string) error {
	err := n.Store.Tx(func(tx *store.Tx) error { _, err := tx.Thread(id); return err })
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	now := n.Now().UTC()
	_, err = n.Store.EnsureCopiedThread(store.Thread{ID: id, Source: string(core.SourceApp), Key: "chat:" + id, CreatedBy: n.Owner, CreatedAt: now, UpdatedAt: now})
	return err
}

// rank is this machine's place among the reachable machines, newest copy first.
func (n *Node) rank() (int, int, error) {
	ms, err := n.Store.Members()
	if err != nil {
		return 0, 0, err
	}
	heads, err := n.Store.Heads()
	if err != nil {
		return 0, 0, err
	}
	var mine int64
	for _, h := range heads {
		mine += h
	}
	type cand struct {
		id   string
		have int64
	}
	cs := []cand{{n.id, mine}}
	for _, m := range ms {
		if m.ID != n.id && n.Online(m) {
			cs = append(cs, cand{m.ID, m.Have})
		}
	}
	slices.SortFunc(cs, func(a, b cand) int {
		if a.have != b.have {
			if a.have > b.have {
				return -1
			}
			return 1
		}
		return strings.Compare(a.id, b.id)
	})
	return slices.IndexFunc(cs, func(c cand) bool { return c.id == n.id }), len(cs), nil
}

// decide works through held messages: hand those routed to this machine's
// agents to Eval, claim the ones this machine should route, and route the
// ones whose claim it won.
func (n *Node) decide() error {
	now := n.Now().UTC()
	// Hand-offs to this machine's agents, in case one arrived before a
	// restart and wasn't acted on.
	routed, err := n.Store.RecentEvents(kindRouted, now.Add(-window))
	if err != nil {
		return err
	}
	for _, e := range routed {
		var b routedBody
		if e.Origin != n.id && e.Decode(&b) == nil && strings.HasPrefix(b.Agent, n.id+".") {
			if err := n.takeHandOff(e.ThreadID, b.DeliveryID); err != nil {
				return err
			}
		}
	}
	held, err := n.Store.HeldTriggers(now.Add(-window))
	if err != nil || len(held) == 0 {
		return err
	}
	rank, reachable, err := n.rank()
	if err != nil {
		return err
	}
	kick := false
	for _, t := range held {
		threads, err := n.threadsOf(t)
		if err != nil || len(threads) == 0 {
			continue
		}
		thread := threads[0]
		routed, err := n.Store.ThreadEvents(thread, kindRouted)
		if err != nil {
			return err
		}
		lead := ""
		for _, e := range routed {
			var b routedBody
			if e.Decode(&b) == nil && b.DeliveryID == t.DeliveryID && strings.HasPrefix(b.Agent, n.id+".") {
				lead = n.Local(b.Agent)
			}
		}
		if lead != "" {
			if ok, err := n.Store.ReleaseHeld(t.ID, lead); err != nil {
				return err
			} else if ok {
				kick = true
			}
			continue
		}
		claims, err := n.claims(thread, t.DeliveryID)
		if err != nil {
			return err
		}
		if len(claims) == 0 {
			if now.Sub(t.CreatedAt) >= time.Duration(rank)*ClaimWindow {
				err := n.Store.Tx(func(tx *store.Tx) error {
					_, err := tx.AppendEvent(n.id, thread, kindClaim, "claim:"+t.DeliveryID, claimBody{DeliveryID: t.DeliveryID}, now)
					return err
				})
				if err != nil {
					return err
				}
				n.Kick()
			}
			continue
		}
		win := claims[0]
		if win.Origin != n.id {
			// Another machine routes it; this copy stays held.
			continue
		}
		if reachable == 1 || now.Sub(win.CreatedAt) >= ClaimWindow || n.allSeen(win.Seq) {
			if ok, err := n.Store.ReleaseHeld(t.ID, ""); err != nil {
				return err
			} else if ok {
				kick = true
			}
		}
	}
	if kick && n.Hooks.KickEval != nil {
		n.Hooks.KickEval()
	}
	return nil
}

// claims returns the claims on one message, the winner first: a machine
// whose task already took it, else the earliest by clock, then member id.
func (n *Node) claims(threadID, deliveryID string) ([]store.ChatEvent, error) {
	all, err := n.Store.ThreadEvents(threadID, kindClaim)
	if err != nil {
		return nil, err
	}
	var out []store.ChatEvent
	for _, e := range all {
		var b claimBody
		if e.Decode(&b) == nil && b.DeliveryID == deliveryID {
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, func(a, b store.ChatEvent) int {
		var ab, bb claimBody
		_ = a.Decode(&ab)
		_ = b.Decode(&bb)
		if ab.Owned != bb.Owned {
			if ab.Owned {
				return -1
			}
			return 1
		}
		if a.HLC != b.HLC {
			if a.HLC < b.HLC {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Origin, b.Origin)
	})
	return out, nil
}

func (n *Node) threadsOf(t store.Trigger) ([]string, error) {
	var ids []string
	err := n.Store.Tx(func(tx *store.Tx) error {
		var err error
		ids, err = tx.ThreadIDsOf(t.ID)
		return err
	})
	return ids, err
}

// allSeen reports whether every reachable machine has already shown, in an
// exchange, that it holds this machine's event seq. An exchange carries both
// ways, so any claim it made before seeing this one is here by then.
func (n *Node) allSeen(seq int64) bool {
	ms, err := n.Store.Members()
	if err != nil {
		return false
	}
	for _, m := range ms {
		if m.ID == n.id || !n.Online(m) {
			continue
		}
		n.mu.Lock()
		heads, ok := n.peerHeads[m.ID]
		n.mu.Unlock()
		if !ok || heads[n.id] < seq {
			return false
		}
	}
	return true
}
