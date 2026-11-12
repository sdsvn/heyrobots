package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/gateway"
	"heyrobots/service/internal/store"
)

func (h *harness) patchJSON(path, token string, v any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(v)
	return h.do("PATCH", path, token, bytes.NewReader(b), "application/json")
}

func decodeServer(t *testing.T, body *bytes.Buffer) gen.McpServer {
	t.Helper()
	var v gen.McpServer
	if err := json.Unmarshal(body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMCPServerLifecycle(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()
	sam := h.pair(h.inviteDelegate(owner, "sam"), "sam's phone")

	req := gen.McpServerRequest{
		Name: "tracker", Transport: gen.McpServerRequestTransportStdio, Command: &[]string{"npx", "tracker-mcp"},
		Env: &map[string]string{"TRACKER_TOKEN": "s3cret"}, Purpose: ptr("Our issue tracker"),
	}
	if c := h.postJSON("/api/v1/mcp-servers", sam, req).Code; c != http.StatusForbidden {
		t.Fatalf("delegate create = %d", c)
	}
	rec := h.postJSON("/api/v1/mcp-servers", owner, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	v := decodeServer(t, rec.Body)
	if v.Toolset != "mcp:tracker" || v.Enabled || !v.HasSecrets || v.Purpose != "Our issue tracker" {
		t.Fatalf("server = %+v", v)
	}
	// Credentials go to the vault and never come back.
	if strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatal("the secret was returned")
	}
	if raw, ok := h.vault.Get(gateway.SecretsKey("tracker")); !ok || !strings.Contains(raw, "s3cret") {
		t.Fatal("the secret wasn't kept")
	}
	if c := h.postJSON("/api/v1/mcp-servers", owner, req).Code; c != http.StatusConflict {
		t.Fatalf("duplicate = %d", c)
	}

	// Tools the server offered arrive unreviewed.
	if err := h.store.Tx(func(tx *store.Tx) error {
		return tx.SyncMCPTools("tracker", []store.MCPTool{
			{Name: "search", Description: "Search issues.", Hash: "h1"},
			{Name: "create_issue", Description: "Create an issue.", Hash: "h2"},
		}, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	rec = h.do("GET", "/api/v1/mcp-servers", owner, nil, "")
	var list gen.McpServerList
	json.Unmarshal(rec.Body.Bytes(), &list)
	if list.ToReview != 2 || len(list.Servers) != 1 || list.Servers[0].Tools[0].Mode != gen.McpToolModeUnreviewed {
		t.Fatalf("list = %+v", list)
	}

	// Reviewing: outward needs a reach.
	if c := h.putJSON("/api/v1/mcp-servers/tracker/tools/create_issue", owner, gen.McpToolReview{Mode: gen.McpToolReviewModeOutward}).Code; c != http.StatusBadRequest {
		t.Fatalf("outward without reach = %d", c)
	}
	others := gen.McpToolReviewReachOthers
	rec = h.putJSON("/api/v1/mcp-servers/tracker/tools/create_issue", owner, gen.McpToolReview{Mode: gen.McpToolReviewModeOutward, Reach: &others})
	if rec.Code != http.StatusOK {
		t.Fatalf("review = %d %s", rec.Code, rec.Body)
	}
	rec = h.putJSON("/api/v1/mcp-servers/tracker/tools/search", owner, gen.McpToolReview{Mode: gen.McpToolReviewModeRead})
	if v = decodeServer(t, rec.Body); v.ToReview != 0 {
		t.Fatalf("to review = %d", v.ToReview)
	}
	if c := h.putJSON("/api/v1/mcp-servers/tracker/tools/nope", owner, gen.McpToolReview{Mode: gen.McpToolReviewModeRead}).Code; c != http.StatusNotFound {
		t.Fatalf("unknown tool = %d", c)
	}

	// Letting tasks use it adds its toolset to the policy, where the router
	// and the gate see it like any other toolset.
	rec = h.patchJSON("/api/v1/mcp-servers/tracker", owner, gen.McpServerUpdate{Enabled: ptr(true), Trusted: ptr(true)})
	if v = decodeServer(t, rec.Body); !v.Enabled || !v.Trusted {
		t.Fatalf("server = %+v", v)
	}
	pol, _ := h.store.Policy()
	if !slices.Contains(pol.Toolsets, core.MCPToolset("tracker")) {
		t.Fatalf("toolsets = %v", pol.Toolsets)
	}
	// The settings screen saves it back unchanged.
	if c := h.do("PUT", "/api/v1/policy", owner, strings.NewReader(`{"mode":"observe","toolsets":["research","mcp:tracker"],"delegates":[{"name":"sam","toolsets":["mcp:tracker"]}],"rules":[]}`), "application/json").Code; c != http.StatusOK {
		t.Fatalf("policy with an mcp toolset = %d", c)
	}

	// Removing the server forgets it, its credentials and its toolset.
	if c := h.do("DELETE", "/api/v1/mcp-servers/tracker", owner, nil, "").Code; c != http.StatusNoContent {
		t.Fatalf("delete = %d", c)
	}
	pol, _ = h.store.Policy()
	if slices.Contains(pol.Toolsets, core.MCPToolset("tracker")) || slices.Contains(pol.Delegates[0].Toolsets, core.MCPToolset("tracker")) {
		t.Fatalf("policy still names it: %+v", pol)
	}
	if _, ok := h.vault.Get(gateway.SecretsKey("tracker")); ok {
		t.Fatal("the secret is still kept")
	}
	if c := h.do("DELETE", "/api/v1/mcp-servers/tracker", owner, nil, "").Code; c != http.StatusNotFound {
		t.Fatalf("delete again = %d", c)
	}
}

func TestMCPServerRequestsAreChecked(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()
	for _, tc := range []struct {
		name string
		req  string
	}{
		{"bad name", `{"name":"Tracker","transport":"stdio","command":["x"]}`},
		{"no command", `{"name":"t","transport":"stdio"}`},
		{"plain http", `{"name":"t","transport":"http","url":"http://mcp.example.com"}`},
		{"token on stdio", `{"name":"t","transport":"stdio","command":["x"],"token":"y"}`},
		{"bad variable", `{"name":"t","transport":"stdio","command":["x"],"env":{"A B":"y"}}`},
		{"unknown transport", `{"name":"t","transport":"ws","url":"wss://x"}`},
	} {
		if rec := h.do("POST", "/api/v1/mcp-servers", owner, strings.NewReader(tc.req), "application/json"); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", tc.name, rec.Code, rec.Body)
		}
	}
	if rec := h.do("POST", "/api/v1/mcp-servers", owner, strings.NewReader(`{"name":"local","transport":"http","url":"http://127.0.0.1:9000/mcp"}`), "application/json"); rec.Code != http.StatusCreated {
		t.Errorf("local http: %d %s", rec.Code, rec.Body)
	}
}
