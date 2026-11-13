package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"unicode"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/catalog"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/gateway"
	"heyrobots/service/internal/google"
	"heyrobots/service/internal/store"
)

// Probe holds where the catalogue looks things up.
type Probe struct {
	GitHubAPI   string
	GitHubRaw   string
	OllamaURL   string
	LMStudioURL string
	// LookPath finds a program, and GOOS is the operating system.
	LookPath func(string) (string, error)
	GOOS     string
	// SignedIn replaces asking Claude Code whether heyrobots is signed in.
	SignedIn func() bool
}

func (p Probe) withDefaults() Probe {
	def := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	def(&p.GitHubAPI, "https://api.github.com")
	def(&p.GitHubRaw, "https://raw.githubusercontent.com")
	def(&p.OllamaURL, "http://localhost:11434")
	def(&p.LMStudioURL, "http://localhost:1234")
	def(&p.GOOS, runtime.GOOS)
	if p.LookPath == nil {
		p.LookPath = exec.LookPath
	}
	return p
}

// appEntry is one app as the owner has it now.
type appEntry struct {
	view gen.App
	cat  catalog.App
	// server is the MCP server that backs it, if one is registered.
	server *store.MCPServer
	tools  []store.MCPTool
	acc    *store.Account
}

// appEntries merges the catalogue with what is connected: the Google
// account, registered MCP servers and local apps.
func (s *Server) appEntries() ([]appEntry, error) {
	var acc *store.Account
	var servers []store.MCPServer
	tools := map[string][]store.MCPTool{}
	enabled := map[string]bool{}
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		as, err := tx.Accounts()
		if err != nil {
			return err
		}
		for _, a := range as {
			if a.Provider == "google" {
				acc = &a
			}
		}
		if servers, err = tx.MCPServers(); err != nil {
			return err
		}
		for _, srv := range servers {
			if tools[srv.Name], err = tx.MCPTools(srv.Name); err != nil {
				return err
			}
		}
		ls, err := tx.LocalApps()
		for _, l := range ls {
			enabled[l.ID] = true
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	agents, err := s.opts.Store.Agents()
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	var out []appEntry
	for _, c := range catalog.Apps() {
		e := appEntry{cat: c, view: gen.App{Id: c.ID, Name: c.Name, Category: gen.AppCategory(c.Category), Kind: gen.AppKind(c.Kind),
			Mark: c.Mark, Tint: gen.AppTint(c.Tint), Status: gen.AppStatusOff, Summary: c.About, Accounts: []string{}}}
		toolset := core.Toolset(c.Toolset)
		switch {
		case c.Coming:
			e.view.Summary = "Coming soon"
		case c.Kind == catalog.KindGoogle:
			if acc != nil {
				e.acc = acc
				e.view.Connected, e.view.Accounts, e.view.Status = true, []string{acc.Email}, gen.AppStatusOn
				e.view.Summary = acc.Email + " · reads only"
				if google.HasScope(*acc, google.ScopeGmailSend) {
					e.view.Summary = acc.Email + " · sends after your OK"
				}
				if acc.Status == store.AccountBroken {
					e.view.Status, e.view.Summary = gen.AppStatusBroken, acc.Email+" · sign in again"
				}
			}
		case c.Kind == catalog.KindMCP:
			toolset = core.MCPToolset(c.ID)
			for i, srv := range servers {
				if srv.Name == c.ID || (c.URL != "" && srv.URL == c.URL) {
					used[srv.Name] = true
					e.server, e.tools = &servers[i], tools[srv.Name]
					toolset = core.MCPToolset(srv.Name)
					mcpState(&e)
					break
				}
			}
		case c.Kind == catalog.KindLocal:
			if s.localFound(c) {
				e.view.Status, e.view.Summary = gen.AppStatusFound, "On this Mac"
				if enabled[c.ID] && localAccess(c) == nil {
					e.view.Status, e.view.Connected = gen.AppStatusOn, true
				}
			}
		}
		e.view.Agents = agentsWith(agents, toolset)
		e.view.Toolset = string(toolset)
		out = append(out, e)
	}
	// Servers the owner added by address are apps too.
	for i, srv := range servers {
		if used[srv.Name] {
			continue
		}
		name := srv.Title
		if name == "" {
			name = srv.Name
		}
		e := appEntry{server: &servers[i], tools: tools[srv.Name], view: gen.App{Id: srv.Name, Name: name,
			Category: gen.AppCategoryOther, Kind: gen.Mcp, Mark: markOf(name), Tint: gen.AppTintInk, Accounts: []string{}}}
		mcpState(&e)
		e.view.Agents = agentsWith(agents, core.MCPToolset(srv.Name))
		e.view.Toolset = string(core.MCPToolset(srv.Name))
		out = append(out, e)
	}
	return out, nil
}

// mcpState fills an app's status from its server: broken when the last
// sync failed, review when tools wait for the owner.
func mcpState(e *appEntry) {
	e.view.Connected, e.view.Status = true, gen.AppStatusOn
	e.view.Summary = "Reads on its own · changes after your OK"
	if e.server.Transport == store.MCPStdio {
		var cmd []string
		_ = json.Unmarshal([]byte(e.server.Command), &cmd)
		here, line := true, strings.Join(cmd, " ")
		e.view.RunsHere, e.view.Command = &here, &line
	}
	for _, t := range e.tools {
		if t.NeedsReview() {
			e.view.NewTools++
		}
	}
	switch {
	case e.server.StatusError != "":
		e.view.Status, e.view.Summary = gen.AppStatusBroken, "Can't reach it: "+e.server.StatusError
	case e.view.NewTools > 0:
		e.view.Status, e.view.Summary = gen.AppStatusReview, "New tools to look at"
	}
}

func agentsWith(agents []store.Agent, ts core.Toolset) []gen.AgentRef {
	out := []gen.AgentRef{}
	if ts == "" {
		return out
	}
	for _, a := range agents {
		if slices.Contains(a.ToolsetList(), ts) {
			out = append(out, gen.AgentRef{Id: a.ID, Name: a.Name, Color: a.Color, Badge: a.Badge})
		}
	}
	return out
}

// markOf makes a logo tile's letters from a name.
func markOf(name string) string {
	var out []rune
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		out = append(out, unicode.ToUpper([]rune(w)[0]))
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

func (s *Server) localFound(c catalog.App) bool {
	switch c.Probe {
	case catalog.ProbeClaudeCode:
		_, err := s.opts.Probe.LookPath("claude")
		return err == nil
	case catalog.ProbeDarwin:
		return s.opts.Probe.GOOS == "darwin"
	}
	return false
}

// localAccess checks that heyrobots can really read what a local app needs.
// macOS refuses until the owner grants access in System Settings.
func localAccess(c catalog.App) error {
	if c.Access == "" {
		return nil
	}
	p := c.Access
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		p = filepath.Join(home, rest)
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.IsDir() {
		_, err = f.Readdirnames(1)
		return err
	}
	_, err = f.Read(make([]byte, 1))
	return err
}

func (s *Server) findApp(w http.ResponseWriter, id string) (appEntry, bool) {
	es, err := s.appEntries()
	if err != nil {
		s.internal(w, "list apps", err)
		return appEntry{}, false
	}
	for _, e := range es {
		if e.view.Id == id {
			return e, true
		}
	}
	writeError(w, http.StatusNotFound, "no such app")
	return appEntry{}, false
}

// ListApps lists the catalogue with what is connected. Owner devices only.
func (s *Server) ListApps(w http.ResponseWriter, r *http.Request, p gen.ListAppsParams) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	es, err := s.appEntries()
	if err != nil {
		s.internal(w, "list apps", err)
		return
	}
	q := ""
	if p.Q != nil {
		q = strings.ToLower(strings.TrimSpace(*p.Q))
	}
	out := gen.AppList{Apps: []gen.App{}}
	for _, e := range es {
		if strings.Contains(strings.ToLower(e.view.Name), q) {
			out.Apps = append(out.Apps, e.view)
		}
	}
	if q != "" {
		out.Apps = append(out.Apps, s.registryApps(r.Context(), q, es)...)
	}
	writeJSON(w, http.StatusOK, out)
}

// ownData matches tools that only change the owner's own things.
var ownData = regexp.MustCompile(`(?i)(archive|label|star|flag|pin|snooze|mark_?as)`)

// GetApp shows one app's tools and who uses it. Owner devices only.
func (s *Server) GetApp(w http.ResponseWriter, r *http.Request, appID string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	e, ok := s.findApp(w, appID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, gen.AppDetail{App: e.view, Tools: appTools(e), Recent: []gen.Recent{}})
}

func appTools(e appEntry) []gen.AppTool {
	out := []gen.AppTool{}
	if e.cat.Kind == catalog.KindGoogle {
		send := gen.AppToolModeAfterOk
		if e.acc == nil || !google.HasScope(*e.acc, google.ScopeGmailSend) {
			send = gen.AppToolModeOff
		}
		if e.cat.ID == "gmail" {
			return append(out,
				gen.AppTool{Name: "read_mail", Title: "Read and search mail", Summary: "Runs on its own.", Mode: gen.AppToolModeReads},
				gen.AppTool{Name: "send_mail", Title: "Send and reply", Summary: "Waits for your OK.", Mode: send})
		}
		return append(out,
			gen.AppTool{Name: "read_calendar", Title: "Read your calendar", Summary: "Runs on its own.", Mode: gen.AppToolModeReads},
			gen.AppTool{Name: "add_event", Title: "Add events", Summary: "Waits for your OK.", Mode: send})
	}
	for _, t := range e.tools {
		if t.Removed {
			continue
		}
		v := gen.AppTool{Name: t.Name, Title: t.Title, Summary: firstSentence(t.Description), Switchable: ownData.MatchString(t.Name), Mode: gen.AppToolModeOff}
		if v.Title == "" {
			v.Title = strings.ReplaceAll(t.Name, "_", " ")
			v.Title = strings.ToUpper(v.Title[:1]) + v.Title[1:]
		}
		if !t.NeedsReview() {
			switch t.Mode {
			case store.MCPToolRead:
				v.Mode = gen.AppToolModeReads
				if v.Switchable {
					v.Mode = gen.AppToolModeOwn
				}
			case store.MCPToolOutward:
				v.Mode = gen.AppToolModeAfterOk
			}
		}
		out = append(out, v)
	}
	return out
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".\n"); i >= 0 {
		s = s[:i+1]
	}
	if r := []rune(s); len(r) > 120 {
		s = string(r[:119]) + "…"
	}
	return strings.TrimSuffix(s, "\n")
}

// SetAppTool changes how one of an MCP app's tools may be used, as a tool
// review. Owner devices only.
func (s *Server) SetAppTool(w http.ResponseWriter, r *http.Request, appID, toolName string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.AppToolUpdate
	if !decode(w, r, &req) {
		return
	}
	e, ok := s.findApp(w, appID)
	if !ok {
		return
	}
	if e.server == nil {
		writeError(w, http.StatusBadRequest, "this app's tools can't be changed")
		return
	}
	mode, reach := store.MCPToolBlocked, ""
	switch req.Mode {
	case gen.AppToolUpdateModeReads:
		mode = store.MCPToolRead
	case gen.AppToolUpdateModeOwn:
		if !ownData.MatchString(toolName) {
			writeError(w, http.StatusBadRequest, "only tools that change your own things can run on their own")
			return
		}
		mode = store.MCPToolRead
	case gen.AppToolUpdateModeAfterOk:
		mode, reach = store.MCPToolOutward, string(core.ReachOthers)
	case gen.AppToolUpdateModeOff:
	default:
		writeError(w, http.StatusBadRequest, "mode must be reads, after_ok, own or off")
		return
	}
	err := s.opts.Store.Tx(func(tx *store.Tx) error { return tx.ReviewMCPTool(e.server.Name, toolName, mode, reach) })
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such tool")
		return
	}
	if err != nil {
		s.internal(w, "review mcp tool", err)
		return
	}
	s.opts.Log.Info("app tool set", "app", appID, "tool", toolName, "mode", mode)
	if e, ok = s.findApp(w, appID); !ok {
		return
	}
	for _, t := range appTools(e) {
		if t.Name == toolName {
			writeJSON(w, http.StatusOK, t)
			return
		}
	}
	writeError(w, http.StatusNotFound, "no such tool")
}

// ConnectApp starts connecting an app. Owner devices only.
func (s *Server) ConnectApp(w http.ResponseWriter, r *http.Request, appID string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.ConnectRequest
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	c, known := catalog.Find(appID)
	switch {
	case strings.HasPrefix(appID, registryPrefix):
		s.connectRegistry(w, r, appID, req)
	case known && len(c.Command) > 0:
		s.connectPackage(w, r, c.ID, c.Command, nil, req.Settings, nil)
	case known && c.Coming:
		writeError(w, http.StatusBadRequest, c.Name+" isn't available yet")
	case known && c.Kind == catalog.KindGoogle:
		s.connectGoogle(w, appID)
	case known && c.Kind == catalog.KindLocal:
		s.connectLocal(w, c)
	case known:
		s.connectMCP(w, r, appID, c.URL, req.Token)
	case req.Url != nil:
		s.connectMCP(w, r, appID, strings.TrimSpace(*req.Url), req.Token)
	default:
		// A server added earlier by address.
		if _, ok := s.findApp(w, appID); ok {
			s.connectMCP(w, r, appID, "", req.Token)
		}
	}
}

func (s *Server) connectResult(w http.ResponseWriter, id string, res gen.ConnectResult) {
	e, ok := s.findApp(w, id)
	if !ok {
		return
	}
	res.App = e.view
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) connectGoogle(w http.ResponseWriter, id string) {
	g := s.opts.Google
	if g == nil {
		writeError(w, http.StatusBadRequest, "Google accounts aren't available on this server")
		return
	}
	if _, err := g.Client(); err != nil {
		yes := true
		s.connectResult(w, id, gen.ConnectResult{NeedsClient: &yes})
		return
	}
	u, _, err := g.Start(s.opts.GoogleRedirect, true, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.connectResult(w, id, gen.ConnectResult{OpenUrl: &u})
}

// connectLocal switches a local app on once heyrobots can really use it;
// until then it answers the steps the owner takes.
func (s *Server) connectLocal(w http.ResponseWriter, c catalog.App) {
	if !s.localFound(c) {
		writeError(w, http.StatusBadRequest, c.Name+" isn't on this machine")
		return
	}
	if localAccess(c) != nil {
		steps := c.Steps
		if len(steps) == 0 {
			steps = []string{"Allow heyrobotsd to use " + c.Name + ", then connect again."}
		}
		s.connectResult(w, c.ID, gen.ConnectResult{Steps: &steps})
		return
	}
	if err := s.opts.Store.Tx(func(tx *store.Tx) error { return tx.SetLocalApp(c.ID, true, s.opts.Now().UTC()) }); err != nil {
		s.internal(w, "enable local app", err)
		return
	}
	s.opts.Log.Info("local app connected", "app", c.ID)
	s.connectResult(w, c.ID, gen.ConnectResult{})
}

// hostName makes a server name from an address: mcp.linear.app → linear.
func hostName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	for _, label := range strings.Split(u.Hostname(), ".") {
		label = strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				return r
			case r >= 'A' && r <= 'Z':
				return r + 'a' - 'A'
			}
			return '_'
		}, label)
		if !slices.Contains([]string{"mcp", "api", "www", "server"}, label) && core.ValidMCPName(label) {
			return label
		}
	}
	return ""
}

// connectMCP registers a server, or updates its token, and reads its tools.
func (s *Server) connectMCP(w http.ResponseWriter, r *http.Request, id, addr string, token *string) {
	name := id
	_, inCatalog := catalog.Find(id)
	var existing *store.MCPServer
	if e, err := s.appEntries(); err == nil {
		for _, x := range e {
			if x.view.Id == id && x.server != nil {
				existing = x.server
			}
		}
	}
	if existing != nil {
		name = existing.Name
	} else if !inCatalog && !core.ValidMCPName(name) {
		name = hostName(addr)
	}
	if existing == nil {
		if status, msg := s.addMCPServer(name, addr, token); msg != "" {
			writeError(w, status, msg)
			return
		}
		if err := s.setToolset(core.MCPToolset(name), true); err != nil {
			s.internal(w, "enable mcp toolset", err)
			return
		}
	} else if token != nil && strings.TrimSpace(*token) != "" {
		b, _ := json.Marshal(gateway.Secrets{Token: strings.TrimSpace(*token)})
		if err := s.opts.Vault.Set(gateway.SecretsKey(name), string(b)); err != nil {
			s.internal(w, "save mcp token", err)
			return
		}
	}
	srv := s.syncMCP(r.Context(), name)
	res := gen.ConnectResult{}
	// A server that refuses heyrobots is signed in to with OAuth when it
	// offers that (Linear, Notion, Atlassian, Stripe), else it wants a
	// pasted token (GitHub).
	if e := srv.StatusError; strings.Contains(e, "Unauthorized") || strings.Contains(e, "401") || strings.Contains(e, "Forbidden") {
		if u := s.startMCPSignIn(name, srv.URL); u != "" {
			res.OpenUrl = &u
		} else {
			yes := true
			res.NeedsToken = &yes
		}
	}
	s.connectResult(w, name, res)
}

// syncMCP reads a server's tools and returns the server as it is after.
func (s *Server) syncMCP(ctx context.Context, name string) store.MCPServer {
	if s.opts.MCP != nil {
		ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
		if err := s.opts.MCP.Refresh(ctx, name); err != nil {
			s.opts.Log.Warn("read mcp tools", "server", name, "err", err)
		}
		cancel()
	}
	var srv store.MCPServer
	_ = s.opts.Store.Tx(func(tx *store.Tx) error {
		var err error
		srv, err = tx.MCPServer(name)
		return err
	})
	return srv
}

// setToolset adds a toolset to the policy or takes it out.
func (s *Server) setToolset(ts core.Toolset, on bool) error {
	version := 0
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		pol, err := tx.Policy()
		if err != nil || slices.Contains(pol.Toolsets, ts) == on {
			return err
		}
		next := pol
		next.Toolsets = slices.DeleteFunc(slices.Clone(pol.Toolsets), func(t core.Toolset) bool { return t == ts })
		if on {
			next.Toolsets = append(next.Toolsets, ts)
		}
		saved, err := tx.SavePolicy(next, s.opts.Now().UTC())
		version = saved.Version
		return err
	})
	if err == nil && version > 0 {
		s.opts.Broker.Publish(Event{Type: "policy.updated", Data: map[string]int{"version": version}})
	}
	return err
}

// DisconnectApp disconnects an app. Owner devices only.
func (s *Server) DisconnectApp(w http.ResponseWriter, r *http.Request, appID string, p gen.DisconnectAppParams) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	e, ok := s.findApp(w, appID)
	if !ok {
		return
	}
	switch {
	case e.server != nil:
		s.DeleteMcpServer(w, r, e.server.Name)
	case e.cat.Kind == catalog.KindGoogle:
		// Gmail and Google Calendar share the one Google account.
		if p.Account != nil && e.acc != nil && !strings.EqualFold(*p.Account, e.acc.Email) {
			writeError(w, http.StatusNotFound, "no such account")
			return
		}
		s.DisconnectGoogle(w, r)
	case e.cat.Kind == catalog.KindLocal:
		if err := s.opts.Store.Tx(func(tx *store.Tx) error { return tx.SetLocalApp(appID, false, s.opts.Now()) }); err != nil {
			s.internal(w, "disable local app", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
