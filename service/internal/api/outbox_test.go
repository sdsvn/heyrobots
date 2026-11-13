package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/usage"
)

// queueAction creates a Ticket for the owner with one email waiting for
// approval, and returns its API id and thread.
func (h *harness) queueAction(to string) (string, string) {
	h.t.Helper()
	now := time.Now().UTC()
	var id uint
	err := h.store.Tx(func(tx *store.Tx) error {
		if err := tx.CreateThread(store.Thread{ID: "th1", Source: "mail", Key: "k1", CreatedBy: "mail:ann@example.com", CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		m := core.Mandate{Scope: "answer Ann", Strength: core.Explicit, Role: core.RoleOwner, Requester: policy.AppOwner}
		if err := tx.CreateTicket(store.Ticket{ID: "tk1", Open: true, Stage: "waiting", OriginThreadID: "th1"}, m, "th1", now); err != nil {
			return err
		}
		if err := tx.CreateEffect(store.Effect{TicketID: "tk1", ThreadID: "th1", Source: "mail", Kind: "action", Action: "send_email",
			Reach: "others", Body: "Hello", Recipients: to, Details: `{"Recipients":["` + to + `"]}`,
			Findings: `[{"kind":"recipient","value":"` + to + `","why":"named only in an outside message"}]`,
			Status:   store.EffectAwaitingApproval, Approval: store.ApprovalAwaiting, CreatedAt: now}); err != nil {
			return err
		}
		v, err := tx.ThreadView("th1")
		id = v.Effects[0].ID
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return apiEffectID(id), "th1"
}

func TestOwnerApprovesAndDelegateCannot(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()
	sam := h.pair(h.inviteDelegate(owner, "sam"), "sam's phone")
	id, th := h.queueAction("bob@example.com")

	rec := h.do("GET", "/api/v1/approvals", owner, nil, "")
	var list gen.ApprovalList
	json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != 200 || len(list.Approvals) != 1 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	a := list.Approvals[0]
	if a.Id != id || *a.Scope != "answer Ann" || *a.RequestedBy != "the owner" || (*a.Recipients)[0] != "bob@example.com" || len(a.Findings) != 1 {
		t.Fatalf("approval view %+v", a)
	}
	if rec := h.do("GET", "/api/v1/approvals", sam, nil, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("delegate listed approvals: %d", rec.Code)
	}
	if rec := h.postJSON("/api/v1/approvals/"+id, sam, gen.ApprovalDecision{Decision: gen.Approve}); rec.Code != http.StatusForbidden {
		t.Fatalf("delegate approved: %d", rec.Code)
	}
	if rec := h.postJSON("/api/v1/approvals/"+id, owner, map[string]string{"decision": "maybe"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad decision: %d", rec.Code)
	}
	if rec := h.postJSON("/api/v1/approvals/ef_999", owner, gen.ApprovalDecision{Decision: gen.Approve}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: %d", rec.Code)
	}
	// An email to someone else needs the app to have checked the person.
	if rec := h.postJSON("/api/v1/approvals/"+id, owner, gen.ApprovalDecision{Decision: gen.Approve}); rec.Code != http.StatusForbidden {
		t.Fatalf("approved without biometrics: %d %s", rec.Code, rec.Body)
	}
	yes := true
	rec = h.postJSON("/api/v1/approvals/"+id, owner, gen.ApprovalDecision{Decision: gen.Approve, UserVerified: &yes})
	var got gen.Approval
	json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 200 || got.Status != store.EffectPending || *got.Approval != store.ApprovalApproved {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body)
	}
	if rec := h.postJSON("/api/v1/approvals/"+id, owner, gen.ApprovalDecision{Decision: gen.Reject}); rec.Code != http.StatusConflict {
		t.Fatalf("second decision: %d", rec.Code)
	}
	// The decision is in the conversation's history.
	rec = h.do("GET", "/api/v1/threads/"+th, owner, nil, "")
	var detail gen.ThreadDetail
	json.Unmarshal(rec.Body.Bytes(), &detail)
	var sawDecision, sawAction bool
	for _, it := range detail.Items {
		if it.Kind == gen.Message && strings.HasPrefix(*it.Text, "Approved:") && strings.Contains(*it.Text, "biometrics") && *it.From == "app:owner" {
			sawDecision = true
		}
		if it.Kind == gen.Action && *it.Action == "send_email" && *it.Approval == "approved" {
			sawAction = true
		}
	}
	if !sawDecision || !sawAction {
		t.Fatalf("thread %s", rec.Body)
	}
}

func TestLimitsAreValidatedAndKept(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()
	put := func(body string) (int, gen.PolicyView) {
		rec := h.do("PUT", "/api/v1/policy", owner, strings.NewReader(body), "application/json")
		var v gen.PolicyView
		json.Unmarshal(rec.Body.Bytes(), &v)
		return rec.Code, v
	}
	code, v := put(`{"mode":"observe","toolsets":[],"delegates":[],"rules":[],"limits":{"monthly_budget_usd":5,"digest_hour":0,"timezone":"Europe/Berlin"}}`)
	if code != 200 || *v.Limits.MonthlyBudgetUsd != 5 || *v.Limits.DigestHour != 0 || *v.Limits.Timezone != "Europe/Berlin" || *v.Limits.SendsPerHour != policy.DefaultSendsPerHour {
		t.Fatalf("limits: %d %+v", code, v.Limits)
	}
	// Leaving limits out keeps them.
	if code, v = put(`{"mode":"observe","toolsets":[],"delegates":[],"rules":[]}`); *v.Limits.MonthlyBudgetUsd != 5 {
		t.Fatalf("limits not kept: %+v", v.Limits)
	}
	for _, bad := range []string{`{"timezone":"Mars/Olympus"}`, `{"digest_hour":24}`, `{"monthly_budget_usd":0}`, `{"sends_per_hour":0}`} {
		if code, _ := put(`{"mode":"observe","toolsets":[],"delegates":[],"rules":[],"limits":` + bad + `}`); code != http.StatusBadRequest {
			t.Fatalf("%s accepted", bad)
		}
	}
}

func TestDigestsAndUsageAreOwnerOnly(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()
	h.srv.opts.Usage = &usage.Ledger{Store: h.store, Prices: usage.Prices{"m": {In: 1, Out: 1}}}
	h.store.RecordUsage(store.Usage{At: time.Now().UTC(), TicketID: "tk1", Model: "m", InputTokens: 10, CostMicros: 1_500_000})
	h.store.Tx(func(tx *store.Tx) error {
		return tx.CreateDigest(store.Digest{ID: "dg1", Body: "## a\n\nb", Count: 1, CreatedAt: time.Now().UTC()}, nil)
	})
	sam := h.pair(h.inviteDelegate(owner, "sam"), "sam's phone")
	for _, p := range []string{"/api/v1/digests", "/api/v1/usage"} {
		if rec := h.do("GET", p, sam, nil, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("delegate read %s: %d", p, rec.Code)
		}
	}
	rec := h.do("GET", "/api/v1/usage", owner, nil, "")
	var u gen.UsageView
	json.Unmarshal(rec.Body.Bytes(), &u)
	if rec.Code != 200 || u.SpentUsd != 1.5 || u.BudgetUsd != policy.DefaultMonthlyBudgetUSD || len(u.Tickets) != 1 {
		t.Fatalf("usage: %d %s", rec.Code, rec.Body)
	}
	rec = h.do("GET", "/api/v1/digests", owner, nil, "")
	var d gen.DigestList
	json.Unmarshal(rec.Body.Bytes(), &d)
	if rec.Code != 200 || len(d.Digests) != 1 || d.Digests[0].Id != "dg1" {
		t.Fatalf("digests: %d %s", rec.Code, rec.Body)
	}
}
