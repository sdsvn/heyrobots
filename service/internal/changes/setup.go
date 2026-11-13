package changes

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/outbox"
	"heyrobots/service/internal/store"
)

// setup makes an approved setup change.
func (s Sender) setup(ctx context.Context, e store.Effect, c Change) (string, error) {
	switch e.Action {
	case ActionConnect:
		if s.Connect == nil {
			return "", outbox.Permanent(errors.New("add-ons can't be connected here"))
		}
		signIn, todo, err := s.Connect(ctx, c.AddOn)
		if err != nil {
			return "", outbox.Permanent(err)
		}
		// What only the owner can do is said in the chat the change came from.
		switch {
		case signIn != "":
			return "", s.say(e, "To finish connecting "+c.Title+", sign in on the server's computer: "+signIn)
		case todo != "":
			return "", s.say(e, todo)
		}
		return "", nil
	case ActionSkill:
		if s.AddSkill == nil {
			return "", outbox.Permanent(errors.New("skills can't be added here"))
		}
		_, err := s.AddSkill(ctx, c.SkillSource)
		if err != nil {
			return "", outbox.Permanent(err)
		}
		return "", nil
	case ActionAccess:
		return "", s.Store.Tx(func(tx *store.Tx) error {
			ts, err := tx.MCPTools(c.Server)
			if err != nil {
				return err
			}
			for _, t := range ts {
				mode, reach := store.MCPToolBlocked, ""
				switch c.Access {
				case AccessReads:
					mode, reach = store.DefaultMode(t.ReadOnlyHint)
				case AccessAsks:
					mode, reach = store.MCPToolOutward, string(core.ReachOthers)
				}
				if t.Removed {
					continue
				}
				if err := tx.ReviewMCPTool(c.Server, t.Name, mode, reach); err != nil {
					return err
				}
			}
			return nil
		})
	case ActionGive:
		a, err := s.Store.Agent(c.Agent)
		if err != nil {
			return "", outbox.Permanent(err)
		}
		if c.Toolset != "" && !a.AnyToolset {
			ts := a.ToolsetList()
			if !slices.Contains(ts, core.Toolset(c.Toolset)) {
				b, _ := json.Marshal(append(ts, core.Toolset(c.Toolset)))
				a.Toolsets = string(b)
			}
		}
		if c.Skill != "" && !slices.Contains(a.SkillList(), c.Skill) {
			b, _ := json.Marshal(append(a.SkillList(), c.Skill))
			a.Skills = string(b)
		}
		a.UpdatedAt = s.Now().UTC()
		return "", s.Store.SaveAgent(a)
	}
	return "", outbox.Permanent(errors.New("unknown change"))
}

// say posts a message in the chat a change came from.
func (s Sender) say(e store.Effect, text string) error {
	return s.Store.Tx(func(tx *store.Tx) error {
		return tx.CreateEffect(store.Effect{TicketID: e.TicketID, ThreadID: e.ThreadID, Source: string(core.SourceApp), Kind: "message",
			Reach: string(core.ReachOwner), Body: text, Status: store.EffectPending, CreatedAt: s.Now().UTC()})
	})
}

