package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/gateway"
	"heyrobots/service/internal/google"
	"heyrobots/service/internal/skills"
	"heyrobots/service/internal/store"
)

func newCatalogHarness(t *testing.T, more func(h *harness, o *Options)) (*harness, string) {
	t.Helper()
	// No test reaches the real skills.sh.
	skills.SHURL = "http://127.0.0.1:1/api/search"
	h := newHarnessWith(t, nil, func(h *harness, o *Options) {
		o.Probe = Probe{LookPath: func(string) (string, error) { return "", exec.ErrNotFound }, GOOS: "linux",
			OllamaURL: "http://127.0.0.1:1", LMStudioURL: "http://127.0.0.1:1"}
		o.Skills = &skills.Library{Dir: filepath.Join(t.TempDir(), "skills")}
		if more != nil {
			more(h, o)
		}
	})
	return h, h.completeSetup()
}

func getJSON[T any](t *testing.T, h *harness, path, token string) T {
	t.Helper()
	rec := h.do("GET", path, token, nil, "")
	var v T
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
		t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body)
	}
	return v
}

func findApp(apps []gen.App, id string) gen.App {
	for _, a := range apps {
		if a.Id == id {
			return a
		}
	}
	return gen.App{}
}

func TestListAppsShowsConnectedGmailAndMCPServers(t *testing.T) {
	h, owner := newCatalogHarness(t, nil)
	err := h.store.Tx(func(tx *store.Tx) error {
		if err := tx.SaveAccount(store.Account{Provider: "google", Email: "sam@example.com", Status: store.AccountConnected,
			Scopes: google.ScopeGmailSend, ConnectedAt: time.Now()}); err != nil {
			return err
		}
		for _, srv := range []store.MCPServer{
			{Name: "linear", Transport: store.MCPHTTP, URL: "https://mcp.linear.app/mcp"},
			{Name: "tracker", Transport: store.MCPHTTP, URL: "https://tracker.example.com/mcp", Title: "Acme Tracker"},
		} {
			if err := tx.CreateMCPServer(srv); err != nil {
				return err
			}
		}
		return tx.SyncMCPTools("linear", []store.MCPTool{{Name: "create_issue", Hash: "h"}}, time.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	ag, _ := h.store.Agent("ag_general")
	ag.ID, ag.Name, ag.Builtin, ag.AnyToolset, ag.Toolsets = "ag_pm", "PM", false, false, `["mcp:linear"]`
	if err := h.store.SaveAgent(ag); err != nil {
		t.Fatal(err)
	}

	apps := getJSON[gen.AppList](t, h, "/api/v1/apps", owner).Apps
	gmail := findApp(apps, "gmail")
	if !gmail.Connected || gmail.Status != gen.AppStatusOn || !slices.Equal(gmail.Accounts, []string{"sam@example.com"}) || !strings.Contains(gmail.Summary, "after your OK") {
		t.Fatalf("gmail = %+v", gmail)
	}
	lin := findApp(apps, "linear")
	if !lin.Connected || lin.Status != gen.AppStatusOn || lin.NewTools != 0 || len(lin.Agents) != 1 || lin.Agents[0].Name != "PM" {
		t.Fatalf("linear = %+v", lin)
	}
	if tr := findApp(apps, "tracker"); tr.Name != "Acme Tracker" || tr.Kind != gen.Mcp || tr.Mark != "AT" || !tr.Connected {
		t.Fatalf("tracker = %+v", tr)
	}
	if a := findApp(apps, "imessage"); a.Status != gen.AppStatusOff {
		t.Fatalf("imessage off linux = %+v", a)
	}
	if got := getJSON[gen.AppList](t, h, "/api/v1/apps?q=gmai", owner).Apps; len(got) != 1 || got[0].Id != "gmail" {
		t.Fatalf("q = %+v", got)
	}

	// A tool that isn't read-only starts out asking first; the owner can switch it off.
	detail := getJSON[gen.AppDetail](t, h, "/api/v1/apps/linear", owner)
	if len(detail.Tools) != 1 || detail.Tools[0].Mode != gen.AppToolModeAfterOk || detail.Tools[0].Title != "Create issue" {
		t.Fatalf("detail = %+v", detail)
	}
	rec := h.putJSON("/api/v1/apps/linear/tools/create_issue", owner, gen.AppToolUpdate{Mode: gen.AppToolUpdateModeOff})
	var tool gen.AppTool
	json.Unmarshal(rec.Body.Bytes(), &tool)
	if rec.Code != http.StatusOK || tool.Mode != gen.AppToolModeOff {
		t.Fatalf("set tool = %d %s", rec.Code, rec.Body)
	}
}

func TestConnectingAnMCPAppRegistersItAndAsksForATokenOn401(t *testing.T) {
	h, owner := newCatalogHarness(t, func(h *harness, o *Options) {
		o.MCP = &gateway.MCP{Store: h.store, Vault: h.vault, Connect: func(context.Context, store.MCPServer) (*mcp.ClientSession, error) {
			return nil, errors.New("connect: Unauthorized")
		}}
	})
	rec := h.postJSON("/api/v1/apps/linear/connect", owner, gen.ConnectRequest{})
	var res gen.ConnectResult
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || res.NeedsToken == nil || !*res.NeedsToken || res.App.Status != gen.AppStatusBroken {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body)
	}
	var srv store.MCPServer
	h.store.Tx(func(tx *store.Tx) error { srv, _ = tx.MCPServer("linear"); return nil })
	pol, _ := h.store.Policy()
	if srv.URL != "https://mcp.linear.app/mcp" || !slices.Contains(pol.Toolsets, core.MCPToolset("linear")) {
		t.Fatalf("server = %+v, toolsets = %v", srv, pol.Toolsets)
	}
}

func TestSearchSkillsFindsACuratedOne(t *testing.T) {
	h, owner := newCatalogHarness(t, nil)
	got := getJSON[gen.SkillCatalog](t, h, "/api/v1/skills/catalog?q=debugging", owner).Skills
	if len(got) != 1 || got[0].Source != "obra/superpowers/skills/systematic-debugging" || got[0].Installed {
		t.Fatalf("skills = %+v", got)
	}
}

func TestAddSkillFromGitHubInstallsIt(t *testing.T) {
	sha := strings.Repeat("ab", 20)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/kit/commits/HEAD":
			fmt.Fprint(w, sha)
		case "/repos/acme/kit/git/trees/" + sha:
			fmt.Fprint(w, `{"tree":[{"path":"skills","type":"tree","mode":"040000"},
				{"path":"skills/hello/SKILL.md","type":"blob","mode":"100644","size":60},
				{"path":"skills/hello/scripts/run.sh","type":"blob","mode":"100644","size":10},
				{"path":"skills/other/SKILL.md","type":"blob","mode":"100644","size":60}]}`)
		case "/acme/kit/" + sha + "/skills/hello/SKILL.md":
			fmt.Fprint(w, "---\nname: hello\ndescription: Says hello nicely.\n---\nBe kind.\n")
		case "/acme/kit/" + sha + "/skills/hello/scripts/run.sh":
			fmt.Fprint(w, "echo hi\n")
		case "/api/search":
			fmt.Fprint(w, `{"skills":[{"id":"acme/kit/rare","source":"acme/kit","name":"rare","installs":3},{"id":"acme/kit/hello","source":"acme/kit","name":"hello","installs":900}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer gh.Close()
	h, owner := newCatalogHarness(t, func(h *harness, o *Options) { o.Probe.GitHubAPI, o.Probe.GitHubRaw = gh.URL, gh.URL })

	found := getJSON[gen.SkillCatalog](t, h, "/api/v1/skills/catalog?q=https://github.com/acme/kit/tree/HEAD/skills/hello", owner).Skills
	if len(found) != 1 || found[0].Name != "hello" || found[0].Source != "acme/kit/skills/hello" {
		t.Fatalf("resolve = %+v", found)
	}
	// skills.sh lists it by owner/repo/name, most installed first; adding
	// it finds the skill's folder by that name.
	skills.SHURL = gh.URL + "/api/search"
	sh := getJSON[gen.SkillCatalog](t, h, "/api/v1/skills/catalog?q=hel", owner).Skills
	if len(sh) != 2 || sh[0].Source != "acme/kit/hello" || sh[0].Installs == nil || *sh[0].Installs != 900 {
		t.Fatalf("skills.sh = %+v", sh)
	}
	rec := h.postJSON("/api/v1/skills/add", owner, gen.AddSkillRequest{Source: "acme/kit/hello"})
	var sk gen.Skill
	json.Unmarshal(rec.Body.Bytes(), &sk)
	if rec.Code != http.StatusOK || sk.Name != "hello" || !sk.HasScripts || sk.Files != 2 {
		t.Fatalf("add = %d %s", rec.Code, rec.Body)
	}
	skills.SHURL = "http://127.0.0.1:1/api/search"
	got := getJSON[gen.SkillCatalog](t, h, "/api/v1/skills/catalog?q=hello", owner).Skills
	if len(got) != 1 || !got[0].Installed || got[0].Source != "acme/kit/skills/hello" {
		t.Fatalf("installed = %+v", got)
	}
}

func TestListBrainsFindsOllama(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"models":[{"name":"llama3.2:3b"},{"name":"nomic-embed-text"},{"name":"qwen3:8b"}]}`)
	}))
	defer ollama.Close()
	h, owner := newCatalogHarness(t, func(h *harness, o *Options) { o.Probe.OllamaURL = ollama.URL })
	b := getJSON[gen.BrainList](t, h, "/api/v1/brains", owner)
	if b.Chosen || len(b.Work) < 2 || b.Work[0].Model != "qwen3:8b" || b.Work[1].Model != "llama3.2:3b" ||
		*b.Work[0].BaseUrl != ollama.URL+"/v1" || !b.Work[0].Available || b.Work[0].NeedsKey {
		t.Fatalf("brains = %+v", b)
	}
	if slices.ContainsFunc(b.Quick, func(x gen.Brain) bool { return x.Model == "nomic-embed-text" }) {
		t.Fatal("a model without tools was offered")
	}
}

// oauthMCP is an MCP server behind OAuth: protected-resource and
// authorization-server metadata, client registration, an authorize step
// that redirects with a code, and a token endpoint whose first token has
// already expired, so connecting needs a refresh.
func oauthMCP(t *testing.T) *httptest.Server {
	ms := mcp.NewServer(&mcp.Implementation{Name: "tracker"}, nil)
	mcp.AddTool(ms, &mcp.Tool{Name: "find"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})
	mh := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, nil)
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+srv.URL+`/.well-known/oauth-protected-resource/mcp"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		mh.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"resource":%q,"authorization_servers":[%q]}`, srv.URL+"/mcp", srv.URL)
	})
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"issuer":%[1]q,"authorization_endpoint":"%[1]s/authorize","token_endpoint":"%[1]s/token",
			"registration_endpoint":"%[1]s/register","response_types_supported":["code"],"code_challenge_methods_supported":["S256"]}`, srv.URL)
	})
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, r *http.Request) {
		var m struct {
			RedirectURIs []string `json:"redirect_uris"`
		}
		json.NewDecoder(r.Body).Decode(&m)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"client_id": "c1", "client_secret": "s1", "redirect_uris": m.RedirectURIs, "token_endpoint_auth_method": "client_secret_post"})
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != "c1" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "bad authorize", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=k1&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Form.Get("client_secret") != "s1":
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
		case r.Form.Get("grant_type") == "authorization_code" && r.Form.Get("code") == "k1" && r.Form.Get("code_verifier") != "":
			fmt.Fprint(w, `{"access_token":"stale","refresh_token":"r1","token_type":"Bearer","expires_in":1}`)
		case r.Form.Get("grant_type") == "refresh_token" && r.Form.Get("refresh_token") == "r1":
			fmt.Fprint(w, `{"access_token":"fresh","refresh_token":"r2","token_type":"Bearer","expires_in":3600}`)
		default:
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		}
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestConnectingAnOAuthMCPAppSignsInAndConnects(t *testing.T) {
	remote := oauthMCP(t)
	h, owner := newCatalogHarness(t, func(h *harness, o *Options) {
		o.MCP = &gateway.MCP{Store: h.store, Vault: h.vault}
		o.MCPRedirect = "http://127.0.0.1:1/oauth/mcp/callback"
	})
	addr := remote.URL + "/mcp"
	rec := h.postJSON("/api/v1/apps/tracker/connect", owner, gen.ConnectRequest{Url: &addr})
	var res gen.ConnectResult
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || res.OpenUrl == nil || res.NeedsToken != nil {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body)
	}

	// The browser signs in and ends on the loopback address; here the
	// owner pastes it.
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(*res.OpenUrl)
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize = %v %v", resp, err)
	}
	back := resp.Header.Get("Location")
	if !strings.HasPrefix(back, "http://127.0.0.1:1/oauth/mcp/callback?") {
		t.Fatalf("redirect = %s", back)
	}
	rec = h.postJSON("/api/v1/apps/tracker/connect/finish", owner, gen.GoogleConnectFinish{RedirectUrl: back})
	res = gen.ConnectResult{}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || res.App.Status != gen.AppStatusOn || res.App.NewTools != 0 {
		t.Fatalf("finish = %d %s", rec.Code, rec.Body)
	}
	raw, _ := h.vault.Get(gateway.SecretsKey("tracker"))
	var sec gateway.Secrets
	json.Unmarshal([]byte(raw), &sec)
	if o := sec.OAuth; o == nil || o.ClientID != "c1" || o.ClientSecret != "s1" || o.Token.AccessToken != "fresh" || o.Token.RefreshToken != "r2" {
		t.Fatalf("stored = %s", raw)
	}
	if rec := h.postJSON("/api/v1/apps/tracker/connect/finish", owner, gen.GoogleConnectFinish{RedirectUrl: back}); rec.Code != http.StatusBadRequest {
		t.Fatalf("reused code = %d %s", rec.Code, rec.Body)
	}
}

func TestConnectingAnMCPAppWithoutOAuthMetadataAsksForAToken(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer remote.Close()
	h, owner := newCatalogHarness(t, func(h *harness, o *Options) {
		o.MCP = &gateway.MCP{Store: h.store, Vault: h.vault}
		o.MCPRedirect = "http://127.0.0.1:1/oauth/mcp/callback"
	})
	addr := remote.URL + "/mcp"
	rec := h.postJSON("/api/v1/apps/tracker/connect", owner, gen.ConnectRequest{Url: &addr})
	var res gen.ConnectResult
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || res.NeedsToken == nil || !*res.NeedsToken || res.OpenUrl != nil {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body)
	}
}
