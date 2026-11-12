package google_test

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"heyrobots/service/internal/google"
	"heyrobots/service/internal/google/googletest"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/vault"
)

func setup(t *testing.T) (*google.Google, *googletest.Fake, *vault.Vault) {
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
	f := googletest.New(t)
	return &google.Google{Vault: v, Store: st, Ends: f.Endpoints(), HTTP: f.Server.Client()}, f, v
}

const redirect = "http://127.0.0.1:7340/oauth/google/callback"

func TestConnectingUsesTheOwnersClientAndPKCE(t *testing.T) {
	g, f, v := setup(t)
	if _, _, err := g.Start(redirect, false, ""); !errors.Is(err, google.ErrNoClient) {
		t.Fatalf("started without a client: %v", err)
	}
	if err := g.SetClient(google.ClientConfig{ClientID: "abc", ClientSecret: "s"}); err == nil {
		t.Fatal("accepted a client id that isn't Google's")
	}
	if err := g.SetClient(google.ClientConfig{ClientID: "123.apps.googleusercontent.com", ClientSecret: "sec"}); err != nil {
		t.Fatal(err)
	}

	authURL, state, err := g.Start(redirect, false, "")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("access_type") != "offline" || q.Get("redirect_uri") != redirect || q.Get("state") != state {
		t.Fatalf("auth url %s", authURL)
	}
	if strings.Contains(q.Get("scope"), "gmail.send") {
		t.Fatal("asked to send without the owner allowing it")
	}

	back, _ := url.Parse(f.Authorize(authURL))
	if _, err := g.Finish(context.Background(), "wrong-state", back.Query().Get("code")); !errors.Is(err, google.ErrUnknownFlow) {
		t.Fatalf("finished with a forged state: %v", err)
	}
	// A wrong state also spends nothing: the real one still works once.
	acc, err := g.Finish(context.Background(), state, back.Query().Get("code"))
	if err != nil {
		t.Fatal(err)
	}
	if acc.Email != "ann@gmail.com" || acc.AssistantAddress != "ann+assistant@gmail.com" || acc.Status != store.AccountConnected {
		t.Fatalf("%+v", acc)
	}
	if _, err := g.Finish(context.Background(), state, back.Query().Get("code")); !errors.Is(err, google.ErrUnknownFlow) {
		t.Fatal("a sign-in finished twice")
	}
	if raw, _ := v.Get(vault.GoogleToken); !strings.Contains(raw, "rt-1") {
		t.Fatal("the token isn't in the vault")
	}

	// Disconnecting revokes with Google and forgets everything.
	if err := g.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.Revoked) != 1 || f.Revoked[0] != "rt-1" {
		t.Fatalf("revoked %v", f.Revoked)
	}
	if _, ok := v.Get(vault.GoogleToken); ok {
		t.Fatal("token kept after disconnecting")
	}
	if _, err := g.Account(); !errors.Is(err, google.ErrNotConnected) {
		t.Fatal("account kept after disconnecting")
	}
}

func TestAConnectionWithoutARefreshTokenIsRefused(t *testing.T) {
	g, f, _ := setup(t)
	g.SetClient(google.ClientConfig{ClientID: "1.apps.googleusercontent.com", ClientSecret: "s"})
	f.NoRefresh = true
	authURL, state, _ := g.Start(redirect, true, "robot")
	back, _ := url.Parse(f.Authorize(authURL))
	if _, err := g.Finish(context.Background(), state, back.Query().Get("code")); err == nil || !strings.Contains(err.Error(), "refresh token") {
		t.Fatalf("got %v", err)
	}
}

func TestPlusAddress(t *testing.T) {
	for in, want := range map[string]string{
		"ann@gmail.com":        "ann+assistant@gmail.com",
		"ann+news@example.com": "ann+assistant@example.com",
	} {
		if got := google.PlusAddress(in, "assistant"); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
	g, _, _ := setup(t)
	g.SetClient(google.ClientConfig{ClientID: "1.apps.googleusercontent.com", ClientSecret: "s"})
	if _, _, err := g.Start(redirect, false, "bad tag"); err == nil {
		t.Fatal("accepted a tag with a space")
	}
}
