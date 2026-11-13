package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/config"
	"heyrobots/service/internal/devices"
	"heyrobots/service/internal/guard"
	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/vault"
)

type harness struct {
	t       *testing.T
	handler http.Handler
	store   *store.Store
	srv     *Server
	vault   *vault.Vault
	mu      sync.Mutex
	shown   []string
}

func newHarness(t *testing.T, guardMW func(http.Handler) http.Handler) *harness {
	t.Helper()
	return newHarnessWith(t, guardMW, nil)
}

func newHarnessWith(t *testing.T, guardMW func(http.Handler) http.Handler, more func(h *harness, o *Options)) *harness {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	v, err := vault.Open(dir, vault.FileKey{Path: filepath.Join(dir, "vault.key")})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, vault: v, store: st}
	o := Options{
		Store: st, Devices: devices.New(st, time.Now), Vault: v, Broker: NewBroker(),
		Intake: intake.New(st, time.Now), Now: time.Now,
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Guard: guardMW, Heartbeat: 50 * time.Millisecond,
		ShowSetupCode: func(code string) {
			h.mu.Lock()
			h.shown = append(h.shown, code)
			h.mu.Unlock()
		},
	}
	if more != nil {
		more(h, &o)
	}
	srv := New(o)
	if err := srv.AnnounceSetup(); err != nil {
		t.Fatal(err)
	}
	h.handler = srv.Handler()
	h.srv = srv
	return h
}

func (h *harness) setupCode() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.shown) == 0 {
		h.t.Fatal("no setup code was shown")
	}
	return h.shown[len(h.shown)-1]
}

// do sends a request as if it came through a local tunnel: from 127.0.0.1,
// with forwarding headers pointing at the internet.
func (h *harness) do(method, path, token string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, body)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	r.Header.Set("Cf-Connecting-Ip", "203.0.113.9")
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, r)
	return rec
}

func (h *harness) postForm(path string, form url.Values) *httptest.ResponseRecorder {
	return h.do("POST", path, "", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
}

func (h *harness) postJSON(path, token string, v any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(v)
	return h.do("POST", path, token, bytes.NewReader(b), "application/json")
}

var pairingCodeRE = regexp.MustCompile(`id="pairing-code">([0-9A-Z-]+)<`)

// completeSetup runs setup and pairs the first owner device.
func (h *harness) completeSetup() (ownerToken string) {
	h.t.Helper()
	rec := h.postForm("/setup", url.Values{"setup_code": {h.setupCode()}})
	if rec.Code != http.StatusOK {
		h.t.Fatalf("setup status %d: %s", rec.Code, rec.Body)
	}
	m := pairingCodeRE.FindStringSubmatch(rec.Body.String())
	if m == nil {
		h.t.Fatal("setup page shows no pairing code")
	}
	return h.pair(m[1], "owner laptop")
}

func (h *harness) pair(code, name string) string {
	h.t.Helper()
	rec := h.postJSON("/api/v1/pair", "", gen.PairRequest{Code: code, DeviceName: name})
	if rec.Code != http.StatusOK {
		h.t.Fatalf("pair status %d: %s", rec.Code, rec.Body)
	}
	var resp gen.PairResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp.Token
}

func (h *harness) inviteDelegate(ownerToken, name string) string {
	h.t.Helper()
	rec := h.do("PUT", "/api/v1/policy", ownerToken, strings.NewReader(`{"mode":"observe","toolsets":["research","writing"],"delegates":[{"name":"`+name+`","toolsets":["writing"]}],"rules":[]}`), "application/json")
	if rec.Code != http.StatusOK {
		h.t.Fatalf("policy: %d %s", rec.Code, rec.Body)
	}
	dn := name
	rec = h.postJSON("/api/v1/pairing-codes", ownerToken, gen.PairingCodeRequest{Role: gen.Delegate, DelegateName: &dn})
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("invite status %d: %s", rec.Code, rec.Body)
	}
	var c gen.PairingCode
	json.Unmarshal(rec.Body.Bytes(), &c)
	return c.Code
}

func TestTunnelledRequestCannotUseSetupWithoutTheTerminalCode(t *testing.T) {
	h := newHarness(t, nil)

	// Seeing the form is harmless: it holds no secret.
	rec := h.do("GET", "/setup", "", nil, "")
	if rec.Code != http.StatusOK || pairingCodeRE.MatchString(rec.Body.String()) {
		t.Fatalf("GET /setup: status %d, leaked code %v", rec.Code, pairingCodeRE.MatchString(rec.Body.String()))
	}
	for _, code := range []string{"", "AAAAA-AAAAA"} {
		rec := h.postForm("/setup", url.Values{"setup_code": {code}, "model_api_key": {"attacker-key"}})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("code %q: status %d, want 401", code, rec.Code)
		}
		if pairingCodeRE.MatchString(rec.Body.String()) {
			t.Fatal("a pairing code was issued without the terminal code")
		}
	}
	if _, ok := h.vault.Get(vault.ModelAPIKey); ok {
		t.Fatal("the model key was saved without the terminal code")
	}
}

func TestSetupPairsTheOwnerAndThenCloses(t *testing.T) {
	h := newHarness(t, nil)
	token := h.completeSetup()

	rec := h.do("GET", "/api/v1/me", token, nil, "")
	var me gen.Device
	json.Unmarshal(rec.Body.Bytes(), &me)
	if rec.Code != http.StatusOK || me.Role != gen.Owner {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("GET", "/setup", "", nil, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /setup after pairing: %d, want 404", rec.Code)
	}
	rec = h.postForm("/setup", url.Values{"setup_code": {h.setupCode()}, "model_api_key": {"other"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /setup after pairing: %d, want 404", rec.Code)
	}
}

func TestSetupReopensWithANewCodeWhenNoOwnerDeviceIsLeft(t *testing.T) {
	h := newHarness(t, nil)
	token := h.completeSetup()
	var me gen.Device
	json.Unmarshal(h.do("GET", "/api/v1/me", token, nil, "").Body.Bytes(), &me)
	oldCode := h.setupCode()
	before := len(h.shown)
	if rec := h.do("DELETE", "/api/v1/devices/"+me.Id, token, nil, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if len(h.shown) != before+1 {
		t.Fatal("revoking the last owner device must print a new terminal code")
	}
	if rec := h.do("GET", "/setup", "", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /setup: %d, want it open again", rec.Code)
	}
	if rec := h.postForm("/setup", url.Values{"setup_code": {oldCode}}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old terminal code: %d, want 401", rec.Code)
	}
}

func TestDeviceTokenIsRequired(t *testing.T) {
	h := newHarness(t, nil)
	if rec := h.do("GET", "/api/v1/health", "", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("health: %d", rec.Code)
	}
	for _, tok := range []string{"", "hr_not-a-real-token", "garbage"} {
		if rec := h.do("GET", "/api/v1/me", tok, nil, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d, want 401", tok, rec.Code)
		}
	}
}

func TestPairRejectsUnknownFieldsAndBadCodes(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.do("POST", "/api/v1/pair", "", strings.NewReader(`{"code":"X","device_name":"x","role":"owner"}`), "application/json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d, want 400", rec.Code)
	}
	rec = h.postJSON("/api/v1/pair", "", gen.PairRequest{Code: "AAAAA-AAAAA", DeviceName: "x"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad code: %d, want 401", rec.Code)
	}
}

func TestDelegateDevicesAreLimited(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()
	delegate := h.pair(h.inviteDelegate(owner, "partner"), "partner phone")

	rec := h.do("GET", "/api/v1/me", delegate, nil, "")
	var me gen.Device
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me.Role != gen.Delegate || me.DelegateName == nil || *me.DelegateName != "partner" {
		t.Fatalf("delegate me: %s", rec.Body)
	}
	if rec := h.do("GET", "/api/v1/devices", delegate, nil, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("delegate list devices: %d, want 403", rec.Code)
	}
	if rec := h.postJSON("/api/v1/pairing-codes", delegate, gen.PairingCodeRequest{Role: gen.Owner}); rec.Code != http.StatusForbidden {
		t.Fatalf("delegate create code: %d, want 403", rec.Code)
	}
	if rec := h.do("GET", "/api/v1/devices", owner, nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("owner list devices: %d", rec.Code)
	}
}

// readEvents collects event types from a stream until it ends or times out.
func readEvents(t *testing.T, resp *http.Response, until string, timeout time.Duration) (types []string, ended bool) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if typ, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				types = append(types, typ)
				if typ == until && until != "device.revoked" {
					return
				}
			}
		}
		ended = true
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		resp.Body.Close()
		<-done
		ended = false
	}
	return types, ended
}

func openStream(t *testing.T, base, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", base+"/api/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestOwnerStreamSeesNewDevices(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()
	ts := httptest.NewServer(h.handler)
	// Cleanups run last-first, so the stream body closes before the server.
	t.Cleanup(ts.Close)
	stream := openStream(t, ts.URL, owner)
	code := h.inviteDelegate(owner, "partner")
	h.pair(code, "partner phone")
	types, _ := readEvents(t, stream, "device.paired", 2*time.Second)
	if len(types) == 0 || types[len(types)-1] != "device.paired" {
		t.Fatalf("events = %v, want device.paired", types)
	}
}

func TestRevokingADeviceEndsItsTokenAndStream(t *testing.T) {
	h := newHarness(t, nil)
	owner := h.completeSetup()
	delegate := h.pair(h.inviteDelegate(owner, "partner"), "partner phone")
	var me gen.Device
	json.Unmarshal(h.do("GET", "/api/v1/me", delegate, nil, "").Body.Bytes(), &me)

	ts := httptest.NewServer(h.handler)
	// Cleanups run last-first, so the stream body closes before the server.
	t.Cleanup(ts.Close)
	stream := openStream(t, ts.URL, delegate)
	if rec := h.do("DELETE", "/api/v1/devices/"+me.Id, owner, nil, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rec.Code)
	}
	types, ended := readEvents(t, stream, "device.revoked", 2*time.Second)
	if !ended || len(types) == 0 || types[len(types)-1] != "device.revoked" {
		t.Fatalf("events = %v ended = %v; want the stream to end after device.revoked", types, ended)
	}
	if rec := h.do("GET", "/api/v1/me", delegate, nil, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token: %d, want 401", rec.Code)
	}
}

// --- public mode ---

type testProxy struct {
	key    *rsa.PrivateKey
	server *httptest.Server
}

func newTestProxy(t *testing.T) *testProxy {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	p := &testProxy{key: key}
	p.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *testProxy) identity(t *testing.T, email string) string {
	sig, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: p.key, KeyID: "k1"}}, nil)
	raw, err := jwt.Signed(sig).Claims(map[string]any{
		"iss": "https://proxy.example.com", "aud": "aud", "email": email,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
	}).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPublicModeFailsClosedWithoutAProxyIdentity(t *testing.T) {
	p := newTestProxy(t)
	g, err := guard.New(context.Background(), config.Guard{
		Header: "X-Proxy-Identity", Issuer: "https://proxy.example.com", Audience: "aud",
		JWKSURL: p.server.URL, OwnerClaim: "email", Owner: "owner@example.com",
	}, p.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, g.Middleware)

	send := func(method, path, identity, token string, body io.Reader, ct string) int {
		r := httptest.NewRequest(method, path, body)
		r.RemoteAddr = "127.0.0.1:1"
		if identity != "" {
			r.Header.Set("X-Proxy-Identity", identity)
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, r)
		return rec.Code
	}

	// Without the proxy's identity, nothing is reachable: not health, not
	// pairing, and not setup, even with the right terminal code.
	form := url.Values{"setup_code": {h.setupCode()}, "model_api_key": {"k"}}.Encode()
	for _, c := range []struct{ method, path, body, ct string }{
		{"GET", "/api/v1/health", "", ""},
		{"POST", "/api/v1/pair", `{"code":"X","device_name":"x"}`, "application/json"},
		{"GET", "/setup", "", ""},
		{"POST", "/setup", form, "application/x-www-form-urlencoded"},
	} {
		if got := send(c.method, c.path, "", "", strings.NewReader(c.body), c.ct); got != http.StatusUnauthorized {
			t.Errorf("%s %s without identity: %d, want 401", c.method, c.path, got)
		}
		if got := send(c.method, c.path, "forged.token.value", "", strings.NewReader(c.body), c.ct); got != http.StatusUnauthorized {
			t.Errorf("%s %s with forged identity: %d, want 401", c.method, c.path, got)
		}
	}
	if got := send("GET", "/api/v1/health", p.identity(t, "intruder@example.com"), "", nil, ""); got != http.StatusForbidden {
		t.Errorf("someone else signed in: %d, want 403", got)
	}
	owner := p.identity(t, "owner@example.com")
	if got := send("GET", "/api/v1/health", owner, "", nil, ""); got != http.StatusOK {
		t.Errorf("owner signed in, health: %d, want 200", got)
	}
	// The proxy sign-in alone is not enough: the device token is still needed.
	if got := send("GET", "/api/v1/me", owner, "", nil, ""); got != http.StatusUnauthorized {
		t.Errorf("owner signed in without a device token: %d, want 401", got)
	}
}
