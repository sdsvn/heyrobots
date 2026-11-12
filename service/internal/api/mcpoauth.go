package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/gateway"
)

// signInTimeout is how long a sign-in waits for the browser to come back.
const signInTimeout = 10 * time.Minute

// mcpSignIn is a sign-in to an MCP server waiting for the browser to come
// back with a code; nil means the owner declined.
type mcpSignIn struct {
	name string
	code chan *auth.AuthorizationResult
	done chan error
}

// mcpSignIns are the sign-ins under way, by their one-time state.
type mcpSignIns struct {
	mu sync.Mutex
	m  map[string]*mcpSignIn
}

func (p *mcpSignIns) put(state string, si *mcpSignIn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]*mcpSignIn{}
	}
	p.m[state] = si
}

func (p *mcpSignIns) take(state string) *mcpSignIn {
	p.mu.Lock()
	defer p.mu.Unlock()
	si := p.m[state]
	delete(p.m, state)
	return si
}

// startMCPSignIn asks a server that refused heyrobots how to sign in, with
// the go-sdk's OAuth flow: its metadata, a client registered for heyrobots
// and PKCE. It answers the address to open, or "" when the server offers no
// sign-in heyrobots can use; the flow then waits for finishMCPSignIn.
func (s *Server) startMCPSignIn(name, addr string) string {
	if s.opts.MCPRedirect == "" || addr == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), signInTimeout)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, addr, strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"heyrobots","version":"0.1"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		return ""
	}
	if resp.StatusCode != http.StatusUnauthorized {
		resp.Body.Close()
		cancel()
		return ""
	}
	si := &mcpSignIn{name: name, code: make(chan *auth.AuthorizationResult, 1), done: make(chan error, 1)}
	open := make(chan string, 1)
	h, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			ClientName: "heyrobots", RedirectURIs: []string{s.opts.MCPRedirect}, TokenEndpointAuthMethod: "none",
			GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}}},
		RequestRefreshToken: true, AcceptUnadvertisedIss: true,
		AuthorizationCodeFetcher: func(ctx context.Context, a *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			u, err := url.Parse(a.URL)
			if err != nil {
				return nil, err
			}
			state := u.Query().Get("state")
			s.signIns.put(state, si)
			defer s.signIns.take(state)
			open <- a.URL
			select {
			case r := <-si.code:
				if r == nil {
					return nil, errors.New("you declined; nothing was connected")
				}
				return r, nil
			case <-ctx.Done():
				return nil, errors.New("the sign-in took too long; connect again")
			}
		},
		// The token set goes to the vault, where the gateway refreshes it.
		NewTokenSource: func(_ context.Context, cfg *oauth2.Config, tok *oauth2.Token) (oauth2.TokenSource, error) {
			b, _ := json.Marshal(gateway.Secrets{OAuth: gateway.NewOAuth(cfg, tok)})
			return oauth2.StaticTokenSource(tok), s.opts.Vault.Set(gateway.SecretsKey(name), string(b))
		},
	})
	if err != nil {
		resp.Body.Close()
		cancel()
		return ""
	}
	go func() {
		defer cancel()
		si.done <- h.Authorize(ctx, req, resp)
	}()
	select {
	case u := <-open:
		return u
	case err := <-si.done:
		s.opts.Log.Info("mcp server has no sign-in", "server", name, "err", err)
		return ""
	}
}

// finishMCPSignIn hands the code the browser came back with to the waiting
// sign-in and reads the server's tools with the new token. It answers the
// server's name.
func (s *Server) finishMCPSignIn(ctx context.Context, q url.Values) (string, error) {
	si := s.signIns.take(q.Get("state"))
	if si == nil {
		return "", errors.New("that sign-in is over or unknown; connect the app again")
	}
	if e := q.Get("error"); e != "" || q.Get("code") == "" {
		si.code <- nil
		<-si.done
		if e == "" {
			e = "no sign-in code"
		}
		return "", errors.New("the server answered: " + e)
	}
	si.code <- &auth.AuthorizationResult{Code: q.Get("code"), State: q.Get("state"), Iss: q.Get("iss")}
	if err := <-si.done; err != nil {
		return "", err
	}
	s.opts.Log.Info("mcp server signed in", "server", si.name)
	if srv := s.syncMCP(ctx, si.name); srv.StatusError != "" {
		return si.name, errors.New("signed in, but can't reach it: " + srv.StatusError)
	}
	return si.name, nil
}

// MCPCallback is where an MCP server's sign-in sends the browser back to,
// on the computer running heyrobots. The one-time state ties it to a
// sign-in an owner device started.
func (s *Server) MCPCallback(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	view := struct{ Error, Email, Address string }{}
	if name, err := s.finishMCPSignIn(r.Context(), r.URL.Query()); err != nil {
		view.Error = err.Error()
		w.WriteHeader(http.StatusBadRequest)
	} else {
		view.Email = name
	}
	callbackTmpl.Execute(w, view)
}

// FinishAppConnect finishes signing in to an MCP app with the address the
// browser ended on. Owner devices only.
func (s *Server) FinishAppConnect(w http.ResponseWriter, r *http.Request, appID string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.GoogleConnectFinish
	if !decode(w, r, &req) {
		return
	}
	u, err := url.Parse(strings.TrimSpace(req.RedirectUrl))
	if err != nil {
		writeError(w, http.StatusBadRequest, "paste the whole address from the browser")
		return
	}
	name, err := s.finishMCPSignIn(r.Context(), u.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.connectResult(w, name, gen.ConnectResult{})
}
