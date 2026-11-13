// Package registry finds MCP servers in the official MCP registry
// (registry.modelcontextprotocol.io): servers their publishers run, and
// packages that run on this computer.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// URL is the registry's address; tests point it at a fake.
var URL = "https://registry.modelcontextprotocol.io"

// Server is one server in the registry, as heyrobots can run it.
type Server struct {
	// Name is its registry name, such as com.notion/mcp.
	Name        string
	Title       string
	Description string
	Version     string
	// Publisher is the namespace before the slash, such as com.notion.
	Publisher string
	// Official is set for the app's own server: the publisher is what was
	// searched for, and its domain serves the server.
	Official bool
	// Remote is the address of the server its publisher runs, if any.
	Remote string
	// Package runs it on this computer, when there's no remote.
	Package *Package
}

// Package is a server that runs on this computer.
type Package struct {
	// Command starts it, such as npx -y @playwright/mcp@0.0.40.
	Command  []string
	Settings []Setting
}

// Setting is a value a package needs, passed as an environment variable.
type Setting struct {
	Name        string
	Description string
	Secret      bool
	Required    bool
}

// Client searches the registry and keeps answers for a day.
type Client struct {
	HTTP *http.Client
	Now  func() time.Time

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	at  time.Time
	out []Server
}

const keep = 24 * time.Hour

// Search finds servers by text, official ones and ones heyrobots can run
// first.
func (c *Client) Search(ctx context.Context, q string) ([]Server, error) {
	q = strings.TrimSpace(q)
	now := c.now()
	c.mu.Lock()
	if hit, ok := c.cache["q:"+q]; ok && now.Sub(hit.at) < keep {
		c.mu.Unlock()
		return hit.out, nil
	}
	c.mu.Unlock()
	out, err := c.fetch(ctx, url.Values{"search": {q}, "version": {"latest"}, "limit": {"60"}})
	if err != nil {
		return nil, err
	}
	// The app's own server: its publisher is what was searched for, and
	// the publisher's domain serves it (com.notion at mcp.notion.com).
	word := strings.ToLower(strings.Fields(q + " _")[0])
	out = slices.Clone(out)
	for i := range out {
		out[i].Official = brand(out[i].Publisher) == word && official(out[i].Publisher, out[i].Remote)
	}
	slices.SortStableFunc(out, func(a, b Server) int {
		am, bm := brand(a.Publisher) == word, brand(b.Publisher) == word
		switch {
		case am != bm:
			if am {
				return -1
			}
			return 1
		case a.Official != b.Official:
			if a.Official {
				return -1
			}
			return 1
		case (a.Remote != "") != (b.Remote != ""):
			if a.Remote != "" {
				return -1
			}
			return 1
		}
		return 0
	})
	c.put("q:"+q, now, out)
	return out, nil
}

// Get returns one server by its registry name.
func (c *Client) Get(ctx context.Context, name string) (Server, error) {
	list, err := c.Search(ctx, name)
	if err != nil {
		return Server{}, err
	}
	for _, s := range list {
		if s.Name == name {
			return s, nil
		}
	}
	return Server{}, fmt.Errorf("%s isn't in the MCP registry", name)
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) put(key string, at time.Time, out []Server) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil {
		c.cache = map[string]cached{}
	}
	c.cache[key] = cached{at, out}
}

// wire is the registry's answer.
type wire struct {
	Servers []struct {
		Server struct {
			Name        string `json:"name"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Version     string `json:"version"`
			Remotes     []struct {
				Type string `json:"type"`
				URL  string `json:"url"`
			} `json:"remotes"`
			Packages []struct {
				RegistryType string `json:"registryType"`
				Identifier   string `json:"identifier"`
				Version      string `json:"version"`
				Transport    struct {
					Type string `json:"type"`
				} `json:"transport"`
				Env []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
					IsSecret    bool   `json:"isSecret"`
					IsRequired  bool   `json:"isRequired"`
				} `json:"environmentVariables"`
			} `json:"packages"`
		} `json:"server"`
	} `json:"servers"`
}

func (c *Client) fetch(ctx context.Context, q url.Values) ([]Server, error) {
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, URL+"/v0/servers?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the MCP registry can't be reached: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the MCP registry answered %s", resp.Status)
	}
	var w wire
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&w); err != nil {
		return nil, fmt.Errorf("the MCP registry's answer couldn't be read: %w", err)
	}
	out := []Server{}
	for _, e := range w.Servers {
		s := e.Server
		v := Server{Name: s.Name, Title: s.Title, Description: s.Description, Version: s.Version}
		v.Publisher, _, _ = strings.Cut(s.Name, "/")
		for _, r := range s.Remotes {
			// Addresses with {placeholders} need values heyrobots can't guess.
			if r.Type == "streamable-http" && strings.HasPrefix(r.URL, "https://") && !strings.Contains(r.URL, "{") {
				v.Remote = r.URL
				break
			}
		}
		if v.Remote == "" {
			for _, p := range s.Packages {
				if p.Transport.Type != "" && p.Transport.Type != "stdio" {
					continue
				}
				var cmd []string
				switch p.RegistryType {
				case "npm":
					cmd = []string{"npx", "-y", p.Identifier + "@" + orLatest(p.Version)}
				case "pypi":
					cmd = []string{"uvx", p.Identifier + pyVersion(p.Version)}
				default:
					continue
				}
				pkg := &Package{Command: cmd}
				for _, e := range p.Env {
					pkg.Settings = append(pkg.Settings, Setting{Name: e.Name, Description: e.Description, Secret: e.IsSecret, Required: e.IsRequired})
				}
				v.Package = pkg
				break
			}
		}
		if v.Remote == "" && v.Package == nil {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

func orLatest(v string) string {
	if v == "" {
		return "latest"
	}
	return v
}

func pyVersion(v string) string {
	if v == "" {
		return ""
	}
	return "==" + v
}

// brand is the publisher's own name: notion in com.notion, upstash in
// io.github.upstash.
func brand(publisher string) string {
	parts := strings.Split(strings.ToLower(publisher), ".")
	if len(parts) > 2 && parts[0] == "io" && parts[1] == "github" {
		return parts[2]
	}
	if len(parts) > 1 {
		return parts[1]
	}
	return publisher
}

// official reports whether a publisher's domain, read from its reversed
// namespace (com.notion is notion.com), serves the remote address.
func official(publisher, remote string) bool {
	u, err := url.Parse(remote)
	if remote == "" || err != nil {
		return false
	}
	parts := strings.Split(publisher, ".")
	if len(parts) < 2 {
		return false
	}
	slices.Reverse(parts)
	domain := strings.Join(parts, ".")
	host := u.Hostname()
	return host == domain || strings.HasSuffix(host, "."+domain)
}
