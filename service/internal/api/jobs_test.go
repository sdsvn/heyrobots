package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/jobs"
)

func TestJobsCanBeKeptFromTheApp(t *testing.T) {
	h := newHarnessWith(t, nil, func(h *harness, o *Options) {
		o.Jobs = &jobs.Library{Dir: t.TempDir()}
	})
	owner := h.completeSetup()
	sam := h.pair(h.inviteDelegate(owner, "sam"), "sam's phone")

	brief := "---\nname: brief\ndescription: Morning brief.\nagent: ag_general\nrule: brief\nschedule: {days: weekdays, time: \"08:00\"}\nsteps:\n  - {name: draft, kind: model, instruction: Draft it.}\n  - {name: check, kind: ask, question: \"Send it?\"}\n---\nA short brief.\n"
	rec := h.putJSON("/api/v1/jobs/brief", owner, gen.JobRequest{Content: brief})
	if rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s", rec.Code, rec.Body)
	}
	if c := h.putJSON("/api/v1/jobs/brief", sam, gen.JobRequest{Content: brief}).Code; c != http.StatusForbidden {
		t.Fatalf("delegate saved = %d", c)
	}
	if c := h.putJSON("/api/v1/jobs/loop", owner, gen.JobRequest{Content: "---\nname: loop\nagent: ag_general\nrule: r\nsteps: [{name: s, kind: job, job: loop}]\n---\n"}).Code; c != http.StatusBadRequest {
		t.Fatalf("self-running job saved = %d", c)
	}
	if c := h.putJSON("/api/v1/jobs/wrap", owner, gen.JobRequest{Content: "---\nname: wrap\nagent: ag_general\nrule: r\nsteps: [{name: s, kind: job, job: brief}]\n---\n"}).Code; c != http.StatusOK {
		t.Fatalf("wrapper = %d", c)
	}

	var list gen.JobList
	json.Unmarshal(h.do("GET", "/api/v1/jobs", owner, nil, "").Body.Bytes(), &list)
	if len(list.Jobs) != 2 || list.Jobs[0].Name != "brief" || list.Jobs[0].Schedule != "weekdays 08:00" || list.Jobs[0].Steps[1].Detail != "Send it?" || list.Jobs[0].Content != brief || list.Jobs[0].LastRunAt != nil {
		t.Fatalf("list = %+v", list)
	}

	// A job another job runs stays until that one goes.
	if c := h.do("DELETE", "/api/v1/jobs/brief", owner, nil, "").Code; c != http.StatusBadRequest {
		t.Fatalf("delete in use = %d", c)
	}
	if c := h.do("DELETE", "/api/v1/jobs/wrap", owner, nil, "").Code; c != http.StatusNoContent {
		t.Fatalf("delete = %d", c)
	}
	if c := h.do("DELETE", "/api/v1/jobs/wrap", owner, nil, "").Code; c != http.StatusNotFound {
		t.Fatalf("delete again = %d", c)
	}
}
