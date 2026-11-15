package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/store"
)

func TestOnlyTheOwnerClearsTheHistory(t *testing.T) {
	var forgot []bool
	h := newHarnessWith(t, nil, func(h *harness, o *Options) {
		o.ClearHistory = func(_ context.Context, forget bool) (store.Cleared, string, error) {
			forgot = append(forgot, forget)
			return store.Cleared{Tasks: 3, Chats: 2, Actions: 1, ForgotBrain: forget}, "/data/backups/x.db", nil
		}
	})
	owner := h.completeSetup()
	sam := h.pair(h.inviteDelegate(owner, "sam"), "sam's phone")
	if c := h.postJSON("/api/v1/history/clear", sam, gen.ClearHistoryRequest{}).Code; c != http.StatusForbidden {
		t.Fatalf("a delegate clears = %d", c)
	}
	rec := h.postJSON("/api/v1/history/clear", owner, gen.ClearHistoryRequest{ForgetBrain: true})
	var out gen.ClearedHistory
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || out.Tasks != 3 || !out.ForgotBrain || out.Backup == "" || len(forgot) != 1 || !forgot[0] {
		t.Fatalf("clear = %d %+v %v", rec.Code, out, forgot)
	}
}
