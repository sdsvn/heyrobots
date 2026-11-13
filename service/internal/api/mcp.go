package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/gateway"
	"heyrobots/service/internal/store"
)

// refreshTimeout bounds reading a server's tools during a request.
const refreshTimeout = 45 * time.Second

// addMCPServer records a new server and keeps its secrets in the vault. It
// answers a status and a message for the person when it can't.
func (s *Server) addMCPServer(name, addr string, token *string) (int, string) {
	srv := store.MCPServer{Name: name, Transport: store.MCPHTTP, URL: strings.TrimSpace(addr), CreatedAt: s.opts.Now().UTC()}
	if !core.ValidMCPName(name) {
		return http.StatusBadRequest, "a name starts with a lower-case letter and has up to 24 lower-case letters, digits or underscores"
	}
	if err := gateway.CheckURL(srv.URL); err != nil {
		return http.StatusBadRequest, err.Error()
	}
	var sec gateway.Secrets
	if token != nil {
		sec.Token = strings.TrimSpace(*token)
	}
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		if _, err := tx.MCPServer(srv.Name); err == nil {
			return errExists
		}
		return tx.CreateMCPServer(srv)
	})
	if errors.Is(err, errExists) {
		return http.StatusConflict, "a server with that name exists"
	}
	if err != nil {
		s.opts.Log.Error("create mcp server", "err", err)
		return http.StatusInternalServerError, "the server couldn't be saved"
	}
	if len(sec.Env) > 0 || sec.Token != "" {
		b, _ := json.Marshal(sec)
		if err := s.opts.Vault.Set(gateway.SecretsKey(srv.Name), string(b)); err != nil {
			_ = s.opts.Store.Tx(func(tx *store.Tx) error { return tx.DeleteMCPServer(srv.Name) })
			s.opts.Log.Error("save mcp secrets", "err", err)
			return http.StatusInternalServerError, "the server's credentials couldn't be saved"
		}
	}
	s.opts.Log.Info("mcp server added", "server", srv.Name, "transport", srv.Transport)
	return 0, ""
}

var errExists = errors.New("exists")

// DeleteMcpServer forgets a server, its tools and its credentials, and
// takes its toolset out of the policy. Owner devices only.
func (s *Server) DeleteMcpServer(w http.ResponseWriter, r *http.Request, name string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	version := 0
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		if err := tx.DeleteMCPServer(name); err != nil {
			return err
		}
		pol, err := tx.Policy()
		if err != nil {
			return err
		}
		ts := core.MCPToolset(name)
		drop := func(t core.Toolset) bool { return t == ts }
		next, changed := pol, false
		if slices.Contains(pol.Toolsets, ts) {
			next.Toolsets, changed = slices.DeleteFunc(slices.Clone(pol.Toolsets), drop), true
		}
		next.Delegates = slices.Clone(pol.Delegates)
		for i, d := range next.Delegates {
			if slices.Contains(d.Toolsets, ts) {
				next.Delegates[i].Toolsets, changed = slices.DeleteFunc(slices.Clone(d.Toolsets), drop), true
			}
		}
		if changed {
			saved, err := tx.SavePolicy(next, s.opts.Now().UTC())
			version = saved.Version
			return err
		}
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such server")
		return
	}
	if err != nil {
		s.internal(w, "delete mcp server", err)
		return
	}
	if err := s.opts.Vault.Delete(gateway.SecretsKey(name)); err != nil {
		s.opts.Log.Warn("forget mcp secrets", "server", name, "err", err)
	}
	if version > 0 {
		s.opts.Broker.Publish(Event{Type: "policy.updated", Data: map[string]int{"version": version}})
	}
	s.opts.Log.Info("mcp server removed", "server", name)
	w.WriteHeader(http.StatusNoContent)
}
