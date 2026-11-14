package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/store"
)

func TestTheOwnerSeesAndCorrectsAnAgentsBrain(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()
	sam := h.pair(h.inviteDelegate(owner, "sam"), "sam's phone")
	st := h.store
	now := time.Now().UTC()
	var fact store.BrainFact
	var declined, samDeclined int64
	err := st.Tx(func(tx *store.Tx) error {
		var err error
		fact, err = tx.AddFact(store.BrainFact{AgentID: core.GeneralAgent, Person: "owner", Kind: store.KindFact, Text: "Lena signs with DocuSign", Source: store.FromOutside, CreatedAt: now})
		if err != nil {
			return err
		}
		if _, _, err := tx.SaveFollowUp(store.BrainFollowUp{ID: "fu_1", AgentID: core.GeneralAgent, Person: "owner", Kind: store.FollowUpReply,
			What: "Check whether Lena replied", DueAt: now.Add(72 * time.Hour), State: store.FollowUpOpen, CreatedAt: now}); err != nil {
			return err
		}
		for _, in := range []store.Incident{
			{AgentID: core.GeneralAgent, Person: "owner", Cause: store.CauseDeclined, Key: "send_email", TicketID: "tk_1", Detail: "x", Note: "too formal", At: now},
			{AgentID: core.GeneralAgent, Person: "delegate-sam", Cause: store.CauseDeclined, Key: "send_email", TicketID: "tk_2", Detail: "private", Note: "sam's words", At: now},
		} {
			if err := tx.AddIncident(in); err != nil {
				return err
			}
		}
		ins, _ := tx.Incidents(core.GeneralAgent, "", time.Time{}, 10)
		for _, in := range ins {
			if in.Person == "owner" {
				declined = in.ID
			} else {
				samDeclined = in.ID
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/agents/" + core.GeneralAgent + "/brain"
	if c := h.do("GET", path, sam, nil, "").Code; c != http.StatusForbidden {
		t.Fatalf("delegate = %d", c)
	}
	var b gen.AgentBrain
	rec := h.do("GET", path, owner, nil, "")
	json.Unmarshal(rec.Body.Bytes(), &b)
	if rec.Code != 200 || len(b.Facts) != 1 || b.Facts[0].Source != "outside" || len(b.WaitingOn) != 1 || len(b.WentWrong) != 2 {
		t.Fatalf("brain = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "sam's words") || strings.Contains(rec.Body.String(), "private") {
		t.Fatalf("a delegate's words showed: %s", rec.Body)
	}
	// Snooze, then cancel, the follow-up.
	until := now.Add(240 * time.Hour)
	if c := h.postJSON("/api/v1/follow-ups/fu_1", owner, gen.FollowUpUpdate{Action: "snooze", Until: &until}).Code; c != 200 {
		t.Fatalf("snooze = %d", c)
	}
	if c := h.postJSON("/api/v1/follow-ups/fu_1", owner, gen.FollowUpUpdate{Action: "cancel"}).Code; c != 200 {
		t.Fatalf("cancel = %d", c)
	}
	// Keep the owner's reason as a how-to; a delegate's can't be kept.
	if c := h.postJSON(fmt.Sprintf("/api/v1/incidents/%d", samDeclined), owner, gen.IncidentUpdate{Action: "keep"}).Code; c != 400 {
		t.Fatalf("keep a delegate's = %d", c)
	}
	if c := h.postJSON(fmt.Sprintf("/api/v1/incidents/%d", declined), owner, gen.IncidentUpdate{Action: "keep"}).Code; c != 204 {
		t.Fatalf("keep = %d", c)
	}
	if c := h.do("DELETE", fmt.Sprintf("%s/facts/%d", path, fact.ID), owner, nil, "").Code; c != 204 {
		t.Fatalf("forget = %d", c)
	}
	b = gen.AgentBrain{}
	json.Unmarshal(h.do("GET", path, owner, nil, "").Body.Bytes(), &b)
	var howTo, putAway int
	for _, f := range b.Facts {
		if f.Kind == "howto" && f.Text == "When drafting an email: too formal" {
			howTo++
		}
		if f.Id == fact.ID && f.PutAwayAt != nil {
			putAway++
		}
	}
	if len(b.WaitingOn) != 0 || len(b.WentWrong) != 1 || howTo != 1 || putAway != 1 {
		t.Fatalf("after = %+v", b)
	}
	// Forgetting can be undone.
	if c := h.do("POST", fmt.Sprintf("%s/facts/%d/restore", path, fact.ID), owner, nil, "").Code; c != 204 {
		t.Fatalf("restore = %d", c)
	}
	_ = st.Tx(func(tx *store.Tx) error {
		f, _ := tx.Fact(core.GeneralAgent, "owner", fact.ID)
		if f.OldAt != nil {
			t.Error("not restored")
		}
		return nil
	})
}
