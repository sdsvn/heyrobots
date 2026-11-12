package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/store"
)

// ListMachines lists the owner's machines that share the team, this one
// first. Owner devices only.
func (s *Server) ListMachines(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	out := gen.MachineList{Machines: []gen.Machine{}}
	c := s.opts.Cluster
	if c == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	ms, err := s.opts.Store.Members()
	if err != nil {
		s.internal(w, "list machines", err)
		return
	}
	agents, err := s.opts.Store.Agents()
	if err != nil {
		s.internal(w, "list agents", err)
		return
	}
	count := map[string]int{}
	for _, a := range agents {
		home := a.Home
		if home == "" {
			home = c.ID()
		}
		count[home]++
	}
	for _, m := range ms {
		if m.RemovedAt != nil {
			continue
		}
		v := gen.Machine{Id: m.ID, Name: m.Name, Self: m.ID == c.ID(), Online: c.Online(m), Agents: count[m.ID]}
		if m.Address != "" {
			addr := m.Address
			v.Address = &addr
		}
		if seen := c.LastSeen(m); !seen.IsZero() && !v.Self {
			v.LastSeen = &seen
		}
		if v.Self {
			out.Machines = append([]gen.Machine{v}, out.Machines...)
		} else {
			out.Machines = append(out.Machines, v)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateMachineCode shows a code another machine types to join this one.
// Owner devices only.
func (s *Server) CreateMachineCode(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	c := s.opts.Cluster
	if c == nil || c.Address() == "" {
		writeError(w, http.StatusConflict, "other machines can't reach this one: set HEYROBOTS_CLUSTER_LISTEN, or join from this machine instead")
		return
	}
	code, exp := c.NewCode()
	addr := c.Address()
	writeJSON(w, http.StatusCreated, gen.MachineCode{Code: code, Address: &addr, ExpiresAt: exp})
}

// JoinMachine joins another machine with the code it shows. Owner devices only.
func (s *Server) JoinMachine(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	if s.opts.Cluster == nil {
		writeError(w, http.StatusConflict, "machines aren't set up on this server")
		return
	}
	var req gen.JoinMachineRequest
	if !decode(w, r, &req) {
		return
	}
	addr, code := strings.TrimSpace(req.Address), strings.TrimSpace(req.Code)
	if addr == "" || code == "" {
		writeError(w, http.StatusBadRequest, "address and code are required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	m, err := s.opts.Cluster.Join(ctx, addr, code)
	if err != nil {
		writeError(w, http.StatusBadGateway, "couldn't join that machine: "+err.Error())
		return
	}
	s.opts.Log.Info("joined machine", "member", m.ID, "name", m.Name)
	writeJSON(w, http.StatusCreated, gen.Machine{Id: m.ID, Name: m.Name, Online: true, Address: &m.Address})
}

// RemoveMachine takes a machine out of the team. Owner devices only.
func (s *Server) RemoveMachine(w http.ResponseWriter, r *http.Request, machineID string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	if s.opts.Cluster == nil {
		writeError(w, http.StatusNotFound, "no such machine")
		return
	}
	err := s.opts.Cluster.Remove(machineID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such machine")
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
