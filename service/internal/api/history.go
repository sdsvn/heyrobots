package api

import (
	"context"
	"encoding/json"
	"net/http"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/store"
)

// HistoryClearer clears the history: it stops running work, keeps a
// backup, clears the database and removes the tasks' files.
type HistoryClearer func(ctx context.Context, forgetBrain bool) (store.Cleared, string, error)

// ClearHistory removes every chat and task so the owner starts fresh.
// Owner devices only.
func (s *Server) ClearHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.ClearHistoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "say whether to forget what agents learned")
		return
	}
	if s.opts.ClearHistory == nil {
		writeError(w, http.StatusNotFound, "clearing the history isn't available on this server")
		return
	}
	c, backup, err := s.opts.ClearHistory(r.Context(), req.ForgetBrain)
	if err != nil {
		s.opts.Log.Error("clear history", "err", err)
		writeError(w, http.StatusInternalServerError, "the history couldn't be cleared: "+err.Error())
		return
	}
	s.opts.Log.Info("history cleared", "tasks", c.Tasks, "chats", c.Chats, "actions", c.Actions, "forgot_brain", c.ForgotBrain, "backup", backup)
	writeJSON(w, http.StatusOK, gen.ClearedHistory{Tasks: c.Tasks, Chats: c.Chats, Actions: c.Actions, ForgotBrain: c.ForgotBrain, Backup: backup})
}
