package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/eval"
)

func TestDirectChatsAndGroups(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()
	sam := h.pair(h.inviteDelegate(owner, "sam"), "sam's phone")
	rec := h.postJSON("/api/v1/agents", owner, gen.AgentRequest{Name: "Coder", Purpose: "Writes code.", Toolsets: []string{"research"}, Skills: []string{},
		Capacity: 1, Color: gen.AgentRequestColorSky, Badge: gen.AgentRequestBadgeSearch, Enabled: true})
	var coder gen.Agent
	json.Unmarshal(rec.Body.Bytes(), &coder)

	// Groups are the owner's, and their chat exists from the start.
	if c := h.postJSON("/api/v1/groups", sam, gen.GroupRequest{Name: "Devs", Members: []string{coder.Id}}).Code; c != http.StatusForbidden {
		t.Fatalf("delegate create group = %d", c)
	}
	if c := h.postJSON("/api/v1/groups", owner, gen.GroupRequest{Name: "Devs", Members: []string{"ag_nope"}}).Code; c != http.StatusBadRequest {
		t.Fatalf("unknown member = %d", c)
	}
	rec = h.postJSON("/api/v1/groups", owner, gen.GroupRequest{Name: "Devs", Members: []string{coder.Id, core.GeneralAgent}})
	var g gen.Group
	json.Unmarshal(rec.Body.Bytes(), &g)
	if rec.Code != http.StatusCreated || g.ThreadId == nil || len(g.Members) != 2 {
		t.Fatalf("create group = %d %s", rec.Code, rec.Body)
	}

	// Sam may use only the General agent: not the Coder, nor a group with it.
	send := func(token, id string, req gen.SendMessageRequest) (int, gen.SendMessageResponse) {
		req.ClientMessageId, req.Text = id, "hello"
		rec := h.postJSON("/api/v1/messages", token, req)
		var out gen.SendMessageResponse
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if c, _ := send(sam, "s1", gen.SendMessageRequest{AgentId: &coder.Id}); c != http.StatusForbidden {
		t.Fatalf("delegate direct chat = %d", c)
	}
	if c, _ := send(sam, "s2", gen.SendMessageRequest{GroupId: &g.Id}); c != http.StatusForbidden {
		t.Fatalf("delegate group chat = %d", c)
	}
	if c, _ := send(sam, "s3", gen.SendMessageRequest{AgentId: ptr(core.GeneralAgent)}); c != http.StatusAccepted {
		t.Fatalf("delegate chat with General = %d", c)
	}

	// The owner's direct chat is one conversation, titled with the agent.
	_, first := send(owner, "o1", gen.SendMessageRequest{AgentId: &coder.Id})
	if _, again := send(owner, "o2", gen.SendMessageRequest{AgentId: &coder.Id}); again.ThreadId != first.ThreadId {
		t.Fatal("a second message opened another chat")
	}
	h.evalAll(routeFunc(func(c eval.Candidate) ([]eval.Route, error) {
		var out []eval.Route
		for _, tr := range c.Triggers {
			out = append(out, eval.Route{Action: eval.ActionAccept, TriggerIDs: []string{tr.ID}, Scope: "say hi", Agent: tr.Agents[0], Note: "On it."})
		}
		return out, nil
	}))
	summary := func() gen.ThreadSummary {
		var list gen.ThreadList
		json.Unmarshal(h.do("GET", "/api/v1/threads", owner, nil, "").Body.Bytes(), &list)
		for _, th := range list.Threads {
			if th.Id == first.ThreadId {
				return th
			}
		}
		t.Fatalf("chat missing from %+v", list)
		return gen.ThreadSummary{}
	}
	s := summary()
	// A direct chat skips the router, so no "On it." note comes first: the
	// agent's own answer will.
	if s.Title != "Coder" || s.Agent == nil || s.Agent.Id != coder.Id || s.Last == nil || s.Last.From != "you" {
		t.Fatalf("summary = %+v last=%+v", s, s.Last)
	}
	var detail gen.ThreadDetail
	json.Unmarshal(h.do("GET", "/api/v1/threads/"+first.ThreadId, owner, nil, "").Body.Bytes(), &detail)
	if detail.Agent == nil || detail.Tickets[0].Agent.Id != coder.Id {
		t.Fatalf("detail = %+v", detail)
	}
	for _, it := range detail.Items {
		if it.Kind == gen.Reply && (it.Agent == nil || it.Agent.Id != coder.Id) {
			t.Fatalf("reply without its agent: %+v", it)
		}
	}
	if c := h.do("POST", "/api/v1/threads/"+first.ThreadId+"/read", owner, nil, "").Code; c != http.StatusNoContent {
		t.Fatalf("mark read = %d", c)
	}
	if s := summary(); s.Unread != 0 {
		t.Fatalf("unread after reading = %d", s.Unread)
	}

	// A removed group's chat stays readable but takes no new messages.
	if c := h.do("DELETE", "/api/v1/groups/"+g.Id, owner, nil, "").Code; c != http.StatusNoContent {
		t.Fatalf("delete group = %d", c)
	}
	if c := h.do("GET", "/api/v1/threads/"+*g.ThreadId, owner, nil, "").Code; c != http.StatusOK {
		t.Fatalf("removed group's chat = %d", c)
	}
	if c, _ := send(owner, "o3", gen.SendMessageRequest{GroupId: &g.Id}); c != http.StatusNotFound {
		t.Fatalf("message to a removed group = %d", c)
	}
}
