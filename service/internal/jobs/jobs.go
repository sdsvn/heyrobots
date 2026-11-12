// Package jobs holds the standing jobs an agent owns: work that starts on
// a schedule or a trigger and runs steps the owner can see. Jobs live in
// the jobs folder of the data folder, one file each, and run as ADK
// workflows inside an ordinary task, so they get the agent's queue, the
// Mandate's cap and the outbox like any other work.
package jobs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"heyrobots/service/internal/core"
)

// Step kinds.
const (
	// KindModel asks the agent's model to do the step, with the instruction.
	KindModel = "model"
	// KindJob runs another job as a sub-workflow.
	KindJob = "job"
	// KindAsk pauses for the owner's answer to the question, and resumes
	// with it, after a restart too.
	KindAsk = "ask"
	// KindTool calls one tool with fixed arguments and no model.
	KindTool = "tool"
)

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Job is one standing job as its file defines it.
type Job struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Agent       string         `yaml:"agent"`
	Toolsets    []core.Toolset `yaml:"toolsets"`
	// Rule is the enabled rule that lets this job's work run.
	Rule string `yaml:"rule"`
	// By is the agent that proposed the job, when an agent did.
	By       string   `yaml:"by,omitempty"`
	Schedule Schedule `yaml:"schedule,omitempty"`
	Trigger  Trigger  `yaml:"trigger,omitempty"`
	Steps    []Step   `yaml:"steps"`
	// Body is the markdown after the frontmatter: what the job is for,
	// shown to the owner and to model steps.
	Body string `yaml:"-"`
}

// Schedule starts the job at set times.
type Schedule struct {
	// Days are "mon,tue" or "weekdays"; empty means every day.
	Days string `yaml:"days,omitempty"`
	// Time is the local time of day, "08:00".
	Time string `yaml:"time,omitempty"`
}

// Trigger starts the job when a read tool on an MCP server returns a new
// item. Polling is opt-in: nothing is polled unless a job names the tool.
type Trigger struct {
	// Tool is "server/tool", a tool the owner reviewed as a read.
	Tool string `yaml:"tool,omitempty"`
	// Args are the tool's arguments.
	Args map[string]any `yaml:"args,omitempty"`
	// Every is the polling interval, such as "5m".
	Every string `yaml:"every,omitempty"`
	// Key is the field that identifies an item, so one isn't seen twice.
	Key string `yaml:"key,omitempty"`
}

// Step is one step of a job's plan.
type Step struct {
	Name string `yaml:"name"`
	Kind string `yaml:"kind"`
	// Instruction is what a model step does; Question what an ask step
	// asks; Job which job a job step runs; Tool and Args what a tool step
	// calls.
	Instruction string         `yaml:"instruction,omitempty"`
	Question    string         `yaml:"question,omitempty"`
	Job         string         `yaml:"job,omitempty"`
	Tool        string         `yaml:"tool,omitempty"`
	Args        map[string]any `yaml:"args,omitempty"`
	// Retry repeats a failed step up to this many extra times, with
	// backoff.
	Retry int `yaml:"retry,omitempty"`
	// Timeout bounds one attempt, such as "10m".
	Timeout string `yaml:"timeout,omitempty"`
}

// Library is the jobs folder.
type Library struct {
	Dir string
}

// Parse reads one job file: YAML frontmatter between "---" lines, then
// markdown.
func Parse(name string, data []byte) (Job, error) {
	rest, ok := bytes.CutPrefix(bytes.TrimLeft(data, "\xef\xbb\xbf \t\r\n"), []byte("---\n"))
	if !ok {
		return Job{}, errors.New("a job file starts with --- and YAML frontmatter")
	}
	fm, body, ok := bytes.Cut(rest, []byte("\n---"))
	if !ok {
		return Job{}, errors.New("the frontmatter isn't closed with ---")
	}
	var j Job
	if err := yaml.Unmarshal(fm, &j); err != nil {
		return Job{}, fmt.Errorf("frontmatter: %w", err)
	}
	j.Body = strings.TrimSpace(string(body))
	if j.Name == "" {
		j.Name = name
	}
	if j.Name != name {
		return Job{}, fmt.Errorf("the file is %s but the frontmatter names %s", name, j.Name)
	}
	return j, j.check()
}

// check validates one job on its own; cycles between jobs are checked by
// the library.
func (j Job) check() error {
	if !validName.MatchString(j.Name) {
		return errors.New("a job name uses lower-case letters, digits and dashes")
	}
	if j.Agent == "" {
		return errors.New("name the agent that owns the job")
	}
	if j.Rule == "" {
		return errors.New("name the rule that allows the job, so accept mode and the rules still decide")
	}
	for _, t := range j.Toolsets {
		if !t.IsToolset() {
			return fmt.Errorf("unknown toolset %q", t)
		}
	}
	if len(j.Steps) == 0 {
		return errors.New("a job needs at least one step")
	}
	if j.Trigger.Tool != "" {
		if !strings.Contains(j.Trigger.Tool, "/") {
			return errors.New("a trigger tool is written server/tool")
		}
		if j.Trigger.Every == "" {
			return errors.New("a trigger needs an interval, such as every: 5m")
		}
		if j.Trigger.Key == "" {
			return errors.New("a trigger needs the key field that identifies an item")
		}
	}
	seen := map[string]bool{}
	for i, s := range j.Steps {
		if s.Name == "" || seen[s.Name] {
			return fmt.Errorf("step %d needs a name of its own", i+1)
		}
		seen[s.Name] = true
		switch s.Kind {
		case KindModel:
			if strings.TrimSpace(s.Instruction) == "" {
				return fmt.Errorf("step %q needs an instruction", s.Name)
			}
		case KindAsk:
			if strings.TrimSpace(s.Question) == "" {
				return fmt.Errorf("step %q needs a question", s.Name)
			}
		case KindJob:
			if s.Job == "" {
				return fmt.Errorf("step %q needs the job to run", s.Name)
			}
			if s.Job == j.Name {
				return fmt.Errorf("step %q: a job can't run itself", s.Name)
			}
		case KindTool:
			if s.Tool == "" {
				return fmt.Errorf("step %q needs the tool to call", s.Name)
			}
		default:
			return fmt.Errorf("step %q: kind must be model, ask, job or tool", s.Name)
		}
		if s.Retry < 0 || s.Retry > 10 {
			return fmt.Errorf("step %q: retry is 0 to 10", s.Name)
		}
	}
	return nil
}

// List reads every job in the folder. A file that doesn't parse is
// reported with its name, and the rest still load.
func (l Library) List() (map[string]Job, []error) {
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		return nil, []error{err}
	}
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		return nil, []error{err}
	}
	out := map[string]Job{}
	var errs []error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		data, err := os.ReadFile(filepath.Join(l.Dir, e.Name()))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		j, err := Parse(name, data)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		out[name] = j
	}
	// Every job step must point at a job, and no job may reach itself.
	for name, j := range out {
		if err := checkCalls(out, j, []string{name}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			delete(out, name)
		}
	}
	return out, errs
}

func checkCalls(all map[string]Job, j Job, path []string) error {
	for _, s := range j.Steps {
		if s.Kind != KindJob {
			continue
		}
		if slices.Contains(path, s.Job) {
			return fmt.Errorf("job %s calls itself through %s", path[0], strings.Join(append(path[1:], s.Job), " → "))
		}
		child, ok := all[s.Job]
		if !ok {
			return fmt.Errorf("step %q runs job %s, which doesn't exist", s.Name, s.Job)
		}
		if err := checkCalls(all, child, append(path, s.Job)); err != nil {
			return err
		}
	}
	return nil
}

// Get returns one job, with the whole library for its sub-jobs.
func (l Library) Get(name string) (Job, map[string]Job, error) {
	all, errs := l.List()
	j, ok := all[name]
	if !ok {
		for _, e := range errs {
			if strings.HasPrefix(e.Error(), name+": ") {
				return Job{}, nil, e
			}
		}
		return Job{}, nil, os.ErrNotExist
	}
	return j, all, nil
}

// Check parses a job file and checks the library's calls with it in
// place, without writing anything.
func (l Library) Check(name string, data []byte) (Job, error) {
	j, err := Parse(name, data)
	if err != nil {
		return Job{}, err
	}
	all, _ := l.List()
	all[name] = j
	return j, checkCalls(all, j, []string{name})
}

// Save writes a job file after checking it, so a cycle is refused before
// anything changes. The file is replaced whole, through a temporary file.
func (l Library) Save(name string, data []byte) error {
	if _, err := l.Check(name, data); err != nil {
		return err
	}
	f, err := os.CreateTemp(l.Dir, "."+name+"-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(l.Dir, name+".md"))
}

// Delete removes a job file. A job another job runs can't be removed.
func (l Library) Delete(name string) error {
	if !validName.MatchString(name) {
		return os.ErrNotExist
	}
	all, _ := l.List()
	if _, ok := all[name]; !ok {
		if _, err := os.Stat(filepath.Join(l.Dir, name+".md")); err != nil {
			return os.ErrNotExist
		}
	}
	for other, j := range all {
		for _, s := range j.Steps {
			if s.Kind == KindJob && s.Job == name {
				return fmt.Errorf("job %s still runs it", other)
			}
		}
	}
	return os.Remove(filepath.Join(l.Dir, name+".md"))
}

// Names lists job names, sorted.
func Names(all map[string]Job) []string {
	out := make([]string, 0, len(all))
	for n := range all {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
