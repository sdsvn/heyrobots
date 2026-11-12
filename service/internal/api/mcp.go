package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/gateway"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

// refreshTimeout bounds reading a server's tools during a request.
const refreshTimeout = 45 * time.Second

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// ListMcpServers lists the owner's MCP servers. Owner devices only.
func (s *Server) ListMcpServers(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	out := gen.McpServerList{Servers: []gen.McpServer{}}
	pol, err := s.opts.Store.Policy()
	if err != nil {
		s.internal(w, "load policy", err)
		return
	}
	err = s.opts.Store.Tx(func(tx *store.Tx) error {
		ss, err := tx.MCPServers()
		if err != nil {
			return err
		}
		for _, srv := range ss {
			v, err := s.mcpView(tx, pol, srv)
			if err != nil {
				return err
			}
			out.ToReview += v.ToReview
			out.Servers = append(out.Servers, v)
		}
		return nil
	})
	if err != nil {
		s.internal(w, "list mcp servers", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateMcpServer adds an MCP server and reads its tools. Owner devices only.
func (s *Server) CreateMcpServer(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.McpServerRequest
	if !decode(w, r, &req) {
		return
	}
	if status, msg := s.addMCPServer(req); msg != "" {
		writeError(w, status, msg)
		return
	}
	s.refreshAndWrite(w, r, strings.TrimSpace(req.Name), http.StatusCreated)
}

// addMCPServer records a new server and keeps its secrets in the vault. It
// answers a status and a message for the person when it can't.
func (s *Server) addMCPServer(req gen.McpServerRequest) (int, string) {
	srv, sec, msg := newMCPServer(req, s.opts.Now().UTC())
	if msg != "" {
		return http.StatusBadRequest, msg
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

func newMCPServer(req gen.McpServerRequest, now time.Time) (store.MCPServer, gateway.Secrets, string) {
	var sec gateway.Secrets
	name := strings.TrimSpace(req.Name)
	if !core.ValidMCPName(name) {
		return store.MCPServer{}, sec, "a name starts with a lower-case letter and has up to 24 lower-case letters, digits or underscores"
	}
	srv := store.MCPServer{Name: name, Transport: string(req.Transport), CreatedAt: now, Trusted: req.Trusted != nil && *req.Trusted}
	if req.Purpose != nil {
		srv.Purpose = strings.TrimSpace(*req.Purpose)
		if utf8.RuneCountInString(srv.Purpose) > 300 {
			return store.MCPServer{}, sec, "say what the server is for in up to 300 characters"
		}
	}
	switch srv.Transport {
	case store.MCPStdio:
		if req.Command == nil || len(*req.Command) == 0 || strings.TrimSpace((*req.Command)[0]) == "" {
			return store.MCPServer{}, sec, "a stdio server needs the command that starts it"
		}
		if req.Url != nil && *req.Url != "" {
			return store.MCPServer{}, sec, "a stdio server has a command, not an address"
		}
		b, _ := json.Marshal(*req.Command)
		srv.Command = string(b)
		if req.Token != nil && *req.Token != "" {
			return store.MCPServer{}, sec, "a stdio server takes variables, not a token"
		}
		if req.Env != nil {
			if len(*req.Env) > 30 {
				return store.MCPServer{}, sec, "up to 30 variables"
			}
			for k := range *req.Env {
				if !envName.MatchString(k) {
					return store.MCPServer{}, sec, "variable names use letters, digits and underscores"
				}
			}
			sec.Env = *req.Env
		}
	case store.MCPHTTP:
		if req.Url == nil {
			return store.MCPServer{}, sec, "an http server needs its address"
		}
		srv.URL = strings.TrimSpace(*req.Url)
		if err := gateway.CheckURL(srv.URL); err != nil {
			return store.MCPServer{}, sec, err.Error()
		}
		if req.Command != nil && len(*req.Command) > 0 {
			return store.MCPServer{}, sec, "an http server has an address, not a command"
		}
		if req.Env != nil && len(*req.Env) > 0 {
			return store.MCPServer{}, sec, "an http server takes a token, not variables"
		}
		if req.Token != nil {
			sec.Token = strings.TrimSpace(*req.Token)
		}
	default:
		return store.MCPServer{}, sec, "transport must be stdio or http"
	}
	return srv, sec, ""
}

// UpdateMcpServer changes a server's purpose, trust and use. Owner devices
// only.
func (s *Server) UpdateMcpServer(w http.ResponseWriter, r *http.Request, name string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.McpServerUpdate
	if !decode(w, r, &req) {
		return
	}
	updates := map[string]any{}
	if req.Purpose != nil {
		p := strings.TrimSpace(*req.Purpose)
		if utf8.RuneCountInString(p) > 300 {
			writeError(w, http.StatusBadRequest, "say what the server is for in up to 300 characters")
			return
		}
		updates["purpose"] = p
	}
	if req.Trusted != nil {
		updates["trusted"] = *req.Trusted
	}
	var view gen.McpServer
	policyChanged := 0
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		srv, err := tx.MCPServer(name)
		if err != nil {
			return err
		}
		if len(updates) > 0 {
			if err := tx.UpdateMCPServer(name, updates); err != nil {
				return err
			}
		}
		pol, err := tx.Policy()
		if err != nil {
			return err
		}
		if req.Enabled != nil {
			ts := core.MCPToolset(name)
			has := slices.Contains(pol.Toolsets, ts)
			if *req.Enabled != has {
				next := pol
				next.Toolsets = slices.Clone(pol.Toolsets)
				if *req.Enabled {
					next.Toolsets = append(next.Toolsets, ts)
				} else {
					next.Toolsets = slices.DeleteFunc(next.Toolsets, func(t core.Toolset) bool { return t == ts })
				}
				if pol, err = tx.SavePolicy(next, s.opts.Now().UTC()); err != nil {
					return err
				}
				policyChanged = pol.Version
			}
		}
		if srv, err = tx.MCPServer(name); err != nil {
			return err
		}
		view, err = s.mcpView(tx, pol, srv)
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such server")
		return
	}
	if err != nil {
		s.internal(w, "update mcp server", err)
		return
	}
	if policyChanged > 0 {
		s.opts.Broker.Publish(Event{Type: "policy.updated", Data: map[string]int{"version": policyChanged}})
	}
	writeJSON(w, http.StatusOK, view)
}

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

// RefreshMcpServer reads a server's tools again. Owner devices only.
func (s *Server) RefreshMcpServer(w http.ResponseWriter, r *http.Request, name string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		_, err := tx.MCPServer(name)
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such server")
		return
	}
	if err != nil {
		s.internal(w, "load mcp server", err)
		return
	}
	s.refreshAndWrite(w, r, name, http.StatusOK)
}

// refreshAndWrite reads a server's tools and answers with the server. A
// server that can't be reached isn't an error: its view says why.
func (s *Server) refreshAndWrite(w http.ResponseWriter, r *http.Request, name string, status int) {
	if s.opts.MCP != nil {
		ctx, cancel := context.WithTimeout(r.Context(), refreshTimeout)
		if err := s.opts.MCP.Refresh(ctx, name); err != nil {
			s.opts.Log.Warn("read mcp tools", "server", name, "err", err)
		}
		cancel()
	}
	s.writeMCPServer(w, name, status)
}

func (s *Server) writeMCPServer(w http.ResponseWriter, name string, status int) {
	var view gen.McpServer
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		pol, err := tx.Policy()
		if err != nil {
			return err
		}
		srv, err := tx.MCPServer(name)
		if err != nil {
			return err
		}
		view, err = s.mcpView(tx, pol, srv)
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such server")
		return
	}
	if err != nil {
		s.internal(w, "load mcp server", err)
		return
	}
	writeJSON(w, status, view)
}

// ReviewMcpTool records the owner's review of one tool. Owner devices only.
func (s *Server) ReviewMcpTool(w http.ResponseWriter, r *http.Request, name, tool string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.McpToolReview
	if !decode(w, r, &req) {
		return
	}
	mode, reach := string(req.Mode), ""
	switch mode {
	case store.MCPToolRead, store.MCPToolBlocked:
	case store.MCPToolOutward:
		if req.Reach == nil {
			writeError(w, http.StatusBadRequest, "say how far the tool's actions reach: owner, others or irreversible")
			return
		}
		reach = string(*req.Reach)
		switch core.Reach(reach) {
		case core.ReachOwner, core.ReachOthers, core.ReachIrreversible:
		default:
			writeError(w, http.StatusBadRequest, "reach must be owner, others or irreversible")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "mode must be read, outward or blocked")
		return
	}
	err := s.opts.Store.Tx(func(tx *store.Tx) error { return tx.ReviewMCPTool(name, tool, mode, reach) })
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such tool")
		return
	}
	if err != nil {
		s.internal(w, "review mcp tool", err)
		return
	}
	s.opts.Log.Info("mcp tool reviewed", "server", name, "tool", tool, "mode", mode, "reach", reach)
	s.writeMCPServer(w, name, http.StatusOK)
}

func (s *Server) mcpView(tx *store.Tx, pol policy.Policy, srv store.MCPServer) (gen.McpServer, error) {
	ts := core.MCPToolset(srv.Name)
	v := gen.McpServer{
		Name: srv.Name, Toolset: string(ts), Purpose: srv.Purpose, Transport: gen.McpServerTransport(srv.Transport),
		Trusted: srv.Trusted, Enabled: slices.Contains(pol.Toolsets, ts), CreatedAt: srv.CreatedAt,
		CheckedAt: srv.CheckedAt, Tools: []gen.McpTool{},
	}
	if srv.StatusError != "" {
		v.StatusError = &srv.StatusError
	}
	if srv.Command != "" {
		var argv []string
		if json.Unmarshal([]byte(srv.Command), &argv) == nil {
			v.Command = &argv
		}
	}
	if srv.URL != "" {
		v.Url = &srv.URL
	}
	if s.opts.Vault != nil {
		_, v.HasSecrets = s.opts.Vault.Get(gateway.SecretsKey(srv.Name))
	}
	tools, err := tx.MCPTools(srv.Name)
	if err != nil {
		return v, err
	}
	for _, t := range tools {
		if t.Removed {
			continue
		}
		mode := t.Mode
		if mode == "" {
			mode = "unreviewed"
		}
		tv := gen.McpTool{
			Name: t.Name, Title: t.Title, Description: t.Description, Mode: gen.McpToolMode(mode),
			NeedsReview: t.NeedsReview(), Changed: t.ReviewedHash != "" && t.ReviewedHash != t.Hash && t.Mode != store.MCPToolBlocked,
			ReadOnlyHint: t.ReadOnlyHint, DestructiveHint: t.DestructiveHint,
		}
		if t.Reach != "" {
			r := gen.McpToolReach(t.Reach)
			tv.Reach = &r
		}
		if tv.NeedsReview {
			v.ToReview++
		}
		v.Tools = append(v.Tools, tv)
	}
	return v, nil
}
