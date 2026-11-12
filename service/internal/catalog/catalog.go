// Package catalog is the server's list of apps the owner can connect and
// skills the owner can add. Both are files built into the server.
package catalog

import (
	_ "embed"

	"gopkg.in/yaml.v3"
)

// App kinds.
const (
	KindGoogle = "google"
	KindMCP    = "mcp"
	KindLocal  = "local"
)

// Local probes.
const (
	ProbeDarwin     = "darwin"
	ProbeClaudeCode = "claude_code"
)

// App is one app in the catalogue.
type App struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	Category string `yaml:"category"`
	Kind     string `yaml:"kind"`
	// Toolset is a Google app's toolset; an MCP app's is mcp:<id>.
	Toolset string `yaml:"toolset"`
	// Registry is the server's name in the official MCP registry.
	Registry string `yaml:"registry"`
	// URL is a remote MCP server's address.
	URL string `yaml:"url"`
	// Command starts a local MCP server.
	Command []string `yaml:"command"`
	// Probe says how a local app is found.
	Probe string `yaml:"probe"`
	// Access is a file the app must be able to read before it connects.
	Access string `yaml:"access"`
	// Steps are what the owner does before a local app can connect.
	Steps  []string `yaml:"steps"`
	Mark   string   `yaml:"mark"`
	Tint   string   `yaml:"tint"`
	About  string   `yaml:"about"`
	Coming bool     `yaml:"coming"`
}

// Skill is one skill in the curated list.
type Skill struct {
	Source       string `yaml:"source"`
	Name         string `yaml:"name"`
	Description  string `yaml:"description"`
	SuggestedFor string `yaml:"suggested_for"`
}

var (
	//go:embed apps.yaml
	appsYAML []byte
	//go:embed skills.yaml
	skillsYAML []byte

	apps   = mustParse[App](appsYAML)
	skills = mustParse[Skill](skillsYAML)
)

func mustParse[T any](b []byte) []T {
	var out []T
	if err := yaml.Unmarshal(b, &out); err != nil {
		panic("catalog: " + err.Error())
	}
	return out
}

// Apps lists the catalogue's apps in order.
func Apps() []App { return apps }

// Find returns the app with an id.
func Find(id string) (App, bool) {
	for _, a := range apps {
		if a.ID == id {
			return a, true
		}
	}
	return App{}, false
}

// Skills lists the curated skills.
func Skills() []Skill { return skills }
