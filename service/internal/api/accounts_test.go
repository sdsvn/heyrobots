package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/google"
	"heyrobots/service/internal/google/googletest"
	"heyrobots/service/internal/store"
)

const testRedirect = "http://127.0.0.1:7340/oauth/google/callback"

func newGoogleHarness(t *testing.T) (*harness, *googletest.Fake, *int) {
	f := googletest.New(t)
	kicks := 0
	h := newHarnessWith(t, nil, func(h *harness, o *Options) {
		o.Google = &google.Google{Vault: h.vault, Store: h.store, Ends: f.Endpoints(), HTTP: f.Server.Client()}
		o.GoogleRedirect = testRedirect
		o.OnAccounts = func() { kicks++ }
	})
	return h, f, &kicks
}

func TestConnectingGoogleByPastingTheAddress(t *testing.T) {
	h, f, kicks := newGoogleHarness(t)
	owner := h.completeSetup()
	sam := h.pair(h.inviteDelegate(owner, "sam"), "sam's phone")

	if rec := h.do("GET", "/api/v1/accounts", sam, nil, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("delegate listed accounts: %d", rec.Code)
	}
	if rec := h.postJSON("/api/v1/accounts/google/connect", owner, gen.GoogleConnect{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("started without a client: %d %s", rec.Code, rec.Body)
	}
	if rec := h.putJSON("/api/v1/accounts/google/client", owner, gen.GoogleClient{ClientId: "9.apps.googleusercontent.com", ClientSecret: "sec"}); rec.Code != http.StatusNoContent {
		t.Fatalf("client: %d %s", rec.Code, rec.Body)
	}

	rec := h.postJSON("/api/v1/accounts/google/connect", owner, gen.GoogleConnect{AllowSending: true})
	var start gen.GoogleConnectStart
	json.Unmarshal(rec.Body.Bytes(), &start)
	if rec.Code != 200 || start.RedirectUrl != testRedirect || !strings.Contains(start.AuthUrl, "gmail.send") {
		t.Fatalf("connect: %d %s", rec.Code, rec.Body)
	}

	// On a phone, the browser ends on the loopback address, which can't
	// load there. The owner pastes it into the app.
	landed := f.Authorize(start.AuthUrl)
	rec = h.postJSON("/api/v1/accounts/google/finish", owner, gen.GoogleConnectFinish{RedirectUrl: landed})
	var acc gen.Account
	json.Unmarshal(rec.Body.Bytes(), &acc)
	if rec.Code != 200 || acc.Email != "ann@gmail.com" || acc.AssistantAddress != "ann+assistant@gmail.com" || !acc.CanSend {
		t.Fatalf("finish: %d %s", rec.Code, rec.Body)
	}
	if *kicks != 1 {
		t.Fatal("the mail and calendar Sources weren't woken")
	}
	p, _ := h.store.Policy()
	if !slices.Contains(p.Owner, core.Identity{Source: core.SourceMail, ID: "ann@gmail.com"}) ||
		!slices.Contains(p.Owner, core.Identity{Source: core.SourceCalendar, ID: "ann@gmail.com"}) {
		t.Fatalf("owner identities %v", p.Owner)
	}

	// Pasting it again fails: the sign-in is used up.
	if rec := h.postJSON("/api/v1/accounts/google/finish", owner, gen.GoogleConnectFinish{RedirectUrl: landed}); rec.Code != http.StatusBadRequest {
		t.Fatalf("finished twice: %d", rec.Code)
	}

	rec = h.do("GET", "/api/v1/accounts", owner, nil, "")
	var list gen.AccountList
	json.Unmarshal(rec.Body.Bytes(), &list)
	if !list.GoogleClientSet || len(list.Accounts) != 1 || strings.Contains(rec.Body.String(), "sec") {
		t.Fatalf("accounts: %s", rec.Body)
	}

	if rec := h.do("DELETE", "/api/v1/accounts/google", owner, nil, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d", rec.Code)
	}
	p, _ = h.store.Policy()
	if slices.Contains(p.Owner, core.Identity{Source: core.SourceMail, ID: "ann@gmail.com"}) || len(f.Revoked) != 1 {
		t.Fatalf("after disconnect: owner %v revoked %v", p.Owner, f.Revoked)
	}
}

func TestTheCallbackFinishesOnTheServersComputer(t *testing.T) {
	h, f, _ := newGoogleHarness(t)
	owner := h.completeSetup()
	h.putJSON("/api/v1/accounts/google/client", owner, gen.GoogleClient{ClientId: "9.apps.googleusercontent.com", ClientSecret: "sec"})
	rec := h.postJSON("/api/v1/accounts/google/connect", owner, gen.GoogleConnect{})
	var start gen.GoogleConnectStart
	json.Unmarshal(rec.Body.Bytes(), &start)
	landed, _ := url.Parse(f.Authorize(start.AuthUrl))

	// A made-up state does nothing, even without a device token.
	if rec := h.do("GET", "/oauth/google/callback?state=forged&code=x", "", nil, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("forged callback: %d", rec.Code)
	}
	rec = h.do("GET", landed.RequestURI(), "", nil, "")
	// html/template writes the + as &#43;, which shows as + in a browser.
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "ann&#43;assistant@gmail.com") {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatal("callback page has no CSP")
	}
}

func TestStarterRulesAndSchedules(t *testing.T) {
	h, _, _ := newGoogleHarness(t)
	owner := h.completeSetup()

	rec := h.do("GET", "/api/v1/rule-templates", owner, nil, "")
	var tl gen.RuleTemplateList
	json.Unmarshal(rec.Body.Bytes(), &tl)
	if rec.Code != 200 || len(tl.Templates) != 4 || tl.Mode != gen.AcceptModeObserve {
		t.Fatalf("templates: %d %s", rec.Code, rec.Body)
	}
	for _, x := range tl.Templates {
		if x.Added || (x.Name == "morning-brief" && x.Available) {
			t.Fatalf("template %+v", x)
		}
	}

	rec = h.postJSON("/api/v1/rule-templates/morning-brief", owner, nil)
	var pv gen.PolicyView
	json.Unmarshal(rec.Body.Bytes(), &pv)
	if rec.Code != 200 || pv.Mode != gen.AcceptModeObserve || !slices.ContainsFunc(pv.Rules, func(r gen.RulePolicy) bool { return r.Name == "morning-brief" && r.Enabled }) {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	if rec := h.postJSON("/api/v1/rule-templates/morning-brief", owner, nil); rec.Code != http.StatusConflict {
		t.Fatalf("added twice: %d", rec.Code)
	}
	if rec := h.postJSON("/api/v1/rule-templates/nope", owner, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown template: %d", rec.Code)
	}

	rec = h.do("GET", "/api/v1/schedules", owner, nil, "")
	var sl gen.ScheduleList
	json.Unmarshal(rec.Body.Bytes(), &sl)
	if len(sl.Schedules) != 1 || sl.Schedules[0].Days != "mon,tue,wed,thu,fri" || sl.Schedules[0].Time != "08:00" {
		t.Fatalf("schedules: %s", rec.Body)
	}

	days := "sat"
	rec = h.postJSON("/api/v1/schedules", owner, gen.ScheduleRequest{Name: "plants", Note: "Remind me to water the plants", Days: &days, Time: "10:30"})
	var sc gen.Schedule
	json.Unmarshal(rec.Body.Bytes(), &sc)
	if rec.Code != http.StatusCreated || sc.Time != "10:30" {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var row store.Schedule
	h.store.Tx(func(tx *store.Tx) error {
		ss, _ := tx.Schedules()
		for _, s := range ss {
			if s.ID == sc.Id {
				row = s
			}
		}
		return nil
	})
	if row.CreatedBy != "app:owner" {
		t.Fatalf("created by %q", row.CreatedBy)
	}
	bad := "funday"
	if rec := h.postJSON("/api/v1/schedules", owner, gen.ScheduleRequest{Name: "x", Note: "y", Days: &bad, Time: "10:30"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad day: %d", rec.Code)
	}
	if rec := h.postJSON("/api/v1/schedules", owner, gen.ScheduleRequest{Name: "plants", Note: "again", Time: "11:00"}); rec.Code != http.StatusConflict {
		t.Fatalf("same name: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("DELETE", "/api/v1/schedules/"+sc.Id, owner, nil, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
}
