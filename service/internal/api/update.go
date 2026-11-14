package api

import (
	"errors"
	"net/http"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/update"
)

// GetUpdate says which version runs and whether a newer one is out.
func (s *Server) GetUpdate(w http.ResponseWriter, r *http.Request, params gen.GetUpdateParams) {
	u := s.opts.Update
	if u == nil {
		writeError(w, http.StatusNotFound, "updates aren't available on this server")
		return
	}
	st, err := u.Check(r.Context(), params.Check != nil && *params.Check)
	if err != nil {
		s.opts.Log.Info("update check failed", "err", err)
	}
	writeJSON(w, http.StatusOK, updateStatus(st))
}

// StartUpdate installs the latest release. Owner devices only.
func (s *Server) StartUpdate(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	u := s.opts.Update
	if u == nil {
		writeError(w, http.StatusNotFound, "updates aren't available on this server")
		return
	}
	if err := u.Start(); err != nil {
		code := http.StatusConflict
		if !errors.Is(err, update.ErrBusy) {
			code = http.StatusBadRequest
		}
		writeError(w, code, err.Error())
		return
	}
	s.opts.Log.Info("update started")
	writeJSON(w, http.StatusAccepted, updateStatus(u.Status()))
}

func updateStatus(st update.Status) gen.UpdateStatus {
	opt := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	out := gen.UpdateStatus{Version: st.Version, Latest: opt(st.Latest), Newer: st.Newer, Notes: opt(st.Notes), Url: opt(st.URL),
		ApkUrl: opt(st.APKURL), CanUpdate: st.CanUpdate, Why: opt(st.Why), State: st.State, Error: opt(st.Error), Working: &st.Working}
	if !st.CheckedAt.IsZero() {
		out.CheckedAt = &st.CheckedAt
	}
	return out
}
