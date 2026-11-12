// Package cluster makes the owner's heyrobots servers act as one team.
// Each server is an equal member: it runs its own agents and its own lead,
// and keeps a full copy of every shared chat's log. Members agree without
// voting: every log entry is written once by one member, a chat message is
// routed by the reachable member whose copy is newest, and a task runs only
// on its agent's own machine. Links are mutual TLS, pinned to each
// member's ed25519 key, and only ever dialled out.
package cluster

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/store"
)

const (
	// Interval is how often a member dials the members it can reach.
	Interval = time.Second
	// Unreachable is how long a silent member stays available.
	Unreachable        = 15 * time.Second
	claimWindowDefault = 3 * time.Second
	// codeTTL is how long a join code works.
	codeTTL = 10 * time.Minute
	// window is how far back held messages are still decided.
	window = 10 * time.Minute
)

// ClaimWindow is how long a member waits for an earlier claim before it
// routes, and how much longer each next-newest copy waits to claim.
var ClaimWindow = claimWindowDefault

// Card is what other members know about one agent.
type Card struct {
	Agent   store.Agent `json:"agent"`
	Working int         `json:"working"`
}

// Hooks are what the cluster asks the rest of the server to do.
type Hooks struct {
	// ThreadChanged tells apps a chat changed.
	ThreadChanged func(threadID string)
	// KickEval wakes Eval after a message is released to it.
	KickEval func()
	// Decide applies the owner's answer to one of this machine's actions,
	// sent from another machine.
	Decide func(effectID uint, approve bool, note string, verified bool, from string) error
}

// Node is this machine's membership in the cluster.
type Node struct {
	Store  *store.Store
	Intake *intake.Intake
	Log    *slog.Logger
	Now    func() time.Time
	// Owner is the identity whose app chats are shared.
	Owner string
	Hooks Hooks

	id, name, address string
	key               ed25519.PrivateKey
	cert              tls.Certificate

	mu        sync.Mutex
	codes     map[string]time.Time
	seen      map[string]time.Time // last contact per member, in memory
	peerHeads map[string]map[string]int64
	cardsHash string
	cardsAt   time.Time
	kick      chan struct{}
}

// New loads or makes this machine's key and records it as a member.
func New(s *store.Store, in *intake.Intake, log *slog.Logger, name, address, owner string) (*Node, error) {
	key, err := loadKey(filepath.Join(s.DataDir(), "member.key"))
	if err != nil {
		return nil, err
	}
	if name == "" {
		name, _ = os.Hostname()
	}
	n := &Node{Store: s, Intake: in, Log: log, Now: time.Now, Owner: owner, key: key, name: name, address: address,
		codes: map[string]time.Time{}, seen: map[string]time.Time{}, peerHeads: map[string]map[string]int64{}, kick: make(chan struct{}, 1)}
	pub := key.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	n.id = "m" + hex.EncodeToString(sum[:6])
	if n.cert, err = selfCert(key); err != nil {
		return nil, err
	}
	now := n.Now().UTC()
	return n, s.SaveMember(store.Member{ID: n.id, Name: name, Address: address, PublicKey: base64.StdEncoding.EncodeToString(pub), CreatedAt: now, LastSeen: &now})
}

// ID is this machine's member id.
func (n *Node) ID() string { return n.id }

// Name is this machine's name.
func (n *Node) Name() string { return n.name }

// Address is where other machines dial this one, if anywhere.
func (n *Node) Address() string { return n.address }

func loadKey(path string) (ed25519.PrivateKey, error) {
	seed, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		seed = make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, seed, 0o600); err != nil {
			return nil, fmt.Errorf("save member key: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("read member key: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("member key is damaged")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func selfCert(key ed25519.PrivateKey) (tls.Certificate, error) {
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(20 * 365 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, key.Public(), key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// Global names this machine's agent so every member can tell whose it is.
func (n *Node) Global(agentID string) string {
	if agentID == "" || store.IsRemoteAgent(agentID) {
		return agentID
	}
	return n.id + "." + agentID
}

// Local turns a global agent name into this machine's id for it.
func (n *Node) Local(global string) string { return strings.TrimPrefix(global, n.id+".") }

// HasPeers reports whether any other machine shares the team.
func (n *Node) HasPeers() bool {
	ms, err := n.Store.Members()
	if err != nil {
		return false
	}
	return slices.ContainsFunc(ms, func(m store.Member) bool { return m.ID != n.id && m.RemovedAt == nil })
}

// lastSeen is when a member was last heard from, directly or through others.
func (n *Node) lastSeen(m store.Member) time.Time {
	if m.ID == n.id {
		return n.Now()
	}
	n.mu.Lock()
	t := n.seen[m.ID]
	n.mu.Unlock()
	if m.LastSeen != nil && m.LastSeen.After(t) {
		t = *m.LastSeen
	}
	return t
}

// Online reports whether a member was heard from recently.
func (n *Node) Online(m store.Member) bool {
	return m.RemovedAt == nil && n.Now().Sub(n.lastSeen(m)) < Unreachable
}

// Available reports whether another machine's agent can take work now.
func (n *Node) Available(a store.Agent) bool {
	if a.Home == "" {
		return true
	}
	ms, err := n.Store.Members()
	if err != nil {
		return false
	}
	for _, m := range ms {
		if m.ID == a.Home {
			return n.Online(m)
		}
	}
	return false
}

// Presence says whether an agent is available, busy or unavailable, and
// since when it has been unavailable.
func (n *Node) Presence(a store.Agent, working int) (string, *time.Time) {
	if a.Home != "" {
		ms, _ := n.Store.Members()
		for _, m := range ms {
			if m.ID != a.Home {
				continue
			}
			if !n.Online(m) {
				t := n.lastSeen(m)
				return "unavailable", &t
			}
			var cards []Card
			_ = json.Unmarshal([]byte(m.Cards), &cards)
			for _, c := range cards {
				if m.ID+"."+c.Agent.ID == a.ID {
					working = c.Working
				}
			}
		}
	}
	if working > 0 {
		return "busy", nil
	}
	return "available", nil
}

// Kick asks for an exchange with the other machines now.
func (n *Node) Kick() {
	select {
	case n.kick <- struct{}{}:
	default:
	}
}

// NewCode makes a code another machine types to join this one.
func (n *Node) NewCode() (string, time.Time) {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	code := base32.StdEncoding.EncodeToString(b)[:8]
	exp := n.Now().Add(codeTTL)
	n.mu.Lock()
	n.codes[code] = exp
	n.mu.Unlock()
	return code, exp
}

func (n *Node) useCode(code string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	exp, ok := n.codes[strings.ToUpper(strings.TrimSpace(code))]
	delete(n.codes, strings.ToUpper(strings.TrimSpace(code)))
	return ok && n.Now().Before(exp)
}

// Remove takes a machine out of the team; the removal spreads to the others.
func (n *Node) Remove(id string) error {
	if id == n.id {
		return errors.New("this machine can't remove itself")
	}
	ms, err := n.Store.Members()
	if err != nil {
		return err
	}
	for _, m := range ms {
		if m.ID == id && m.RemovedAt == nil {
			now := n.Now().UTC()
			m.RemovedAt = &now
			if err := n.Store.SaveMember(m); err != nil {
				return err
			}
			n.Kick()
			return n.Store.SyncRemoteAgents(id, nil, now)
		}
	}
	return store.ErrNotFound
}

// Run dials the machines it can reach and decides held messages until ctx ends.
func (n *Node) Run(ctx context.Context) {
	tick := time.NewTicker(Interval)
	defer tick.Stop()
	route := time.NewTicker(Interval / 2)
	defer route.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case <-route.C:
			if err := n.decide(); err != nil {
				n.Log.Warn("cluster: decide routing", "err", err)
			}
			continue
		case <-tick.C:
		case <-n.kick:
		}
		ms, err := n.Store.Members()
		if err != nil {
			n.Log.Warn("cluster: members", "err", err)
			continue
		}
		var wg sync.WaitGroup
		for _, m := range ms {
			if m.ID == n.id || m.RemovedAt != nil || m.Address == "" {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				if err := n.exchange(cctx, m); err != nil {
					n.Log.Debug("cluster: exchange", "member", m.ID, "err", err)
				}
			}()
		}
		wg.Wait()
	}
}

// Listen serves the link other machines dial, until ctx ends.
func (n *Node) Listen(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return n.serve(ctx, ln)
}

// MemberName names a machine, or returns its id when unknown.
func (n *Node) MemberName(id string) string {
	ms, _ := n.Store.Members()
	for _, m := range ms {
		if m.ID == id {
			return m.Name
		}
	}
	return id
}

// LastSeen is when a machine was last heard from.
func (n *Node) LastSeen(m store.Member) time.Time { return n.lastSeen(m) }
