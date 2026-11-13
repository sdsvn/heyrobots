package executor

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"heyrobots/service/internal/catalog"
	"heyrobots/service/internal/changes"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/skills"
	"heyrobots/service/internal/store"
)

type findArgs struct {
	Query string `json:"query" jsonschema:"what to look for, such as linear or pdf"`
}

type connectArgs struct {
	AddOn string `json:"add_on" jsonschema:"the add-on's id, from find_add_ons"`
}

type accessArgs struct {
	AddOn  string `json:"add_on" jsonschema:"a connected add-on's id"`
	Access string `json:"access" jsonschema:"reads_on_own (read-only tools run by themselves, others ask), everything_asks, or off"`
}

type addSkillArgs struct {
	Source string `json:"source" jsonschema:"the skill's source from find_skills, owner/repo/path on GitHub"`
}

type giveArgs struct {
	Agent string `json:"agent" jsonschema:"the agent's name"`
	AddOn string `json:"add_on,omitempty" jsonschema:"a connected add-on's id"`
	Skill string `json:"skill,omitempty" jsonschema:"an installed skill's name"`
}

// AddOnView is one add-on as the setup tools show it.
type AddOnView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	About       string `json:"about,omitempty"`
	Connected   bool   `json:"connected"`
	Publisher   string `json:"publisher,omitempty"`
	Official    bool   `json:"official,omitempty"`
	RunsHere    bool   `json:"runs_on_this_computer,omitempty"`
	ToolsOn     int    `json:"tools_on,omitempty"`
	ToolsAsking int    `json:"tools_asking,omitempty"`
	ToolsOff    int    `json:"tools_off,omitempty"`
}

// setupTools let an agent with the setup toolset set up the team. Looking
// is free; every change waits for the owner's OK as a card, and sign-ins
// and secrets stay with the owner.
func (r *run) setupTools() ([]tool.Tool, error) {
	status, err := functiontool.New(functiontool.Config{
		Name:        "setup_status",
		Description: "See the team's setup: connected add-ons with their tools, installed skills, and which agent has which.",
	}, func(_ agent.Context, _ teamArgs) (map[string]any, error) {
		addOns, err := r.connectedAddOns()
		if err != nil {
			return nil, err
		}
		agents, err := r.e.Store.Agents()
		if err != nil {
			return nil, err
		}
		var team []map[string]any
		for _, a := range agents {
			if a.Home != "" {
				continue
			}
			team = append(team, map[string]any{"name": a.Name, "purpose": a.Purpose, "any_toolset": a.AnyToolset,
				"toolsets": a.ToolsetList(), "skills": a.SkillList(), "on": a.Enabled})
		}
		var installed []string
		if r.e.Skills != nil {
			lib, _ := r.e.Skills.List(context.Background())
			for _, sk := range lib {
				installed = append(installed, sk.Name+": "+sk.Description)
			}
		}
		return map[string]any{"add_ons": addOns, "agents": team, "skills": installed,
			"note": "An add-on's toolset is mcp:<id>; give it to an agent so the agent can use it."}, nil
	})
	if err != nil {
		return nil, err
	}
	findAddOns, err := functiontool.New(functiontool.Config{
		Name:        "find_add_ons",
		Description: "Find add-ons (apps and MCP servers) by name or purpose: the starter set, what's connected, and the official MCP registry.",
	}, func(tc agent.Context, a findArgs) (map[string]any, error) {
		q := strings.ToLower(strings.TrimSpace(a.Query))
		connected, err := r.connectedAddOns()
		if err != nil {
			return nil, err
		}
		var out []AddOnView
		for _, c := range connected {
			if strings.Contains(strings.ToLower(c.Name+" "+c.ID), q) {
				out = append(out, c)
			}
		}
		for _, c := range catalog.Apps() {
			if slices.ContainsFunc(out, func(v AddOnView) bool { return v.ID == c.ID }) || !strings.Contains(strings.ToLower(c.Name+" "+c.About), q) {
				continue
			}
			out = append(out, AddOnView{ID: c.ID, Name: c.Name, About: c.About, RunsHere: len(c.Command) > 0})
		}
		res := map[string]any{"add_ons": out}
		if r.e.Registry != nil && q != "" {
			found, err := r.e.Registry.Search(tc, q)
			if err != nil {
				res["registry"] = err.Error()
			}
			for i, f := range found {
				if i == 15 {
					break
				}
				name := f.Title
				if name == "" {
					_, name, _ = strings.Cut(f.Name, "/")
				}
				out = append(out, AddOnView{ID: "registry~" + strings.ReplaceAll(f.Name, "/", "~"), Name: name, About: f.Description,
					Publisher: f.Publisher, Official: f.Official, RunsHere: f.Package != nil})
			}
			res["add_ons"] = out
		}
		return res, nil
	})
	if err != nil {
		return nil, err
	}
	findSkills, err := functiontool.New(functiontool.Config{
		Name:        "find_skills",
		Description: "Find skills: installed ones and the public directory skills.sh, most installed first.",
	}, func(tc agent.Context, a findArgs) (map[string]any, error) {
		found, err := skills.SearchSH(tc, a.Query)
		if err != nil {
			return map[string]any{"error": err.Error()}, nil
		}
		var out []map[string]any
		for i, f := range found {
			if i == 15 {
				break
			}
			out = append(out, map[string]any{"name": f.Name, "source": f.ID, "repository": f.Source, "installs": f.Installs})
		}
		return map[string]any{"skills": out}, nil
	})
	if err != nil {
		return nil, err
	}
	connect, err := functiontool.New(functiontool.Config{
		Name:        "connect_add_on",
		Description: "Propose connecting an add-on. The owner confirms it; any sign-in then opens on the server's computer, and settings or tokens are entered by the owner in Add-ons, never through you.",
	}, func(_ agent.Context, a connectArgs) (ProposeResult, error) {
		if msg := r.mayChange(); msg != "" {
			return ProposeResult{Error: msg}, nil
		}
		id := strings.TrimSpace(a.AddOn)
		name := id
		if c, ok := catalog.Find(id); ok {
			name = c.Name
		} else if !strings.HasPrefix(id, "registry~") {
			return ProposeResult{Error: "use an id from find_add_ons"}, nil
		}
		return r.propose(changes.ActionConnect, changes.Change{Kind: "setup", Title: "Connect " + name, Subtitle: "Its read-only tools work on their own; the rest ask you first", AddOn: id})
	})
	if err != nil {
		return nil, err
	}
	access, err := functiontool.New(functiontool.Config{
		Name:        "set_tool_access",
		Description: "Propose how a connected add-on's tools may run, all at once. The owner confirms it.",
	}, func(_ agent.Context, a accessArgs) (ProposeResult, error) {
		if msg := r.mayChange(); msg != "" {
			return ProposeResult{Error: msg}, nil
		}
		srv, err := r.serverOf(a.AddOn)
		if err != nil {
			return ProposeResult{Error: err.Error()}, nil
		}
		words := map[string]string{changes.AccessReads: "reads on its own, the rest ask you first", changes.AccessAsks: "every tool asks you first", changes.AccessOff: "all tools off"}[a.Access]
		if words == "" {
			return ProposeResult{Error: "access is reads_on_own, everything_asks or off"}, nil
		}
		return r.propose(changes.ActionAccess, changes.Change{Kind: "setup", Title: srv.Name + " tools", Subtitle: words, AddOn: a.AddOn, Server: srv.Name, Access: a.Access})
	})
	if err != nil {
		return nil, err
	}
	addSkill, err := functiontool.New(functiontool.Config{
		Name:        "add_skill",
		Description: "Propose adding a skill from GitHub or skills.sh. The owner confirms it; it is pinned to the version read then.",
	}, func(_ agent.Context, a addSkillArgs) (ProposeResult, error) {
		if msg := r.mayChange(); msg != "" {
			return ProposeResult{Error: msg}, nil
		}
		src := strings.TrimSpace(a.Source)
		if strings.Count(src, "/") < 2 || strings.ContainsAny(src, " \t") {
			return ProposeResult{Error: "a source is owner/repo/path, from find_skills"}, nil
		}
		return r.propose(changes.ActionSkill, changes.Change{Kind: "setup", Title: "Add the skill " + src[strings.LastIndex(src, "/")+1:], Subtitle: "From " + src, SkillSource: src})
	})
	if err != nil {
		return nil, err
	}
	give, err := functiontool.New(functiontool.Config{
		Name:        "give_to_agent",
		Description: "Propose giving an agent a connected add-on or an installed skill. The owner confirms it.",
	}, func(_ agent.Context, a giveArgs) (ProposeResult, error) {
		if msg := r.mayChange(); msg != "" {
			return ProposeResult{Error: msg}, nil
		}
		ag, err := r.agentNamed(a.Agent)
		if err != nil {
			return ProposeResult{Error: err.Error()}, nil
		}
		c := changes.Change{Kind: "setup", Agent: ag.ID}
		switch {
		case a.AddOn != "":
			srv, err := r.serverOf(a.AddOn)
			if err != nil {
				return ProposeResult{Error: err.Error()}, nil
			}
			c.Toolset, c.Title, c.Subtitle = string(core.MCPToolset(srv.Name)), "Give "+ag.Name+" "+srv.Name, ag.Name+" can use its tools"
		case a.Skill != "":
			c.Skill, c.Title, c.Subtitle = a.Skill, "Give "+ag.Name+" the skill "+a.Skill, ag.Name+" can follow it"
		default:
			return ProposeResult{Error: "name an add-on or a skill"}, nil
		}
		return r.propose(changes.ActionGive, c)
	})
	if err != nil {
		return nil, err
	}
	return []tool.Tool{status, findAddOns, findSkills, connect, access, addSkill, give}, nil
}

// connectedAddOns are the connected MCP servers with how their tools run.
func (r *run) connectedAddOns() ([]AddOnView, error) {
	var out []AddOnView
	err := r.e.Store.Tx(func(tx *store.Tx) error {
		servers, err := tx.MCPServers()
		if err != nil {
			return err
		}
		for _, srv := range servers {
			v := AddOnView{ID: srv.Name, Name: srv.Name, Connected: true, RunsHere: srv.Transport == store.MCPStdio}
			if srv.Title != "" {
				v.Name = srv.Title
			}
			ts, err := tx.MCPTools(srv.Name)
			if err != nil {
				return err
			}
			for _, t := range ts {
				switch {
				case t.Removed:
				case !t.Usable():
					v.ToolsOff++
				case t.Mode == store.MCPToolOutward:
					v.ToolsAsking++
				default:
					v.ToolsOn++
				}
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

func (r *run) serverOf(addOn string) (store.MCPServer, error) {
	var srv store.MCPServer
	err := r.e.Store.Tx(func(tx *store.Tx) error {
		var err error
		srv, err = tx.MCPServer(strings.TrimSpace(addOn))
		return err
	})
	if err != nil {
		return srv, fmt.Errorf("%s isn't a connected add-on; see setup_status", addOn)
	}
	return srv, nil
}

func (r *run) agentNamed(name string) (store.Agent, error) {
	agents, err := r.e.Store.Agents()
	if err != nil {
		return store.Agent{}, err
	}
	for _, a := range agents {
		if a.Home == "" && strings.EqualFold(a.Name, strings.TrimSpace(name)) {
			return a, nil
		}
	}
	return store.Agent{}, fmt.Errorf("no agent here is called %s", name)
}
