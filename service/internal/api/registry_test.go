package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/gateway"
	"heyrobots/service/internal/registry"
	"heyrobots/service/internal/store"
)

const registryAnswer = `{"servers":[
 {"server":{"name":"app.linear/linear","description":"Linear","version":"1.0.2","remotes":[{"type":"streamable-http","url":"https://mcp.linear.app/mcp"}]}},
 {"server":{"name":"io.github.acme/notes-mcp","title":"Acme Notes","description":"Your notes, on this computer","version":"2.0.0","packages":[{"registryType":"npm","identifier":"@acme/notes-mcp","version":"2.0.0","transport":{"type":"stdio"},"environmentVariables":[{"name":"NOTES_TOKEN","description":"Your notes token","isSecret":true,"isRequired":true}]}]}}
]}`

// Searching Add-ons also finds servers in the MCP registry; one that runs
// here asks for its settings, then starts with them kept in the vault.
func TestRegistryServersCanBeFoundAndConnected(t *testing.T) {
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(registryAnswer)) }))
	defer reg.Close()
	registry.URL = reg.URL
	h, owner := newCatalogHarness(t, func(h *harness, o *Options) {
		o.Registry = &registry.Client{}
		o.Probe.LookPath = func(p string) (string, error) {
			if p == "npx" {
				return "/usr/bin/npx", nil
			}
			return "", exec.ErrNotFound
		}
	})

	apps := getJSON[gen.AppList](t, h, "/api/v1/apps?q=notes", owner).Apps
	var found gen.App
	for _, a := range apps {
		if strings.HasPrefix(a.Id, "registry~") {
			if a.Id == "registry~app.linear~linear" {
				t.Fatal("Linear is in the catalogue already; the registry's copy should be left out")
			}
			found = a
		}
	}
	if found.Id != "registry~io.github.acme~notes-mcp" || found.Name != "Acme Notes" || found.RunsHere == nil || !*found.RunsHere ||
		*found.Command != "npx -y @acme/notes-mcp@2.0.0" || *found.Publisher != "io.github.acme" {
		t.Fatalf("found = %+v", found)
	}

	var res gen.ConnectResult
	rec := h.postJSON("/api/v1/apps/"+found.Id+"/connect", owner, gen.ConnectRequest{})
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != 200 || res.NeedsSettings == nil || len(*res.NeedsSettings) != 1 || (*res.NeedsSettings)[0].Name != "NOTES_TOKEN" || !(*res.NeedsSettings)[0].Secret {
		t.Fatalf("first connect = %d %s", rec.Code, rec.Body)
	}
	settings := map[string]string{"NOTES_TOKEN": "tok-123"}
	rec = h.postJSON("/api/v1/apps/"+found.Id+"/connect", owner, gen.ConnectRequest{Settings: &settings})
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "tok-123") {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body)
	}
	srv, err := func() (s store.MCPServer, err error) {
		err = h.store.Tx(func(tx *store.Tx) error { s, err = tx.MCPServer("notes_mcp"); return err })
		return
	}()
	if err != nil || srv.Transport != store.MCPStdio || srv.Command != `["npx","-y","@acme/notes-mcp@2.0.0"]` {
		t.Fatalf("server = %+v %v", srv, err)
	}
	if raw, _ := h.vault.Get(gateway.SecretsKey("notes_mcp")); !strings.Contains(raw, "tok-123") {
		t.Fatalf("settings in the vault = %q", raw)
	}
}
