package calendar

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/google"
	"heyrobots/service/internal/google/googletest"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/vault"
)

var acc = store.Account{Email: "ann@gmail.com"}

func TestInvitationsAreUnverified(t *testing.T) {
	e := google.Event{ID: "e1", Summary: "Sync", Organizer: &google.Person{Email: "Bob@Example.com"},
		Start: google.EventTime{DateTime: "2026-11-20T10:00:00Z"}, End: google.EventTime{DateTime: "2026-11-20T11:00:00Z"},
		Attendees: []google.Attendee{{Email: "ann@gmail.com", Self: true, ResponseStatus: "needsAction"}, {Email: "bob@example.com"}}}
	tr, ok := Invitation(acc, e)
	if !ok || tr.Authenticity != core.Unverified || tr.Actor.ID != "bob@example.com" || !tr.ReceivedAll || tr.Addressed {
		t.Fatalf("%+v", tr)
	}
	e.Attendees[0].ResponseStatus = "accepted"
	if _, ok := Invitation(acc, e); ok {
		t.Fatal("an answered invitation became a Trigger")
	}
	e.Organizer.Self = true
	if _, ok := Invitation(acc, e); ok {
		t.Fatal("the owner's own event became an invitation")
	}
}

func TestMeetingsThatStartSoon(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	e := google.Event{ID: "e1", Summary: "Sync", Organizer: &google.Person{Email: "ann@gmail.com", Self: true},
		Start: google.EventTime{DateTime: "2026-11-20T10:00:00Z"}, End: google.EventTime{DateTime: "2026-11-20T10:30:00Z"},
		Attendees: []google.Attendee{{Email: "ann@gmail.com", Self: true}, {Email: "bob@example.com"}}}
	tr, ok := StartsSoon(acc, e, now)
	if !ok || tr.Actor.ID != "ann@gmail.com" || tr.Authenticity != core.Verified || tr.ThreadKeys[0] == "event:e1" {
		t.Fatalf("%+v", tr)
	}
	alone := e
	alone.Attendees = alone.Attendees[:1]
	if _, ok := StartsSoon(acc, alone, now); ok {
		t.Fatal("a meeting with nobody else counted")
	}
	declined := e
	declined.Attendees = []google.Attendee{{Email: "ann@gmail.com", Self: true, ResponseStatus: "declined"}, {Email: "bob@example.com"}}
	if _, ok := StartsSoon(acc, declined, now); ok {
		t.Fatal("a declined meeting counted")
	}
	other := e
	other.Organizer = &google.Person{Email: "bob@example.com"}
	if tr, _ := StartsSoon(acc, other, now); tr.Authenticity != core.Unverified {
		t.Fatal("someone else's meeting spoke as the owner")
	}
}

func TestSenderIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, _ := store.Open(dir)
	t.Cleanup(func() { st.Close() })
	v, _ := vault.Open(dir, vault.FileKey{Path: filepath.Join(dir, "vault.key")})
	f := googletest.New(t)
	g := &google.Google{Vault: v, Store: st, Ends: f.Endpoints(), HTTP: f.Server.Client()}
	g.SetClient(google.ClientConfig{ClientID: "1.apps.googleusercontent.com", ClientSecret: "s"})
	u, state, _ := g.Start("http://127.0.0.1:1/cb", true, "")
	back, _ := url.Parse(f.Authorize(u))
	if _, err := g.Finish(context.Background(), state, back.Query().Get("code")); err != nil {
		t.Fatal(err)
	}
	s := &Sender{Google: g}
	payload, _ := json.Marshal(Event{Title: "Lunch", Start: "2026-11-20T12:00:00+01:00", End: "2026-11-20T13:00:00+01:00", Attendees: []string{"bob@example.com"}})
	e := store.Effect{ID: 42, Action: "create_event", Payload: string(payload)}
	for i := 0; i < 2; i++ {
		if _, err := s.Send(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	if f.Inserted != 1 || f.Events[EventID(42)] == nil {
		t.Fatalf("inserted %d", f.Inserted)
	}

	f.Events["inv"] = &google.Event{ID: "inv", Organizer: &google.Person{Email: "bob@example.com"},
		Attendees: []google.Attendee{{Email: "bob@example.com", Organizer: true}, {Email: "ann@gmail.com", Self: true, ResponseStatus: "needsAction"}}}
	ans, _ := json.Marshal(Invite{EventID: "inv", Response: "accepted"})
	if _, err := s.Send(context.Background(), store.Effect{Action: "answer_invite", Payload: string(ans)}); err != nil {
		t.Fatal(err)
	}
	if f.Events["inv"].Attendees[1].ResponseStatus != "accepted" || f.Events["inv"].Attendees[0].ResponseStatus != "" {
		t.Fatalf("%+v", f.Events["inv"].Attendees)
	}
}
