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

// Google connects from Add-ons: the sign-in ends at the server's own
// callback, which links the owner's addresses and wakes the Sources.
func TestConnectingGoogleFromAddOns(t *testing.T) {
	h, f, kicks := newGoogleHarness(t)
	owner := h.completeSetup()
	sam := h.pair(h.inviteDelegate(owner, "sam"), "sam's phone")

	var res gen.ConnectResult
	rec := h.postJSON("/api/v1/apps/gmail/connect", owner, gen.ConnectRequest{})
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != 200 || res.NeedsClient == nil || !*res.NeedsClient {
		t.Fatalf("connected without a client: %d %s", rec.Code, rec.Body)
	}
	if rec := h.putJSON("/api/v1/accounts/google/client", sam, gen.GoogleClient{ClientId: "9.apps.googleusercontent.com", ClientSecret: "sec"}); rec.Code != http.StatusForbidden {
		t.Fatalf("delegate set the client: %d", rec.Code)
	}
	h.putJSON("/api/v1/accounts/google/client", owner, gen.GoogleClient{ClientId: "9.apps.googleusercontent.com", ClientSecret: "sec"})
	res = gen.ConnectResult{}
	rec = h.postJSON("/api/v1/apps/gmail/connect", owner, gen.ConnectRequest{})
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != 200 || res.OpenUrl == nil || !strings.Contains(*res.OpenUrl, "gmail.send") {
		t.Fatalf("connect: %d %s", rec.Code, rec.Body)
	}
	landed, _ := url.Parse(f.Authorize(*res.OpenUrl))
	if rec := h.do("GET", landed.RequestURI(), "", nil, ""); rec.Code != 200 {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body)
	}
	if *kicks != 1 {
		t.Fatal("the mail and calendar Sources weren't woken")
	}
	p, _ := h.store.Policy()
	if !slices.Contains(p.Owner, core.Identity{Source: core.SourceMail, ID: "ann@gmail.com"}) {
		t.Fatalf("owner identities %v", p.Owner)
	}
	if rec := h.do("DELETE", "/api/v1/apps/gmail", owner, nil, ""); rec.Code/100 != 2 {
		t.Fatalf("disconnect: %d %s", rec.Code, rec.Body)
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
	var res gen.ConnectResult
	rec := h.postJSON("/api/v1/apps/gmail/connect", owner, gen.ConnectRequest{})
	json.Unmarshal(rec.Body.Bytes(), &res)
	landed, _ := url.Parse(f.Authorize(*res.OpenUrl))

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
