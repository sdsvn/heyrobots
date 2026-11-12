// Package google connects the owner's Google account through the owner's
// own OAuth client, and talks to the Gmail and Calendar APIs with plain
// HTTP. Tokens stay in the vault; the model never sees them.
//
// Because each install has one owner, the owner registers their own OAuth
// client in Google Cloud. Google's personal-use exception then lets the
// app use Gmail scopes without verification. The setup guide explains the
// steps and the "unverified app" warning.
package google

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"heyrobots/service/internal/store"
	"heyrobots/service/internal/vault"
)

// Provider is the account row's provider name.
const Provider = "google"

// Scopes. Reading and drafting come first; sending mail and changing the
// calendar are a separate grant the owner gives only when they want
// approved actions to go out.
const (
	ScopeEmail        = "email"
	ScopeGmailRead    = "https://www.googleapis.com/auth/gmail.readonly"
	ScopeGmailCompose = "https://www.googleapis.com/auth/gmail.compose"
	ScopeGmailSend    = "https://www.googleapis.com/auth/gmail.send"
	ScopeCalendarRead = "https://www.googleapis.com/auth/calendar.readonly"
	ScopeCalendarEdit = "https://www.googleapis.com/auth/calendar.events"
)

// ReadScopes and ActScopes are the two levels the owner can grant.
var (
	ReadScopes = []string{"openid", ScopeEmail, ScopeGmailRead, ScopeGmailCompose, ScopeCalendarRead}
	ActScopes  = []string{ScopeGmailSend, ScopeCalendarEdit}
)

// Endpoints are Google's addresses; tests point them at a fake.
type Endpoints struct {
	AuthURL      string
	TokenURL     string
	RevokeURL    string
	UserInfoURL  string
	GmailBase    string
	CalendarBase string
}

// Live are Google's real endpoints.
var Live = Endpoints{
	AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL:     "https://oauth2.googleapis.com/token",
	RevokeURL:    "https://oauth2.googleapis.com/revoke",
	UserInfoURL:  "https://openidconnect.googleapis.com/v1/userinfo",
	GmailBase:    "https://gmail.googleapis.com/gmail/v1/users/me",
	CalendarBase: "https://www.googleapis.com/calendar/v3",
}

// ClientConfig is the owner's OAuth client.
type ClientConfig struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// Errors.
var (
	ErrNoClient     = errors.New("add your Google OAuth client first")
	ErrNotConnected = errors.New("no Google account is connected")
	ErrUnknownFlow  = errors.New("that sign-in link has expired or was already used; start again")
	ErrNeedsSend    = errors.New("sending needs the extra permission; connect Google again and allow sending")
)

// Google holds the connection.
type Google struct {
	Vault *vault.Vault
	Store *store.Store
	Ends  Endpoints
	// HTTP is the base client for Google; tests pass a fake server's.
	HTTP *http.Client
	Now  func() time.Time

	mu    sync.Mutex
	flows map[string]flow
}

type flow struct {
	verifier string
	redirect string
	suffix   string
	expires  time.Time
}

// FlowTTL is how long a sign-in link works.
const FlowTTL = 15 * time.Minute

// Client returns the saved OAuth client.
func (g *Google) Client() (ClientConfig, error) {
	raw, ok := g.Vault.Get(vault.GoogleClient)
	if !ok || raw == "" {
		return ClientConfig{}, ErrNoClient
	}
	var c ClientConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return ClientConfig{}, err
	}
	return c, nil
}

// SetClient saves the owner's OAuth client.
func (g *Google) SetClient(c ClientConfig) error {
	c.ClientID, c.ClientSecret = strings.TrimSpace(c.ClientID), strings.TrimSpace(c.ClientSecret)
	if !strings.HasSuffix(c.ClientID, ".apps.googleusercontent.com") || c.ClientSecret == "" {
		return errors.New("enter the client ID (ending in .apps.googleusercontent.com) and secret of a Desktop app client")
	}
	b, _ := json.Marshal(c)
	return g.Vault.Set(vault.GoogleClient, string(b))
}

func (g *Google) oauth(c ClientConfig, redirect string, scopes []string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: c.ClientID, ClientSecret: c.ClientSecret, RedirectURL: redirect, Scopes: scopes,
		Endpoint: oauth2.Endpoint{AuthURL: g.Ends.AuthURL, TokenURL: g.Ends.TokenURL, AuthStyle: oauth2.AuthStyleInParams},
	}
}

func (g *Google) ctx(ctx context.Context) context.Context {
	if g.HTTP != nil {
		return context.WithValue(ctx, oauth2.HTTPClient, g.HTTP)
	}
	return ctx
}

// Start begins a sign-in. redirect is the server's own callback on the
// loopback address, which Desktop app clients accept on any port. send
// asks for permission to send mail and change the calendar as well.
// suffix is the plus-address tag for the Assistant's address.
func (g *Google) Start(redirect string, send bool, suffix string) (authURL, state string, err error) {
	c, err := g.Client()
	if err != nil {
		return "", "", err
	}
	if suffix == "" {
		suffix = "assistant"
	}
	if !validSuffix(suffix) {
		return "", "", errors.New("the address tag uses only letters, digits, - and _")
	}
	state, verifier := random(24), oauth2.GenerateVerifier()
	scopes := ReadScopes
	if send {
		scopes = append(append([]string{}, ReadScopes...), ActScopes...)
	}
	u := g.oauth(c, redirect, scopes).AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("prompt", "consent"), oauth2.SetAuthURLParam("include_granted_scopes", "true"))
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.flows == nil {
		g.flows = map[string]flow{}
	}
	now := g.now()
	for k, f := range g.flows {
		if now.After(f.expires) {
			delete(g.flows, k)
		}
	}
	g.flows[state] = flow{verifier: verifier, redirect: redirect, suffix: suffix, expires: now.Add(FlowTTL)}
	return u, state, nil
}

// Finish completes a sign-in with the code Google sent back. It saves the
// token and the account, and returns the account.
func (g *Google) Finish(ctx context.Context, state, code string) (store.Account, error) {
	g.mu.Lock()
	f, ok := g.flows[state]
	delete(g.flows, state)
	g.mu.Unlock()
	if !ok || g.now().After(f.expires) {
		return store.Account{}, ErrUnknownFlow
	}
	c, err := g.Client()
	if err != nil {
		return store.Account{}, err
	}
	tok, err := g.oauth(c, f.redirect, nil).Exchange(g.ctx(ctx), code, oauth2.VerifierOption(f.verifier))
	if err != nil {
		return store.Account{}, fmt.Errorf("google refused the sign-in: %w", err)
	}
	if tok.RefreshToken == "" {
		return store.Account{}, errors.New("google gave no refresh token; remove heyrobots from your Google account's third-party access and connect again")
	}
	hc := oauth2.NewClient(g.ctx(ctx), oauth2.StaticTokenSource(tok))
	var info struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := getJSON(ctx, hc, g.Ends.UserInfoURL, &info); err != nil {
		return store.Account{}, fmt.Errorf("read the account's address: %w", err)
	}
	if info.Email == "" || !info.EmailVerified {
		return store.Account{}, errors.New("google didn't confirm the account's address")
	}
	if err := g.saveToken(tok); err != nil {
		return store.Account{}, err
	}
	scopes, _ := tok.Extra("scope").(string)
	email := strings.ToLower(info.Email)
	a := store.Account{Provider: Provider, Email: email, Scopes: scopes, AssistantAddress: PlusAddress(email, f.suffix),
		ConnectedAt: g.now().UTC(), Status: store.AccountConnected}
	err = g.Store.Tx(func(tx *store.Tx) error {
		if old, err := tx.Account(Provider); err == nil && old.Email == email {
			// Reconnecting the same account keeps where polling stopped.
			a.MailCursor, a.CalendarCursor = old.MailCursor, old.CalendarCursor
		}
		return tx.SaveAccount(a)
	})
	return a, err
}

// Disconnect revokes the token with Google and forgets the account.
func (g *Google) Disconnect(ctx context.Context) error {
	if raw, ok := g.Vault.Get(vault.GoogleToken); ok {
		var tok oauth2.Token
		if json.Unmarshal([]byte(raw), &tok) == nil {
			t := tok.RefreshToken
			if t == "" {
				t = tok.AccessToken
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, g.Ends.RevokeURL, strings.NewReader(url.Values{"token": {t}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if resp, err := g.http().Do(req); err == nil {
				resp.Body.Close()
			}
		}
	}
	if err := g.Vault.Delete(vault.GoogleToken); err != nil {
		return err
	}
	return g.Store.Tx(func(tx *store.Tx) error { return tx.DeleteAccount(Provider) })
}

// HTTPClient returns a client that signs requests as the connected
// account, refreshing and saving the token as needed.
func (g *Google) HTTPClient(ctx context.Context) (*http.Client, error) {
	raw, ok := g.Vault.Get(vault.GoogleToken)
	if !ok {
		return nil, ErrNotConnected
	}
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		return nil, err
	}
	c, err := g.Client()
	if err != nil {
		return nil, err
	}
	src := oauth2.ReuseTokenSource(&tok, &savingSource{g: g, base: g.oauth(c, "", nil).TokenSource(g.ctx(ctx), &tok)})
	return oauth2.NewClient(g.ctx(ctx), src), nil
}

// savingSource stores refreshed tokens, and marks the account broken when
// Google refuses to refresh.
type savingSource struct {
	g    *Google
	base oauth2.TokenSource
}

func (s *savingSource) Token() (*oauth2.Token, error) {
	t, err := s.base.Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.ErrorCode == "invalid_grant" {
			_ = s.g.Store.Tx(func(tx *store.Tx) error {
				return tx.UpdateAccount(Provider, map[string]any{"status": store.AccountBroken, "status_error": "Google no longer accepts the sign-in. Connect again."})
			})
		}
		return nil, err
	}
	if err := s.g.saveToken(t); err != nil {
		return nil, err
	}
	return t, nil
}

func (g *Google) saveToken(t *oauth2.Token) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return g.Vault.Set(vault.GoogleToken, string(b))
}

// Account returns the connected account.
func (g *Google) Account() (store.Account, error) {
	var a store.Account
	err := g.Store.Tx(func(tx *store.Tx) error {
		var err error
		a, err = tx.Account(Provider)
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		return a, ErrNotConnected
	}
	return a, err
}

// HasScope reports whether the owner granted a scope.
func HasScope(acc store.Account, scope string) bool {
	return strings.Contains(" "+acc.Scopes+" ", " "+scope+" ")
}

func (g *Google) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Google) http() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return http.DefaultClient
}

// PlusAddress tags an address: ann@gmail.com becomes ann+assistant@gmail.com.
func PlusAddress(email, suffix string) string {
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return email
	}
	local := email[:at]
	if i := strings.IndexByte(local, '+'); i >= 0 {
		local = local[:i]
	}
	return local + "+" + suffix + email[at:]
}

func validSuffix(s string) bool {
	if len(s) > 32 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func random(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// APIError is an error answer from a Google API.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("google answered %d: %s", e.Status, e.Message) }

func getJSON(ctx context.Context, c *http.Client, u string, out any) error {
	return doJSON(ctx, c, http.MethodGet, u, nil, out)
}

func doJSON(ctx context.Context, c *http.Client, method, u string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Message: msg}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}
