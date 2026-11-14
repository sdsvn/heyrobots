package cluster

import (
	"errors"
	"strings"
	"time"

	"heyrobots/service/internal/store"
)

// Machine is one machine in the team, as the app and the command line
// show it.
type Machine struct {
	ID, Name, Address string
	Self, Online      bool
	// LastSeen is when another machine was last heard from.
	LastSeen time.Time
	Agents   int
}

// Machines lists the team, this machine first, without removed ones.
func (n *Node) Machines() ([]Machine, error) {
	ms, err := n.Store.Members()
	if err != nil {
		return nil, err
	}
	agents, err := n.Store.Agents()
	if err != nil {
		return nil, err
	}
	count := map[string]int{}
	for _, a := range agents {
		home := a.Home
		if home == "" {
			home = n.id
		}
		count[home]++
	}
	var out []Machine
	for _, m := range ms {
		if m.RemovedAt != nil {
			continue
		}
		v := Machine{ID: m.ID, Name: m.Name, Address: m.Address, Self: m.ID == n.id, Online: n.Online(m), Agents: count[m.ID]}
		if !v.Self {
			v.LastSeen = n.lastSeen(m)
		}
		if v.Self {
			out = append([]Machine{v}, out...)
		} else {
			out = append(out, v)
		}
	}
	return out, nil
}

// Find returns the machine with this id, or the one machine with this name
// (ignoring case).
func Find(ms []Machine, idOrName string) (Machine, bool) {
	var hit []Machine
	for _, m := range ms {
		if m.ID == idOrName {
			return m, true
		}
		if strings.EqualFold(m.Name, idOrName) {
			hit = append(hit, m)
		}
	}
	if len(hit) == 1 {
		return hit[0], true
	}
	return Machine{}, false
}

// errRemoved is a member's answer when it removed this machine.
var errRemoved = errors.New("this machine was removed from the team")

// wasRemoved reports whether a link key belongs to a removed member.
func (n *Node) wasRemoved(key string) bool {
	ms, err := n.Store.Members()
	if err != nil || key == "" {
		return false
	}
	for _, m := range ms {
		if m.PublicKey == key && m.ID != n.id && m.RemovedAt != nil {
			return true
		}
	}
	return false
}

// leave takes this machine out of the team after another member removed
// it: the others are removed here too, so it stops listing and dialling
// them. It joins again with a new code.
func (n *Node) leave(by store.Member) error {
	ms, err := n.Store.Members()
	if err != nil {
		return err
	}
	now := n.Now().UTC()
	for _, m := range ms {
		if m.ID == n.id || m.RemovedAt != nil {
			continue
		}
		m.RemovedAt = &now
		if err := n.Store.SaveMember(m); err != nil {
			return err
		}
		if err := n.Store.SyncRemoteAgents(m.ID, nil, now); err != nil {
			return err
		}
	}
	n.Log.Warn("cluster: this machine was removed from the team", "by", by.Name)
	return nil
}

// restore brings back a removed member that joins again with a code.
func (n *Node) restore(id string) error {
	ms, err := n.Store.Members()
	if err != nil {
		return err
	}
	for _, m := range ms {
		if m.ID == id && m.RemovedAt != nil {
			m.RemovedAt = nil
			return n.Store.SaveMember(m)
		}
	}
	return nil
}
