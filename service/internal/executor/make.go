package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"gopkg.in/yaml.v3"

	"heyrobots/service/internal/changes"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/jobs"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/sources/schedule"
	"heyrobots/service/internal/store"
)

// JobStepArgs is one step of make_job.
type JobStepArgs struct {
	Name        string         `json:"name" jsonschema:"a short name, unique in the job"`
	Kind        string         `json:"kind" jsonschema:"model, ask, job or tool"`
	Instruction string         `json:"instruction,omitempty" jsonschema:"for model: what to do"`
	Question    string         `json:"question,omitempty" jsonschema:"for ask: the question for the owner"`
	Job         string         `json:"job,omitempty" jsonschema:"for job: the job to run"`
	Tool        string         `json:"tool,omitempty" jsonschema:"for tool: the tool to call"`
	Args        map[string]any `json:"args,omitempty" jsonschema:"for tool: its arguments"`
}

// MakeJobArgs are make_job's arguments.
type MakeJobArgs struct {
	Name         string         `json:"name" jsonschema:"lower-case letters, digits and dashes, such as morning-brief; the same name changes the job"`
	Description  string         `json:"description" jsonschema:"one sentence on what the job is for"`
	Days         string         `json:"days,omitempty" jsonschema:"mon,tue,... or weekdays; empty means every day"`
	Time         string         `json:"time,omitempty" jsonschema:"local time HH:MM, for a scheduled job"`
	TriggerTool  string         `json:"trigger_tool,omitempty" jsonschema:"server/tool to poll, for a job that starts on a new item"`
	TriggerArgs  map[string]any `json:"trigger_args,omitempty"`
	TriggerEvery string         `json:"trigger_every,omitempty" jsonschema:"polling interval, such as 5m"`
	TriggerKey   string         `json:"trigger_key,omitempty" jsonschema:"the field that identifies an item"`
	Toolsets     []string       `json:"toolsets,omitempty" jsonschema:"toolsets the job needs, from this task's; defaults to all of this task's"`
	Steps        []JobStepArgs  `json:"steps"`
}

// MakeRuleArgs are make_rule's arguments.
type MakeRuleArgs struct {
	Name string `json:"name" jsonschema:"a short name"`
	Text string `json:"text" jsonschema:"in the owner's words, the work you may start without being asked"`
}

// SetSettingArgs are set_setting's arguments; give only those to change.
type SetSettingArgs struct {
	DigestTime       string  `json:"digest_time,omitempty" jsonschema:"HH:00, when the daily digest is put together"`
	QuietHours       string  `json:"quiet_hours,omitempty" jsonschema:"HH-HH local hours without notifications, such as 22-07"`
	MonthlyBudgetUSD float64 `json:"monthly_budget_usd,omitempty"`
	Timezone         string  `json:"timezone,omitempty" jsonschema:"IANA name such as Europe/Berlin"`
}

// ProposeResult is what the change tools answer.
type ProposeResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Note  string `json:"note,omitempty"`
}

const proposedNote = "Proposed. It shows as a card in this chat and takes effect when the owner confirms it; say so in your reply."

// mayChange says why this task can't propose changes, or "" when it can.
// Only the owner, asking directly, in a task that read nothing from
// outside, may change what the Assistant does on its own.
func (r *run) mayChange() string {
	switch {
	case r.mandate.Role != core.RoleOwner || r.mandate.Strength != core.Explicit:
		return "only the owner, asking directly, can change jobs, rules and settings"
	case r.mandate.Received.Source == core.SourceSchedule || r.mandate.Job != "":
		return "a scheduled or triggered task can't change jobs, rules or settings"
	case r.isTainted():
		return "this task read outside content (mail, the web or a connected app), so it can't change jobs, rules or settings; the owner can ask for it in a new message"
	}
	return ""
}

func (r *run) isTainted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tainted
}

func (r *run) taint() {
	r.mu.Lock()
	r.tainted = true
	r.mu.Unlock()
}

// untrusted reports whether a tool's answer holds content marked untrusted
// anywhere in it.
func untrusted(v any) bool {
	b, _ := json.Marshal(v)
	return bytes.Contains(b, []byte(`"untrusted":true`))
}

// noteTaint marks the task once any tool answers with untrusted content.
func (r *run) noteTaint(_ agent.Context, _ tool.Tool, _, res map[string]any, _ error) (map[string]any, error) {
	if untrusted(res) {
		r.taint()
	}
	return nil, nil
}

// taintFromHistory marks the task when an earlier turn read untrusted
// content, or this turn's input isn't from a person it works for.
func (r *run) taintFromHistory(ctx context.Context, sessionID string) {
	for _, t := range r.c.Inputs {
		role, _ := r.pol.RoleOf(core.Identity{Source: core.SourceKind(t.ActorSource), ID: t.ActorID})
		if role != core.RoleOwner || t.Authenticity != int(core.Verified) {
			r.taint()
		}
	}
	res, err := r.e.Sessions.Get(ctx, &session.GetRequest{AppName: appName, UserID: "assistant", SessionID: sessionID})
	if err != nil {
		return
	}
	for ev := range res.Session.Events().All() {
		if ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p.FunctionResponse != nil && untrusted(p.FunctionResponse.Response) {
				r.taint()
				return
			}
		}
	}
}

// propose files a change as an action that waits for the owner.
func (r *run) propose(action string, c changes.Change) (ProposeResult, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return ProposeResult{}, err
	}
	err = r.e.Store.Tx(func(tx *store.Tx) error {
		return tx.CreateEffect(store.Effect{
			TicketID: r.c.Ticket.ID, ThreadID: r.c.Ticket.OriginThreadID, Source: changes.Source, Kind: "action", Action: action,
			Reach: string(core.ReachOwner), Body: c.Title + ": " + c.Subtitle, Payload: string(payload),
			Status: store.EffectAwaitingApproval, Approval: store.ApprovalAwaiting, CreatedAt: r.e.Now().UTC(),
		})
	})
	if err != nil {
		return ProposeResult{}, err
	}
	return ProposeResult{OK: true, Note: proposedNote}, nil
}

func (r *run) makeTools() ([]tool.Tool, error) {
	rule, err := functiontool.New(functiontool.Config{
		Name:        "make_rule",
		Description: "Propose a rule that lets you start a kind of work without being asked, when the owner asks for one. The owner confirms it.",
	}, func(_ agent.Context, a MakeRuleArgs) (ProposeResult, error) {
		if msg := r.mayChange(); msg != "" {
			return ProposeResult{Error: msg}, nil
		}
		name, text := strings.TrimSpace(a.Name), strings.TrimSpace(a.Text)
		if name == "" || len(name) > 60 || text == "" || utf8.RuneCountInString(text) > 1000 {
			return ProposeResult{Error: "a rule needs a name of up to 60 characters and text of up to 1000"}, nil
		}
		rl := policy.Rule{Name: name, Text: text, Enabled: true, Agents: []string{r.agent.ID}}
		if err := r.ruleFits(rl); err != nil {
			return ProposeResult{Error: err.Error()}, nil
		}
		return r.propose(changes.ActionRule, changes.Change{Kind: "rule", Title: name, Subtitle: text, Rule: &rl})
	})
	if err != nil {
		return nil, err
	}
	set, err := functiontool.New(functiontool.Config{
		Name:        "set_setting",
		Description: "Propose a change to the owner's settings: digest time, quiet hours, monthly budget or time zone. Give only those to change. The owner confirms it.",
	}, func(_ agent.Context, a SetSettingArgs) (ProposeResult, error) {
		if msg := r.mayChange(); msg != "" {
			return ProposeResult{Error: msg}, nil
		}
		s, done, msg := parseSetting(a)
		if msg != "" {
			return ProposeResult{Error: msg}, nil
		}
		return r.propose(changes.ActionSetting, changes.Change{Kind: "setting", Title: "Change settings", Subtitle: strings.Join(done, ", "), Setting: &s})
	})
	if err != nil {
		return nil, err
	}
	out := []tool.Tool{rule, set}
	if r.e.Jobs == nil {
		return out, nil
	}
	job, err := functiontool.New(functiontool.Config{
		Name:        "make_job",
		Description: "Propose a standing job you own: work that starts on a schedule (days and time) or a trigger and runs its steps. The same name changes the job. The owner confirms it.",
	}, r.makeJob)
	if err != nil {
		return nil, err
	}
	return append(out, job), nil
}

func parseSetting(a SetSettingArgs) (changes.Setting, []string, string) {
	var s changes.Setting
	var done []string
	if a.DigestTime != "" {
		m, err := schedule.ParseTime(a.DigestTime)
		if err != nil || m%60 != 0 {
			return s, nil, "give the digest time on the hour, such as 08:00"
		}
		h := m / 60
		s.DigestHour = &h
		done = append(done, "digest at "+a.DigestTime)
	}
	if a.QuietHours != "" {
		from, to, ok := strings.Cut(a.QuietHours, "-")
		qs, err1 := strconv.Atoi(strings.TrimSpace(from))
		qe, err2 := strconv.Atoi(strings.TrimSpace(to))
		if !ok || err1 != nil || err2 != nil || qs < 0 || qs > 23 || qe < 0 || qe > 23 {
			return s, nil, "give quiet hours as two hours, such as 22-07"
		}
		s.QuietStart, s.QuietEnd = &qs, &qe
		done = append(done, "quiet hours "+a.QuietHours)
	}
	if a.MonthlyBudgetUSD != 0 {
		if a.MonthlyBudgetUSD < 0 || a.MonthlyBudgetUSD > 10000 {
			return s, nil, "the monthly budget is between 0 and 10000 dollars"
		}
		s.MonthlyBudgetUSD = &a.MonthlyBudgetUSD
		done = append(done, fmt.Sprintf("budget $%.2f a month", a.MonthlyBudgetUSD))
	}
	if a.Timezone != "" {
		if _, err := time.LoadLocation(a.Timezone); err != nil {
			return s, nil, "unknown time zone " + a.Timezone + "; use an IANA name such as Europe/Berlin"
		}
		s.Timezone = &a.Timezone
		done = append(done, "time zone "+a.Timezone)
	}
	if len(done) == 0 {
		return s, nil, "name a setting to change"
	}
	return s, done, ""
}

func (r *run) makeJob(_ agent.Context, a MakeJobArgs) (ProposeResult, error) {
	if msg := r.mayChange(); msg != "" {
		return ProposeResult{Error: msg}, nil
	}
	refuse := func(msg string) (ProposeResult, error) { return ProposeResult{Error: msg}, nil }
	j := jobs.Job{Name: a.Name, Description: strings.TrimSpace(a.Description), Agent: r.agent.ID, By: r.agent.ID, Rule: a.Name,
		Toolsets: r.toolsets, Body: strings.TrimSpace(a.Description),
		Trigger: jobs.Trigger{Tool: a.TriggerTool, Args: a.TriggerArgs, Every: a.TriggerEvery, Key: a.TriggerKey}}
	if a.Toolsets != nil {
		j.Toolsets = nil
		for _, t := range a.Toolsets {
			if !slices.Contains(r.toolsets, core.Toolset(t)) {
				return refuse("toolset " + t + " isn't one this task has")
			}
			j.Toolsets = append(j.Toolsets, core.Toolset(t))
		}
	}
	for _, s := range a.Steps {
		j.Steps = append(j.Steps, jobs.Step{Name: s.Name, Kind: s.Kind, Instruction: s.Instruction, Question: s.Question, Job: s.Job, Tool: s.Tool, Args: s.Args})
	}
	var when string
	switch {
	case a.Time != "":
		days, err := schedule.ParseDays(a.Days)
		if err != nil {
			return refuse(err.Error())
		}
		minute, err := schedule.ParseTime(a.Time)
		if err != nil {
			return refuse(err.Error())
		}
		j.Schedule = jobs.Schedule{Days: days, Time: a.Time}
		var next []string
		for _, at := range nextRuns(days, minute, r.pol.Limits.Location(), r.e.Now(), 2) {
			next = append(next, at.Format("Mon 2 Jan 15:04"))
		}
		when = "Next: " + strings.Join(next, ", ")
	case a.TriggerTool != "":
		when = "When " + a.TriggerTool + " has something new, checked every " + a.TriggerEvery
	default:
		return refuse("give a time, or a trigger tool, so the job knows when to start")
	}
	fm, err := yaml.Marshal(j)
	if err != nil {
		return ProposeResult{}, err
	}
	file := "---\n" + string(fm) + "---\n" + j.Body + "\n"
	if _, err := r.e.Jobs.Check(j.Name, []byte(file)); err != nil {
		return refuse(err.Error())
	}
	// The job runs under the rule named after it, added with it when there
	// is none yet.
	rl := policy.Rule{Name: j.Rule, Text: "Run the job " + j.Name + ": " + j.Description, Enabled: true, Agents: []string{r.agent.ID}}
	if err := r.ruleFits(rl); err != nil {
		return refuse(err.Error() + "; choose another name")
	}
	return r.propose(changes.ActionJob, changes.Change{Kind: "job", Title: j.Name, Subtitle: when, JobName: j.Name, JobFile: file, Rule: &rl})
}

// ruleFits checks a rule could be added to the current policy.
func (r *run) ruleFits(rl policy.Rule) error {
	p := r.pol
	p.Rules = slices.Clone(p.Rules)
	return changes.AddRule(&p, rl)
}

// nextRuns lists a schedule's next n times after now, in loc.
func nextRuns(days string, minute int, loc *time.Location, now time.Time, n int) []time.Time {
	now = now.In(loc)
	var out []time.Time
	for i := 0; len(out) < n && i < 15; i++ {
		at := time.Date(now.Year(), now.Month(), now.Day()+i, 0, minute, 0, 0, loc)
		if at.After(now) && (days == "" || slices.Contains(strings.Split(days, ","), schedule.Days[at.Weekday()])) {
			out = append(out, at)
		}
	}
	return out
}
