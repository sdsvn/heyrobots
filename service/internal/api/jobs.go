package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/jobs"
	"heyrobots/service/internal/store"
)

func jobView(j jobs.Job, content string, tx *store.Tx) gen.Job {
	v := gen.Job{Name: j.Name, Description: j.Description, Agent: j.Agent, Rule: j.Rule, Toolsets: []string{}, Steps: []gen.JobStep{}, Content: content}
	for _, t := range j.Toolsets {
		v.Toolsets = append(v.Toolsets, string(t))
	}
	if j.Schedule.Time != "" {
		v.Schedule = strings.TrimSpace(j.Schedule.Days + " " + j.Schedule.Time)
	}
	if j.Trigger.Tool != "" {
		v.Trigger = j.Trigger.Tool + " every " + j.Trigger.Every
	}
	if j.By != "" {
		v.PlannedBy = &j.By
	}
	for _, s := range j.Steps {
		d := s.Instruction
		switch s.Kind {
		case jobs.KindAsk:
			d = s.Question
		case jobs.KindJob:
			d = s.Job
		case jobs.KindTool:
			d = s.Tool
		}
		v.Steps = append(v.Steps, gen.JobStep{Name: s.Name, Kind: gen.JobStepKind(s.Kind), Detail: d})
	}
	if tx != nil {
		if r, err := tx.JobRunIfAny("job:" + j.Name); err == nil && r != nil {
			v.LastRunAt = r.LastRunAt
		}
	}
	return v
}

func (s *Server) jobContent(name string) string {
	b, _ := os.ReadFile(filepath.Join(s.opts.Jobs.Dir, name+".md"))
	return string(b)
}

// ListJobs lists the standing jobs. Owner devices only.
func (s *Server) ListJobs(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	out := gen.JobList{Jobs: []gen.Job{}, Problems: []string{}}
	if s.opts.Jobs == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	all, errs := s.opts.Jobs.List()
	for _, e := range errs {
		out.Problems = append(out.Problems, e.Error())
	}
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		for _, name := range jobs.Names(all) {
			out.Jobs = append(out.Jobs, jobView(all[name], s.jobContent(name), tx))
		}
		return nil
	})
	if err != nil {
		s.internal(w, "list jobs", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// SaveJob adds or replaces a job file. Owner devices only.
func (s *Server) SaveJob(w http.ResponseWriter, r *http.Request, name string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	if s.opts.Jobs == nil {
		writeError(w, http.StatusBadRequest, "there is no jobs folder on this server")
		return
	}
	var req gen.JobRequest
	if !decode(w, r, &req) {
		return
	}
	if err := s.opts.Jobs.Save(name, []byte(req.Content)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	j, _, err := s.opts.Jobs.Get(name)
	if err != nil {
		s.internal(w, "find saved job", err)
		return
	}
	s.opts.Log.Info("job saved", "job", name)
	writeJSON(w, http.StatusOK, jobView(j, req.Content, nil))
}

// DeleteJob removes a job file. Owner devices only.
func (s *Server) DeleteJob(w http.ResponseWriter, r *http.Request, name string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	if s.opts.Jobs == nil {
		writeError(w, http.StatusNotFound, "no such job")
		return
	}
	err := s.opts.Jobs.Delete(name)
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "no such job")
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.opts.Log.Info("job removed", "job", name)
		w.WriteHeader(http.StatusNoContent)
	}
}
