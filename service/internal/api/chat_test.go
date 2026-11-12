package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/eval"
)

func (h *harness) send(token, clientID, threadID, text string) (int, gen.SendMessageResponse) {
	h.t.Helper()
	req := gen.SendMessageRequest{ClientMessageId: clientID, Text: text}
	if threadID != "" {
		req.ThreadId = &threadID
	}
	rec := h.postJSON("/api/v1/messages", token, req)
	var out gen.SendMessageResponse
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

type routeFunc func(eval.Candidate) ([]eval.Route, error)

func (f routeFunc) Route(_ context.Context, c eval.Candidate) ([]eval.Route, error) { return f(c) }

func (h *harness) evalAll(r eval.Router) {
	h.t.Helper()
	e := &eval.Engine{Store: h.store, Router: r, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), OnDecided: h.srv.ThreadUpdated}
	for {
		did, err := e.RunOnce(context.Background())
		if err != nil {
			h.t.Fatal(err)
		}
		if !did {
			return
		}
	}
}

func TestChatMessageIsStoredOnceAndRouted(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()

	code, first := h.send(owner, "m-1", "", "find me a good ramen place nearby")
	if code != http.StatusAccepted || first.Duplicate {
		t.Fatalf("send: %d %+v", code, first)
	}
	// The app retries after a dropped connection: stored once.
	code, again := h.send(owner, "m-1", "", "find me a good ramen place nearby")
	if code != http.StatusAccepted || !again.Duplicate || again.TriggerId != first.TriggerId {
		t.Fatalf("retry: %d %+v", code, again)
	}

	h.evalAll(routeFunc(func(c eval.Candidate) ([]eval.Route, error) {
		return []eval.Route{{Action: eval.ActionAccept, TriggerIDs: []string{c.Triggers[0].ID}, Scope: "Find a ramen place nearby", Toolsets: []core.Toolset{core.ToolsetResearch}, Note: "Looking for ramen places now."}}, nil
	}))

	rec := h.do("GET", "/api/v1/threads/"+first.ThreadId, owner, nil, "")
	var th gen.ThreadDetail
	json.Unmarshal(rec.Body.Bytes(), &th)
	if rec.Code != http.StatusOK || len(th.Items) != 3 || len(th.Tickets) != 1 {
		t.Fatalf("thread: %d %s", rec.Code, rec.Body)
	}
	if th.Items[0].Kind != gen.Message || th.Items[1].Kind != gen.Decision || *th.Items[1].Decision != "accept" || th.Items[2].Kind != gen.Reply || *th.Items[2].Status != "pending" {
		t.Fatalf("items = %s", rec.Body)
	}
	if th.Tickets[0].Scope != "Find a ramen place nearby" || !th.Tickets[0].Open {
		t.Fatalf("ticket = %+v", th.Tickets[0])
	}

	// A follow-up in the same conversation reaches the Ticket.
	if code, next := h.send(owner, "m-2", first.ThreadId, "vegetarian please"); code != http.StatusAccepted || next.ThreadId != first.ThreadId {
		t.Fatalf("follow-up: %d %+v", code, next)
	}
}

func TestDelegatesSeeOnlyTheirOwnConversations(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()
	delegate := h.pair(h.inviteDelegate(owner, "partner"), "partner phone")

	_, ownerMsg := h.send(owner, "o-1", "", "private: plan my surprise for partner")
	_, delegateMsg := h.send(delegate, "d-1", "", "draft a shopping list")

	var list gen.ThreadList
	json.Unmarshal(h.do("GET", "/api/v1/threads", delegate, nil, "").Body.Bytes(), &list)
	if len(list.Threads) != 1 || list.Threads[0].Id != delegateMsg.ThreadId {
		t.Fatalf("delegate sees %+v", list.Threads)
	}
	if rec := h.do("GET", "/api/v1/threads/"+ownerMsg.ThreadId, delegate, nil, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delegate reading the owner's thread: %d, want 404", rec.Code)
	}
	if code, _ := h.send(delegate, "d-2", ownerMsg.ThreadId, "what surprise?"); code != http.StatusNotFound {
		t.Fatalf("delegate posting to the owner's thread: %d, want 404", code)
	}
	json.Unmarshal(h.do("GET", "/api/v1/threads", owner, nil, "").Body.Bytes(), &list)
	if len(list.Threads) != 2 {
		t.Fatalf("owner sees %d threads, want both", len(list.Threads))
	}
}

func TestPolicyIsOwnerOnlyAndValidated(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()
	delegate := h.pair(h.inviteDelegate(owner, "partner"), "partner phone")

	if rec := h.do("GET", "/api/v1/policy", delegate, nil, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("delegate get policy: %d", rec.Code)
	}
	put := func(token, body string) int {
		return h.do("PUT", "/api/v1/policy", token, strings.NewReader(body), "application/json").Code
	}
	if c := put(delegate, `{"mode":"auto","toolsets":["code"],"delegates":[],"rules":[]}`); c != http.StatusForbidden {
		t.Fatalf("delegate put policy: %d", c)
	}
	for _, bad := range []string{
		`{"mode":"yolo","toolsets":[],"delegates":[],"rules":[]}`,
		`{"mode":"auto","toolsets":["shell"],"delegates":[],"rules":[]}`,
		`{"mode":"auto","toolsets":[],"delegates":[{"name":"a","toolsets":[]},{"name":"a","toolsets":[]}],"rules":[]}`,
		`{"mode":"auto","toolsets":[],"delegates":[],"rules":[{"name":"","text":"x","enabled":true}]}`,
	} {
		if c := put(owner, bad); c != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, c)
		}
	}
	rec := h.do("PUT", "/api/v1/policy", owner, strings.NewReader(`{"mode":"auto","toolsets":["research","calendar"],"delegates":[],"rules":[{"name":"bills","text":"Track bill due dates.","enabled":true}]}`), "application/json")
	var p gen.PolicyView
	json.Unmarshal(rec.Body.Bytes(), &p)
	if rec.Code != http.StatusOK || p.Mode != gen.AcceptModeAuto || len(p.Rules) != 1 || p.Version < 3 {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if c := h.postJSON("/api/v1/pairing-codes", owner, gen.PairingCodeRequest{Role: gen.RoleDelegate, DelegateName: ptr("stranger")}).Code; c != http.StatusBadRequest {
		t.Fatalf("inviting a delegate missing from the policy: %d, want 400", c)
	}
}

func ptr[T any](v T) *T { return &v }
