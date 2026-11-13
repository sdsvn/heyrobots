// Package schedule is the time Source: it sends a Trigger at the times the
// owner set, such as weekdays at 8:00.
//
// A scheduled Trigger speaks as the person who created the schedule and
// never with more authority. It doesn't address the Assistant, so the gate
// treats it as proactive work: it needs an enabled rule that covers it,
// and accept mode decides whether that work starts or is only noted. The
// gate judges it against the Policy at the time it fires, so a schedule
// made by a delegate who was removed starts nothing.
package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/jobs"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

// Days in the order schedules store them.
var Days = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// ParseDays checks a list such as "mon,tue,fri"; empty means every day.
func ParseDays(s string) (string, error) {
	if strings.TrimSpace(s) == "" {
		return "", nil
	}
	var out []string
	for _, d := range strings.Split(s, ",") {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "weekdays" {
			for _, w := range Days[1:6] {
				if !slices.Contains(out, w) {
					out = append(out, w)
				}
			}
			continue
		}
		if !slices.Contains(Days, d) {
			return "", fmt.Errorf("%q isn't a day; use sun, mon, tue, wed, thu, fri, sat or weekdays", d)
		}
		if !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return slices.Index(Days, a) - slices.Index(Days, b) })
	return strings.Join(out, ","), nil
}

// ParseTime reads "08:00" as minutes after midnight.
func ParseTime(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, errors.New("give the time as HH:MM, such as 08:00")
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Due returns the latest time at or before now when a schedule should
// have fired, and whether it hasn't fired for it yet. Only the most recent
// missed time fires, so a server that was off for a week doesn't send a
// week of briefs.
func Due(s store.Schedule, now time.Time, loc *time.Location) (time.Time, bool) {
	local := now.In(loc)
	for back := 0; back < 8; back++ {
		day := local.AddDate(0, 0, -back)
		at := time.Date(day.Year(), day.Month(), day.Day(), s.Minute/60, s.Minute%60, 0, 0, loc)
		if at.After(local) {
			continue
		}
		if s.Days != "" && !slices.Contains(strings.Split(s.Days, ","), Days[at.Weekday()]) {
			continue
		}
		created := s.CreatedAt
		if at.Before(created) || (s.LastRunAt != nil && !at.After(*s.LastRunAt)) {
			return at, false
		}
		// Too late to be useful: more than six hours past.
		if local.Sub(at) > 6*time.Hour {
			return at, false
		}
		return at, true
	}
	return time.Time{}, false
}

// Source fires schedules, and the standing jobs that have one.
type Source struct {
	Store  *store.Store
	Intake *intake.Intake
	Log    *slog.Logger
	Now    func() time.Time
	OnNew  func()
	// Jobs is the owner's standing jobs; nil means none.
	Jobs *jobs.Library
	// Tools calls a read tool on an MCP server, for jobs with a trigger;
	// nil means triggers never poll.
	Tools interface {
		Call(ctx context.Context, server, tool string, args map[string]any) (string, error)
	}
}

// Run checks every 30 seconds until ctx ends.
func (s *Source) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		if err := s.Fire(); err != nil {
			s.Log.Warn("schedules", "err", err)
		}
		if err := s.Poll(ctx); err != nil {
			s.Log.Warn("job triggers", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Fire sends a Trigger for each standing job that is due.
func (s *Source) Fire() error {
	now := s.Now()
	p, err := s.Store.Policy()
	if err != nil {
		return err
	}
	loc := p.Limits.Location()
	fired, err := s.fireJobs(now, loc, p)
	if fired > 0 && s.OnNew != nil {
		s.OnNew()
	}
	return err
}

// fireJobs sends a Trigger for each job whose schedule is due. The
// Trigger speaks as the owner and names the job, so the router accepts it
// as that job under the job's rule.
func (s *Source) fireJobs(now time.Time, loc *time.Location, p policy.Policy) (int, error) {
	if s.Jobs == nil || len(p.Owner) == 0 {
		return 0, nil
	}
	// A job's trigger speaks as the owner, so the gate treats it as the
	// owner's proactive work.
	owner := p.Owner[0]
	all, errs := s.Jobs.List()
	for _, err := range errs {
		s.Log.Warn("job file", "err", err)
	}
	fired := 0
	for _, name := range jobs.Names(all) {
		j := all[name]
		if j.Schedule.Time == "" {
			continue
		}
		minute, err := ParseTime(j.Schedule.Time)
		if err != nil {
			s.Log.Warn("job schedule", "job", name, "err", err)
			continue
		}
		days, err := ParseDays(j.Schedule.Days)
		if err != nil {
			s.Log.Warn("job schedule", "job", name, "err", err)
			continue
		}
		var last *time.Time
		var created time.Time
		if err := s.Store.Tx(func(tx *store.Tx) error {
			var err error
			last, created, err = tx.JobRun("job:"+name, now)
			return err
		}); err != nil {
			return fired, err
		}
		at, due := Due(store.Schedule{ID: "job:" + name, Days: days, Minute: minute, CreatedAt: created, LastRunAt: last, Enabled: true}, now, loc)
		if !due {
			continue
		}
		t := core.Trigger{
			DeliveryID:   "job:" + name + "@" + at.UTC().Format(time.RFC3339),
			Source:       core.SourceSchedule,
			Actor:        owner,
			Authenticity: core.Verified,
			ReceivedAll:  true,
			ThreadKeys:   []string{"job:" + name + "@" + at.UTC().Format(time.RFC3339)},
			Text:         fmt.Sprintf("job:%s\nStanding job %q, %s: %s", name, name, at.Format("Monday 15:04 MST"), j.Description),
			At:           at.UTC(),
		}
		if _, err := s.Intake.Ingest(intake.Message{Trigger: t, ThreadKeys: t.ThreadKeys, CreatedBy: owner}); err != nil {
			return fired, err
		}
		if err := s.Store.Tx(func(tx *store.Tx) error { return tx.MarkJobRun("job:"+name, at.UTC()) }); err != nil {
			return fired, err
		}
		fired++
	}
	return fired, nil
}

// Poll calls each triggered job's tool when its interval is up and fires
// the job once per new item. The first poll only learns what is there, so
// adding a job doesn't fire it for everything that already exists.
func (s *Source) Poll(ctx context.Context) error {
	if s.Jobs == nil || s.Tools == nil {
		return nil
	}
	p, err := s.Store.Policy()
	if err != nil || len(p.Owner) == 0 {
		return err
	}
	owner := p.Owner[0]
	now := s.Now()
	all, _ := s.Jobs.List()
	for _, name := range jobs.Names(all) {
		j := all[name]
		if j.Trigger.Tool == "" {
			continue
		}
		every, err := time.ParseDuration(j.Trigger.Every)
		if err != nil || every < time.Minute {
			s.Log.Warn("job trigger", "job", name, "err", "every is a duration of at least 1m")
			continue
		}
		var last *time.Time
		if err := s.Store.Tx(func(tx *store.Tx) error {
			var err error
			last, _, err = tx.JobRun("poll:"+name, now)
			return err
		}); err != nil {
			return err
		}
		if last != nil && now.Sub(*last) < every {
			continue
		}
		server, tool, _ := strings.Cut(j.Trigger.Tool, "/")
		text, err := s.Tools.Call(ctx, server, tool, j.Trigger.Args)
		if err != nil {
			s.Log.Warn("job trigger", "job", name, "err", err)
			continue
		}
		for _, item := range items(text, j.Trigger.Key) {
			key := item[j.Trigger.Key]
			var seen bool
			if err := s.Store.Tx(func(tx *store.Tx) error {
				l, _, err := tx.JobRun("seen:"+name+"#"+key, now)
				seen = l != nil
				if !seen {
					err = errors.Join(err, tx.MarkJobRun("seen:"+name+"#"+key, now))
				}
				return err
			}); err != nil {
				return err
			}
			if seen || last == nil {
				continue
			}
			id := "job:" + name + "#" + key
			t := core.Trigger{
				DeliveryID: id, Source: core.SourceSchedule, Actor: owner, Authenticity: core.Verified, ReceivedAll: true,
				ThreadKeys: []string{id},
				Text:       fmt.Sprintf("job:%s\nStanding job %q: %s\nA new item from %s (untrusted):\n%s", name, name, j.Description, j.Trigger.Tool, item["_json"]),
				At:         now.UTC(),
			}
			if _, err := s.Intake.Ingest(intake.Message{Trigger: t, ThreadKeys: t.ThreadKeys, CreatedBy: owner}); err != nil {
				return err
			}
			if s.OnNew != nil {
				s.OnNew()
			}
		}
		if err := s.Store.Tx(func(tx *store.Tx) error { return tx.MarkJobRun("poll:"+name, now) }); err != nil {
			return err
		}
	}
	return nil
}

// items finds the objects in a tool's JSON that have the key field: a top
// level array, one object, or the first array inside an object. Each item
// comes back with its key and its JSON under "_json", capped.
func items(text, key string) []map[string]string {
	var v any
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &v); err != nil {
		return nil
	}
	var list []any
	switch v := v.(type) {
	case []any:
		list = v
	case map[string]any:
		if _, ok := v[key]; ok {
			list = []any{v}
		} else {
			for _, f := range v {
				if a, ok := f.([]any); ok {
					list = a
					break
				}
			}
		}
	}
	var out []map[string]string
	for _, it := range list {
		m, ok := it.(map[string]any)
		if !ok || m[key] == nil {
			continue
		}
		b, _ := json.Marshal(m)
		if len(b) > 4000 {
			b = append(b[:4000], "…"...)
		}
		out = append(out, map[string]string{key: fmt.Sprint(m[key]), "_json": string(b)})
	}
	return out
}
