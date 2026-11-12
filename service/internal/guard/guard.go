// Package guard checks, in public mode, that an access proxy signed in the
// owner before a request reaches the server. It verifies the proxy's signed
// identity token itself, so a missing or misconfigured proxy fails closed.
package guard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"heyrobots/service/internal/config"
)

// Guard verifies the access proxy's identity token on every request.
type Guard struct {
	header     string
	ownerClaim string
	owner      string
	verifier   *oidc.IDTokenVerifier
}

// New builds a Guard from the configuration. client fetches the proxy's
// signing keys; pass nil for http.DefaultClient.
func New(ctx context.Context, g config.Guard, client *http.Client) (*Guard, error) {
	if g.Header == "" || g.Issuer == "" || g.Audience == "" || g.JWKSURL == "" || g.Owner == "" || g.OwnerClaim == "" {
		return nil, errors.New("guard is not fully configured")
	}
	if client != nil {
		ctx = oidc.ClientContext(ctx, client)
	}
	keys := oidc.NewRemoteKeySet(ctx, g.JWKSURL)
	return &Guard{
		header:     g.Header,
		ownerClaim: g.OwnerClaim,
		owner:      g.Owner,
		verifier: oidc.NewVerifier(g.Issuer, keys, &oidc.Config{
			ClientID:             g.Audience,
			SupportedSigningAlgs: []string{oidc.RS256, oidc.ES256},
		}),
	}, nil
}

// Errors from Check.
var (
	ErrMissing  = errors.New("access proxy sign-in required")
	ErrInvalid  = errors.New("access proxy identity is not valid")
	ErrNotOwner = errors.New("signed-in person is not this server's owner")
)

// Check verifies the identity token on a request.
func (g *Guard) Check(r *http.Request) error {
	raw := strings.TrimSpace(r.Header.Get(g.header))
	raw = strings.TrimPrefix(raw, "Bearer ")
	if raw == "" {
		return ErrMissing
	}
	tok, err := g.verifier.Verify(r.Context(), raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var claims map[string]any
	if err := tok.Claims(&claims); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if v, _ := claims[g.ownerClaim].(string); v == "" || !strings.EqualFold(v, g.owner) {
		return ErrNotOwner
	}
	return nil
}

// Middleware rejects every request that fails Check.
func (g *Guard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := g.Check(r); err != nil {
			status := http.StatusUnauthorized
			if errors.Is(err, ErrNotOwner) {
				status = http.StatusForbidden
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			// The detail stays in the server log; the response says only enough.
			msg := ErrMissing.Error()
			switch {
			case errors.Is(err, ErrNotOwner):
				msg = ErrNotOwner.Error()
			case errors.Is(err, ErrInvalid):
				msg = ErrInvalid.Error()
			}
			fmt.Fprintf(w, `{"error":%q}`, msg)
			return
		}
		next.ServeHTTP(w, r)
	})
}
