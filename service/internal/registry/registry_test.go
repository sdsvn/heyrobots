package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

const answer = `{"servers":[
 {"server":{"name":"ai.smithery/smithery-notion","description":"Notion via Smithery","version":"1.0.0","remotes":[{"type":"streamable-http","url":"https://server.smithery.ai/@smithery/notion/mcp"}]}},
 {"server":{"name":"com.example/notion-pkg","description":"Notion on your computer","version":"2.1.0","packages":[{"registryType":"npm","identifier":"notion-mcp","version":"2.1.0","transport":{"type":"stdio"},"environmentVariables":[{"name":"NOTION_TOKEN","description":"Your token","isSecret":true,"isRequired":true}]}]}},
 {"server":{"name":"com.notion/mcp","description":"Official Notion MCP server","version":"1.0.1","remotes":[{"type":"sse","url":"https://mcp.notion.com/sse"},{"type":"streamable-http","url":"https://mcp.notion.com/mcp"}]}},
 {"server":{"name":"com.example/templated","description":"Needs a region","version":"1","remotes":[{"type":"streamable-http","url":"https://{region}.example.com/mcp"}]}},
 {"server":{"name":"com.example/docker-only","description":"Container","version":"1","packages":[{"registryType":"oci","identifier":"ghcr.io/x/y"}]}}
]}`

func TestSearchRanksTheAppsOwnServerFirstAndKeepsWhatCanRun(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v0/servers" || r.URL.Query().Get("search") != "notion" || r.URL.Query().Get("version") != "latest" {
			t.Errorf("request %s", r.URL)
		}
		w.Write([]byte(answer))
	}))
	defer ts.Close()
	URL = ts.URL
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	c := &Client{Now: func() time.Time { return now }}

	out, err := c.Search(context.Background(), "notion")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range out {
		names = append(names, s.Name)
	}
	// Templated addresses and container-only packages can't run here.
	if !slices.Equal(names, []string{"com.notion/mcp", "ai.smithery/smithery-notion", "com.example/notion-pkg"}) {
		t.Fatalf("order = %v", names)
	}
	if !out[0].Official || out[0].Remote != "https://mcp.notion.com/mcp" || out[1].Official {
		t.Fatalf("official = %+v / %+v", out[0], out[1])
	}
	pkg := out[2].Package
	if pkg == nil || !slices.Equal(pkg.Command, []string{"npx", "-y", "notion-mcp@2.1.0"}) || len(pkg.Settings) != 1 || !pkg.Settings[0].Secret || !pkg.Settings[0].Required {
		t.Fatalf("package = %+v", pkg)
	}

	// A day's answers come from the cache.
	c.Search(context.Background(), "notion")
	now = now.Add(25 * time.Hour)
	c.Search(context.Background(), "notion")
	if calls != 2 {
		t.Fatalf("registry called %d times, want 2", calls)
	}
}
