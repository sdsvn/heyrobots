// Package changes performs the owner's approved changes to their own jobs,
// rules and settings. A worker only proposes them, as outbox actions that
// wait for the owner; the outbox calls this package's Sender once the owner
// approves, and a declined change is never made.
package changes

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"heyrobots/service/internal/jobs"
	"heyrobots/service/internal/outbox"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

// Source is the outbox Source of every change.
const Source = "changes"

// The actions a change can be.
const (
	ActionJob     = "make_job"
	ActionRule    = "make_rule"
	ActionSetting = "set_setting"
)

// Change is an action's payload: what to write, and how its card reads.
type Change struct {
	// Kind is job, rule or setting, as the chat card names it.
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	// JobName and JobFile are a job's name and whole file.
	JobName string `json:"job_name,omitempty"`
	JobFile string `json:"job_file,omitempty"`
	// Rule is the rule to add; for a job, the rule it runs under, added
	// when it doesn't exist yet.
	Rule    *policy.Rule `json:"rule,omitempty"`
	Setting *Setting     `json:"setting,omitempty"`
}

// Setting holds the settings to change; nil leaves one as it is.
type Setting struct {
	DigestHour       *int     `json:"digest_hour,omitempty"`
	QuietStart       *int     `json:"quiet_start,omitempty"`
	QuietEnd         *int     `json:"quiet_end,omitempty"`
	MonthlyBudgetUSD *float64 `json:"monthly_budget_usd,omitempty"`
	Timezone         *string  `json:"timezone,omitempty"`
}

// Of reads the change an Effect proposes.
func Of(e store.Effect) (Change, bool) {
	if e.Source != Source {
		return Change{}, false
	}
	var c Change
	if json.Unmarshal([]byte(e.Payload), &c) != nil || c.Kind == "" {
		return Change{}, false
	}
	return c, true
}

// AddRule adds a rule unless the same one is there. A different rule with
// the name is refused.
func AddRule(p *policy.Policy, r policy.Rule) error {
	i := slices.IndexFunc(p.Rules, func(x policy.Rule) bool { return x.Name == r.Name })
	if i < 0 {
		p.Rules = append(p.Rules, r)
		return nil
	}
	if cur := p.Rules[i]; cur.Enabled && slices.Equal(cur.Agents, r.Agents) {
		return nil
	}
	return errors.New("a different rule named " + r.Name + " already exists")
}

// Apply changes the settings.
func (s Setting) Apply(p *policy.Policy) {
	if s.DigestHour != nil {
		p.Limits.DigestHour, p.Limits.DigestHourSet = *s.DigestHour, true
	}
	if s.QuietStart != nil && s.QuietEnd != nil {
		p.Notify.QuietStart, p.Notify.QuietEnd, p.Notify.QuietSet = *s.QuietStart, *s.QuietEnd, true
	}
	if s.MonthlyBudgetUSD != nil {
		p.Limits.MonthlyBudgetUSD = *s.MonthlyBudgetUSD
	}
	if s.Timezone != nil {
		p.Limits.Timezone = *s.Timezone
	}
}

// Sender makes approved changes. Making one twice does no harm.
type Sender struct {
	Store *store.Store
	Jobs  *jobs.Library
	Now   func() time.Time
}

// Send makes the change an approved action describes.
func (s Sender) Send(_ context.Context, e store.Effect) (string, error) {
	c, ok := Of(e)
	if !ok {
		return "", outbox.Permanent(errors.New("the change is damaged"))
	}
	if c.JobFile != "" {
		if s.Jobs == nil {
			return "", outbox.Permanent(errors.New("jobs aren't set up"))
		}
		if err := s.Jobs.Save(c.JobName, []byte(c.JobFile)); err != nil {
			return "", outbox.Permanent(err)
		}
	}
	var refused error
	err := s.Store.Tx(func(tx *store.Tx) error {
		p, err := tx.Policy()
		if err != nil {
			return err
		}
		if c.Rule != nil {
			if refused = AddRule(&p, *c.Rule); refused != nil {
				return nil
			}
		}
		if c.Setting != nil {
			c.Setting.Apply(&p)
		}
		_, err = tx.SavePolicy(p, s.Now().UTC())
		return err
	})
	if refused != nil {
		return "", outbox.Permanent(refused)
	}
	return "", err
}

// Idempotent is true: a job file is replaced whole and a rule added once.
func (Sender) Idempotent() bool { return true }
