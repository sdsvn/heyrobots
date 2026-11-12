package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

func TestAttentionShowsWhatWaitsOnEachPerson(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()
	sam := h.pair(h.inviteDelegate(owner, "sam"), "sam's phone")
	approvalID, _ := h.queueAction("bob@example.com")

	now := time.Now().UTC()
	err := h.store.Tx(func(tx *store.Tx) error {
		// Sam's task asks a question; so does one of the owner's.
		for _, x := range []struct{ th, tk, by string }{{"th_sam", "tk_sam", policy.AppDelegate("sam").String()}, {"th_own", "tk_own", policy.AppOwner.String()}} {
			if err := tx.CreateThread(store.Thread{ID: x.th, Source: "app", Key: x.th, CreatedBy: x.by, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
			m := core.Mandate{Scope: "plan", Strength: core.Explicit, Role: core.RoleOwner, Requester: policy.AppOwner}
			if err := tx.CreateTicket(store.Ticket{ID: x.tk, Open: true, Stage: string(core.StageQuestion), OriginThreadID: x.th}, m, x.th, now); err != nil {
				return err
			}
		}
		if err := tx.SetTicketChoices("tk_sam", []string{"Window", "Aisle"}); err != nil {
			return err
		}
		// A send that failed.
		return tx.CreateEffect(store.Effect{ThreadID: "th_own", Source: "app", Kind: "message", Reach: "owner", Body: "secret text",
			Status: store.EffectFailed, LastError: "no connected account", CreatedAt: now})
	})
	if err != nil {
		t.Fatal(err)
	}

	list := func(token string) []gen.AttentionItem {
		t.Helper()
		rec := h.do("GET", "/api/v1/attention", token, nil, "")
		var l gen.AttentionList
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &l) != nil {
			t.Fatalf("attention: %d %s", rec.Code, rec.Body)
		}
		return l.Items
	}
	kinds := func(items []gen.AttentionItem) string {
		var ks []string
		for _, it := range items {
			ks = append(ks, string(it.Kind)+"@"+it.ThreadId)
		}
		return strings.Join(ks, " ")
	}

	got := list(owner)
	if k := kinds(got); !strings.Contains(k, "approval@th1") || !strings.Contains(k, "question@th_sam") ||
		!strings.Contains(k, "question@th_own") || !strings.Contains(k, "failure@th_own") || len(got) != 4 {
		t.Fatalf("owner sees %s", k)
	}
	var failureID string
	for _, it := range got {
		if it.Kind == gen.AttentionItemKindApproval && *it.ApprovalId != approvalID {
			t.Fatalf("approval id %s", *it.ApprovalId)
		}
		if it.Kind == gen.AttentionItemKindFailure {
			failureID = it.Id
			if !it.Dismissable || it.Text != "no connected account" {
				t.Fatalf("failure item %+v", it)
			}
		}
	}

	// A delegate sees only questions on their own conversations, with the
	// answers they can tap.
	samItems := list(sam)
	if k := kinds(samItems); k != "question@th_sam" || samItems[0].Choices == nil || strings.Join(*samItems[0].Choices, "|") != "Window|Aisle" {
		t.Fatalf("delegate sees %q %+v", k, samItems)
	}
	rec := h.do("GET", "/api/v1/threads/th_sam", sam, nil, "")
	var th gen.ThreadDetail
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &th) != nil || len(th.Tickets) != 1 || th.Tickets[0].Choices == nil {
		t.Fatalf("thread: %d %s", rec.Code, rec.Body)
	}
	if rec := h.postJSON("/api/v1/attention/"+failureID+"/dismiss", sam, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("delegate dismissed: %d", rec.Code)
	}

	// Questions and approvals can't be dismissed, only answered.
	if rec := h.postJSON("/api/v1/attention/question:tk_own:/dismiss", owner, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("dismissed a question: %d", rec.Code)
	}
	if rec := h.postJSON("/api/v1/attention/"+failureID+"/dismiss", owner, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
	}
	if rec := h.postJSON("/api/v1/attention/"+failureID+"/dismiss", owner, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("second dismiss: %d", rec.Code)
	}
	if k := kinds(list(owner)); strings.Contains(k, "failure") || len(list(owner)) != 3 {
		t.Fatalf("after dismiss %s", k)
	}
}

func TestNotificationSettingsKeepTheTokenSecret(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()
	sam := h.pair(h.inviteDelegate(owner, "sam"), "sam's phone")

	rec := h.do("GET", "/api/v1/notifications", owner, nil, "")
	var ns gen.NotifySettings
	json.Unmarshal(rec.Body.Bytes(), &ns)
	if rec.Code != 200 || ns.Enabled || ns.ServerUrl != "https://ntfy.sh" || ns.QuietStart != 22 || ns.QuietEnd != 7 || ns.PerHour != 6 {
		t.Fatalf("defaults: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("GET", "/api/v1/notifications", sam, nil, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("delegate read settings: %d", rec.Code)
	}

	tok := "tk_secret_value"
	up := gen.NotifyUpdate{Enabled: true, ServerUrl: "https://ntfy.example.com", Topic: "hr-x8f2k", QuietStart: 23, QuietEnd: 6, PerHour: 4, Token: &tok}
	rec = h.putJSON("/api/v1/notifications", owner, up)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), tok) {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	json.Unmarshal(rec.Body.Bytes(), &ns)
	if !ns.HasToken || ns.Topic != "hr-x8f2k" || ns.PerHour != 4 {
		t.Fatalf("saved %+v", ns)
	}
	if v, _ := h.vault.Get("ntfy_token"); v != tok {
		t.Fatal("token not in the vault")
	}
	p, _ := h.store.Policy()
	if b, _ := json.Marshal(p); strings.Contains(string(b), tok) {
		t.Fatal("token stored in the policy")
	}

	// Leaving the token out keeps it; an empty one removes it.
	up.Token = nil
	rec = h.putJSON("/api/v1/notifications", owner, up)
	json.Unmarshal(rec.Body.Bytes(), &ns)
	if !ns.HasToken {
		t.Fatal("token dropped")
	}
	empty := ""
	up.Token = &empty
	rec = h.putJSON("/api/v1/notifications", owner, up)
	json.Unmarshal(rec.Body.Bytes(), &ns)
	if ns.HasToken {
		t.Fatal("token kept")
	}

	for name, bad := range map[string]gen.NotifyUpdate{
		"topic":    {Enabled: true, ServerUrl: "https://ntfy.sh", Topic: "a/b", QuietStart: 22, QuietEnd: 7, PerHour: 6},
		"no topic": {Enabled: true, ServerUrl: "https://ntfy.sh", QuietStart: 22, QuietEnd: 7, PerHour: 6},
		"scheme":   {Enabled: true, ServerUrl: "ftp://ntfy.sh", Topic: "abc", QuietStart: 22, QuietEnd: 7, PerHour: 6},
		"hour":     {Enabled: true, ServerUrl: "https://ntfy.sh", Topic: "abc", QuietStart: 24, QuietEnd: 7, PerHour: 6},
		"rate":     {Enabled: true, ServerUrl: "https://ntfy.sh", Topic: "abc", QuietStart: 22, QuietEnd: 7, PerHour: 0},
	} {
		if rec := h.putJSON("/api/v1/notifications", owner, bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func (h *harness) putJSON(path, token string, v any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(v)
	return h.do("PUT", path, token, bytes.NewReader(b), "application/json")
}
