package guard

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"heyrobots/service/internal/config"
)

const (
	issuer   = "https://proxy.example.com"
	audience = "heyrobots-aud"
	owner    = "owner@example.com"
)

type proxy struct {
	key    *rsa.PrivateKey
	server *httptest.Server
}

func newProxy(t *testing.T) *proxy {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &proxy{key: key}
	p.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *proxy) sign(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "k1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.Signed(sig).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func validClaims() map[string]any {
	return map[string]any{
		"iss":   issuer,
		"aud":   audience,
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
		"sub":   "user-1",
		"email": owner,
	}
}

func newGuard(t *testing.T, p *proxy) *Guard {
	t.Helper()
	g, err := New(context.Background(), config.Guard{
		Header: "X-Proxy-Identity", Issuer: issuer, Audience: audience,
		JWKSURL: p.server.URL, OwnerClaim: "email", Owner: owner,
	}, p.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func request(token string) *http.Request {
	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	if token != "" {
		r.Header.Set("X-Proxy-Identity", token)
	}
	return r
}

func TestGuardFailsClosed(t *testing.T) {
	p := newProxy(t)
	g := newGuard(t, p)
	stranger, _ := rsa.GenerateKey(rand.Reader, 2048)

	expired := validClaims()
	expired["exp"] = time.Now().Add(-time.Minute).Unix()
	wrongAud := validClaims()
	wrongAud["aud"] = "someone-else"
	wrongIss := validClaims()
	wrongIss["iss"] = "https://evil.example.com"
	notOwner := validClaims()
	notOwner["email"] = "intruder@example.com"
	noEmail := validClaims()
	delete(noEmail, "email")

	tests := []struct {
		name  string
		token string
		want  error
	}{
		{"no identity", "", ErrMissing},
		{"not a token", "garbage", ErrInvalid},
		{"signed by another key", p.sign(t, stranger, validClaims()), ErrInvalid},
		{"expired", p.sign(t, p.key, expired), ErrInvalid},
		{"wrong audience", p.sign(t, p.key, wrongAud), ErrInvalid},
		{"wrong issuer", p.sign(t, p.key, wrongIss), ErrInvalid},
		{"someone else signed in", p.sign(t, p.key, notOwner), ErrNotOwner},
		{"no owner claim", p.sign(t, p.key, noEmail), ErrNotOwner},
		{"owner signed in", p.sign(t, p.key, validClaims()), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := g.Check(request(tt.token)); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestMiddlewareBlocksBeforeHandler(t *testing.T) {
	p := newProxy(t)
	g := newGuard(t, p)
	called := false
	h := g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request(""))
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("status %d called %v; want 401 and no handler", rec.Code, called)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, request(p.sign(t, p.key, validClaims())))
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("status %d called %v; want the handler to run", rec.Code, called)
	}
}

func TestUnreachableKeysFailClosed(t *testing.T) {
	p := newProxy(t)
	g := newGuard(t, p)
	token := p.sign(t, p.key, validClaims())
	p.server.Close()
	if err := g.Check(request(token)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid when the proxy's keys can't be fetched", err)
	}
}

func TestIncompleteConfigIsRefused(t *testing.T) {
	if _, err := New(context.Background(), config.Guard{Header: "X"}, nil); err == nil {
		t.Fatal("an incomplete guard must be refused")
	}
}
