package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"time"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/devices"
	"heyrobots/service/internal/gateway"
	"heyrobots/service/internal/registry"
	"heyrobots/service/internal/store"
)

// registryPrefix starts the id of an app found in the MCP registry; the
// registry name follows with its slash as a tilde, so it fits in a path.
const registryPrefix = "registry~"

func registryID(name string) string { return registryPrefix + strings.ReplaceAll(name, "/", "~") }

func registryName(id string) string {
	return strings.Replace(strings.TrimPrefix(id, registryPrefix), "~", "/", 1)
}

// registryApps are the registry's servers matching q that aren't already
// in the catalogue or connected.
func (s *Server) registryApps(ctx context.Context, q string, have []appEntry) []gen.App {
	out := []gen.App{}
	if s.opts.Registry == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	found, err := s.opts.Registry.Search(ctx, q)
	if err != nil {
		s.opts.Log.Warn("search mcp registry", "err", err)
		return out
	}
	for _, f := range found {
		if slices.ContainsFunc(have, func(e appEntry) bool {
			return e.cat.Registry == f.Name || (f.Remote != "" && (e.cat.URL == f.Remote || (e.server != nil && e.server.URL == f.Remote)))
		}) {
			continue
		}
		out = append(out, registryView(f))
		if len(out) == 30 {
			break
		}
	}
	return out
}

func registryView(f registry.Server) gen.App {
	name := f.Title
	if name == "" {
		_, name, _ = strings.Cut(f.Name, "/")
	}
	v := gen.App{Id: registryID(f.Name), Name: name, Category: gen.AppCategoryOther, Kind: gen.Mcp, Mark: markOf(name),
		Tint: gen.AppTintInk, Status: gen.AppStatusOff, Summary: f.Description, Accounts: []string{}, Agents: []gen.AgentRef{},
		Publisher: &f.Publisher, Official: &f.Official}
	if f.Package != nil {
		here, line := true, strings.Join(f.Package.Command, " ")
		v.RunsHere, v.Command = &here, &line
	}
	return v
}

// connectRegistry connects a server found in the registry. It looks the
// server up again rather than trusting what the app sent.
func (s *Server) connectRegistry(w http.ResponseWriter, r *http.Request, id string, req gen.ConnectRequest) {
	if s.opts.Registry == nil {
		writeError(w, http.StatusBadRequest, "the MCP registry isn't available on this server")
		return
	}
	f, err := s.opts.Registry.Get(r.Context(), registryName(id))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if f.Remote != "" {
		s.connectMCP(w, r, mcpNameFor(f.Name), f.Remote, req.Token)
		return
	}
	view := registryView(f)
	s.connectPackage(w, r, mcpNameFor(f.Name), f.Package.Command, f.Package.Settings, req.Settings, &view)
}

// connectPackage starts a server that runs on this computer, such as an
// npm or PyPI package, after its required settings are given. Settings go
// to the vault as its environment.
func (s *Server) connectPackage(w http.ResponseWriter, r *http.Request, name string, cmd []string, need []registry.Setting, given *map[string]string, view *gen.App) {
	if _, err := s.opts.Probe.LookPath(cmd[0]); err != nil {
		tool := map[string]string{"npx": "Node.js", "uvx": "uv"}[cmd[0]]
		writeError(w, http.StatusBadRequest, "it needs "+orName(tool, cmd[0])+" on this computer: install it, then connect again")
		return
	}
	env := map[string]string{}
	if given != nil {
		for k, v := range *given {
			if v = strings.TrimSpace(v); v != "" && envName.MatchString(k) {
				env[k] = v
			}
		}
	}
	var missing []gen.AppSetting
	for _, n := range need {
		if n.Required && env[n.Name] == "" {
			missing = append(missing, gen.AppSetting{Name: n.Name, Description: n.Description, Secret: n.Secret, Required: true})
		}
	}
	if len(missing) > 0 {
		res := gen.ConnectResult{NeedsSettings: &missing}
		if view != nil {
			res.App = *view
		}
		writeJSON(w, http.StatusOK, res)
		return
	}
	b, _ := json.Marshal(cmd)
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		if _, err := tx.MCPServer(name); err == nil {
			return errExists
		}
		return tx.CreateMCPServer(store.MCPServer{Name: name, Transport: store.MCPStdio, Command: string(b), CreatedAt: s.opts.Now().UTC()})
	})
	if err != nil && err != errExists {
		s.internal(w, "create mcp server", err)
		return
	}
	if len(env) > 0 {
		sec, _ := json.Marshal(gateway.Secrets{Env: env})
		if err := s.opts.Vault.Set(gateway.SecretsKey(name), string(sec)); err != nil {
			s.internal(w, "save mcp settings", err)
			return
		}
	}
	if err := s.setToolset(core.MCPToolset(name), true); err != nil {
		s.internal(w, "enable mcp toolset", err)
		return
	}
	s.opts.Log.Info("mcp package connected", "server", name, "command", strings.Join(cmd, " "))
	s.syncMCP(r.Context(), name)
	s.connectResult(w, name, gen.ConnectResult{})
}

func orName(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// mcpNameFor makes a server name from a registry name: notion from
// com.notion/mcp, playwright_mcp from io.github.microsoft/playwright-mcp.
func mcpNameFor(regName string) string {
	pub, rest, _ := strings.Cut(regName, "/")
	pick := rest
	if b := brandOf(pub); rest == "mcp" || rest == "mcp-server" || strings.HasPrefix(rest, b) {
		pick = b
	}
	n := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 'a' - 'A'
		}
		return '_'
	}, pick)
	n = strings.Trim(n, "_")
	for len(n) > 0 && (n[0] < 'a' || n[0] > 'z') {
		n = n[1:]
	}
	if len(n) > 24 {
		n = strings.TrimRight(n[:24], "_")
	}
	if !core.ValidMCPName(n) {
		return "app"
	}
	return n
}

func brandOf(publisher string) string {
	parts := strings.Split(strings.ToLower(publisher), ".")
	if len(parts) > 2 && parts[0] == "io" && parts[1] == "github" {
		return parts[2]
	}
	if len(parts) > 1 {
		return parts[1]
	}
	return publisher
}

// envName is a setting's name: an environment variable.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// ConnectAddOn connects an add-on for a change the owner approved. It runs
// the same steps as the app's Connect, as the owner, and answers a sign-in
// link when the owner must sign in, or what the owner still has to do.
func (s *Server) ConnectAddOn(ctx context.Context, id string) (signIn, todo string, err error) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.WithValue(ctx, ctxKey{}, store.Device{Role: devices.RoleOwner}), http.MethodPost, "/", http.NoBody)
	s.ConnectApp(rec, req, id)
	var res gen.ConnectResult
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &res) != nil {
		var e gen.Error
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		return "", "", errors.New(orName(e.Error, "the add-on couldn't be connected"))
	}
	name := res.App.Name
	switch {
	case res.OpenUrl != nil:
		return *res.OpenUrl, "", nil
	case res.NeedsSettings != nil, res.NeedsToken != nil && *res.NeedsToken, res.NeedsClient != nil && *res.NeedsClient:
		return "", name + " needs something only you can give it, such as a token. Open Settings, then Add-ons, then " + name + ", and connect it there.", nil
	case res.Steps != nil && len(*res.Steps) > 0:
		return "", strings.Join(*res.Steps, " "), nil
	}
	return "", "", nil
}
