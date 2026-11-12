package cluster

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"heyrobots/service/internal/store"
)

// maxEvents caps how many log entries one exchange carries each way.
const maxEvents = 500

// info is one member as the others see it.
type info struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Address   string     `json:"address,omitempty"`
	PublicKey string     `json:"public_key"`
	Cards     []Card     `json:"cards,omitempty"`
	CardsAt   time.Time  `json:"cards_at"`
	Have      int64      `json:"have"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
	RemovedAt *time.Time `json:"removed_at,omitempty"`
}

// message is what one side of an exchange sends.
type message struct {
	From   info              `json:"from"`
	Roster []info            `json:"roster"`
	Heads  map[string]int64  `json:"heads"`
	Events []store.ChatEvent `json:"events,omitempty"`
}

type pairRequest struct {
	Code   string `json:"code"`
	Member info   `json:"member"`
}

func keyOf(cs tls.ConnectionState) string {
	if len(cs.PeerCertificates) == 0 {
		return ""
	}
	pub, ok := cs.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
	if !ok {
		return ""
	}
	return base64.StdEncoding.EncodeToString(pub)
}

func (n *Node) serve(ctx context.Context, ln net.Listener) error {
	cfg := &tls.Config{Certificates: []tls.Certificate{n.cert}, ClientAuth: tls.RequireAnyClientCert, MinVersion: tls.VersionTLS13}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /cluster/pair", n.handlePair)
	mux.HandleFunc("POST /cluster/exchange", n.handleExchange)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, TLSConfig: cfg}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	err := srv.Serve(tls.NewListener(ln, cfg))
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (n *Node) handlePair(w http.ResponseWriter, r *http.Request) {
	var req pairRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// The key the request is signed with is the one that joins.
	if req.Member.PublicKey == "" || req.Member.PublicKey != keyOf(*r.TLS) || req.Member.ID != idOf(req.Member.PublicKey) {
		http.Error(w, "the member's key doesn't match its link", http.StatusForbidden)
		return
	}
	if !n.useCode(req.Code) {
		http.Error(w, "that code is wrong or has expired", http.StatusForbidden)
		return
	}
	now := n.Now().UTC()
	req.Member.LastSeen = &now
	if err := n.merge(req.Member, true); err != nil {
		n.Log.Error("cluster: pair", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	n.Log.Info("cluster: machine joined", "member", req.Member.ID, "name", req.Member.Name)
	n.PublishAll()
	out, err := n.outgoing(nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

func (n *Node) handleExchange(w http.ResponseWriter, r *http.Request) {
	m, ok := n.memberByKey(keyOf(*r.TLS))
	if !ok {
		http.Error(w, "not a member", http.StatusForbidden)
		return
	}
	var in message
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&in); err != nil || in.From.ID != m.ID {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := n.receive(in); err != nil {
		n.Log.Warn("cluster: receive", "member", m.ID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	events, err := n.Store.EventsAfter(in.Heads, maxEvents)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out, err := n.outgoing(events)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func idOf(publicKey string) string {
	pub, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(pub)
	return "m" + hex.EncodeToString(sum[:6])
}

func (n *Node) memberByKey(key string) (store.Member, bool) {
	if key == "" {
		return store.Member{}, false
	}
	ms, err := n.Store.Members()
	if err != nil {
		return store.Member{}, false
	}
	for _, m := range ms {
		if m.PublicKey == key && m.ID != n.id && m.RemovedAt == nil {
			return m, true
		}
	}
	return store.Member{}, false
}

// client dials a member and accepts only the key it expects; an empty key
// accepts any, for the first contact when joining.
func (n *Node) client(key string, seen *string) *http.Client {
	cfg := &tls.Config{Certificates: []tls.Certificate{n.cert}, MinVersion: tls.VersionTLS13, InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			got := keyOf(cs)
			if got == "" || (key != "" && got != key) {
				return errors.New("the machine's key isn't the one paired")
			}
			if seen != nil {
				*seen = got
			}
			return nil
		}}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg, ForceAttemptHTTP2: true}, Timeout: 30 * time.Second}
}

func (n *Node) post(ctx context.Context, c *http.Client, addr, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+addr+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

// Join pairs this machine with the one at addr, using the code it shows.
func (n *Node) Join(ctx context.Context, addr, code string) (store.Member, error) {
	self, err := n.selfInfo()
	if err != nil {
		return store.Member{}, err
	}
	var key string
	var out message
	if err := n.post(ctx, n.client("", &key), addr, "/cluster/pair", pairRequest{Code: code, Member: self}, &out); err != nil {
		return store.Member{}, err
	}
	if out.From.PublicKey != key || out.From.ID != idOf(key) {
		return store.Member{}, errors.New("the machine's key doesn't match its link")
	}
	if out.From.Address == "" {
		out.From.Address = addr
	}
	if err := n.receive(out); err != nil {
		return store.Member{}, err
	}
	n.PublishAll()
	n.Kick()
	ms, err := n.Store.Members()
	if err != nil {
		return store.Member{}, err
	}
	for _, m := range ms {
		if m.ID == out.From.ID {
			return m, nil
		}
	}
	return store.Member{}, store.ErrNotFound
}

// exchange swaps presence, cards and missing log entries with one member.
func (n *Node) exchange(ctx context.Context, m store.Member) error {
	n.mu.Lock()
	known := n.peerHeads[m.ID]
	n.mu.Unlock()
	var events []store.ChatEvent
	if known != nil {
		var err error
		if events, err = n.Store.EventsAfter(known, maxEvents); err != nil {
			return err
		}
	}
	out, err := n.outgoing(events)
	if err != nil {
		return err
	}
	var in message
	if err := n.post(ctx, n.client(m.PublicKey, nil), m.Address, "/cluster/exchange", out, &in); err != nil {
		return err
	}
	if in.From.ID != m.ID {
		return errors.New("the machine answered as another member")
	}
	if in.From.Address == "" {
		in.From.Address = m.Address
	}
	return n.receive(in)
}

// receive merges what a member sent: who is around, their cards, new events.
func (n *Node) receive(in message) error {
	now := n.Now().UTC()
	in.From.LastSeen = &now
	n.mu.Lock()
	n.seen[in.From.ID] = now
	n.peerHeads[in.From.ID] = in.Heads
	n.mu.Unlock()
	if err := n.merge(in.From, true); err != nil {
		return err
	}
	for _, r := range in.Roster {
		if err := n.merge(r, false); err != nil {
			return err
		}
	}
	return n.apply(in.Events)
}

// merge folds what one member says about another into this machine's view.
func (n *Node) merge(in info, direct bool) error {
	if in.ID == n.id || in.ID == "" || in.ID != idOf(in.PublicKey) {
		return nil
	}
	ms, err := n.Store.Members()
	if err != nil {
		return err
	}
	var cur store.Member
	found := false
	for _, m := range ms {
		if m.ID == in.ID {
			cur, found = m, true
		}
	}
	if !found {
		cur = store.Member{ID: in.ID, PublicKey: in.PublicKey, CreatedAt: n.Now().UTC()}
	}
	if cur.RemovedAt != nil {
		// A removed machine stays removed.
		return nil
	}
	changed := !found
	if in.RemovedAt != nil {
		cur.RemovedAt = in.RemovedAt
		if err := n.Store.SaveMember(cur); err != nil {
			return err
		}
		return n.Store.SyncRemoteAgents(cur.ID, nil, n.Now().UTC())
	}
	if in.LastSeen != nil && (cur.LastSeen == nil || in.LastSeen.Sub(*cur.LastSeen) > Unreachable/3) {
		cur.LastSeen, changed = in.LastSeen, true
	}
	if direct || in.CardsAt.After(cur.CardsAt) {
		if cur.Name != in.Name || cur.Address != in.Address || cur.Have != in.Have {
			cur.Name, cur.Have, changed = in.Name, in.Have, true
			if in.Address != "" {
				cur.Address = in.Address
			}
		}
	}
	if in.CardsAt.After(cur.CardsAt) {
		raw, _ := json.Marshal(in.Cards)
		cur.Cards, cur.CardsAt, changed = string(raw), in.CardsAt, true
		agents := make([]store.Agent, 0, len(in.Cards))
		for _, c := range in.Cards {
			agents = append(agents, c.Agent)
		}
		if err := n.Store.SyncRemoteAgents(cur.ID, agents, n.Now().UTC()); err != nil {
			return err
		}
	}
	if !changed {
		return nil
	}
	return n.Store.SaveMember(cur)
}

// selfInfo describes this machine: its own agents and how much it holds.
func (n *Node) selfInfo() (info, error) {
	agents, err := n.Store.Agents()
	if err != nil {
		return info{}, err
	}
	loads, err := n.Store.AgentLoads()
	if err != nil {
		return info{}, err
	}
	var cards []Card
	for _, a := range agents {
		if a.Home != "" {
			continue
		}
		a.Instructions, a.Provider, a.Model, a.BaseURL = "", "", "", ""
		cards = append(cards, Card{Agent: a, Working: len(loads[a.ID].Running)})
	}
	raw, _ := json.Marshal(cards)
	sum := sha256.Sum256(raw)
	heads, err := n.Store.Heads()
	if err != nil {
		return info{}, err
	}
	var have int64
	for _, h := range heads {
		have += h
	}
	now := n.Now().UTC()
	n.mu.Lock()
	if h := hex.EncodeToString(sum[:]); h != n.cardsHash {
		n.cardsHash, n.cardsAt = h, now
	}
	at := n.cardsAt
	n.mu.Unlock()
	return info{ID: n.id, Name: n.name, Address: n.address, PublicKey: base64.StdEncoding.EncodeToString(n.key.Public().(ed25519.PublicKey)),
		Cards: cards, CardsAt: at, Have: have, LastSeen: &now}, nil
}

// outgoing is this machine's side of an exchange.
func (n *Node) outgoing(events []store.ChatEvent) (message, error) {
	self, err := n.selfInfo()
	if err != nil {
		return message{}, err
	}
	heads, err := n.Store.Heads()
	if err != nil {
		return message{}, err
	}
	ms, err := n.Store.Members()
	if err != nil {
		return message{}, err
	}
	out := message{From: self, Heads: heads, Events: events}
	for _, m := range ms {
		if m.ID == n.id {
			continue
		}
		var cards []Card
		_ = json.Unmarshal([]byte(m.Cards), &cards)
		seen := n.lastSeen(m)
		r := info{ID: m.ID, Name: m.Name, Address: m.Address, PublicKey: m.PublicKey, Cards: cards, CardsAt: m.CardsAt, Have: m.Have, RemovedAt: m.RemovedAt}
		if !seen.IsZero() {
			r.LastSeen = &seen
		}
		out.Roster = append(out.Roster, r)
	}
	return out, nil
}
