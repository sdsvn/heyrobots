package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaultsAreLocalOnly(t *testing.T) {
	c, err := FromEnv(env(map[string]string{"HEYROBOTS_DATA_DIR": t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Listen) != 1 || c.Listen[0] != DefaultListen || c.Public {
		t.Fatalf("unexpected defaults %+v", c)
	}
}

func TestAllInterfacesNeedsPublicMode(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:7340", ":7340", "[::]:7340"} {
		_, err := FromEnv(env(map[string]string{"HEYROBOTS_DATA_DIR": t.TempDir(), "HEYROBOTS_LISTEN": addr}))
		if err == nil || !strings.Contains(err.Error(), "all interfaces") {
			t.Errorf("%s: err = %v, want all-interfaces refusal", addr, err)
		}
	}
}

func TestPrivateAddressIsAllowed(t *testing.T) {
	_, err := FromEnv(env(map[string]string{
		"HEYROBOTS_DATA_DIR": t.TempDir(),
		"HEYROBOTS_LISTEN":   "127.0.0.1:7340, 100.64.0.5:7340",
	}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestPublicModeNeedsTheWholeGuard(t *testing.T) {
	_, err := FromEnv(env(map[string]string{
		"HEYROBOTS_DATA_DIR": t.TempDir(),
		"HEYROBOTS_PUBLIC":   "true",
		"HEYROBOTS_LISTEN":   "0.0.0.0:7340",
	}))
	if err == nil {
		t.Fatal("public mode without a guard must be refused")
	}
	for _, want := range []string{"HEYROBOTS_GUARD_ISSUER", "HEYROBOTS_GUARD_JWKS_URL", "HEYROBOTS_GUARD_OWNER", "https HEYROBOTS_SERVER_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestPublicModeWithGuard(t *testing.T) {
	c, err := FromEnv(env(map[string]string{
		"HEYROBOTS_DATA_DIR":       t.TempDir(),
		"HEYROBOTS_PUBLIC":         "true",
		"HEYROBOTS_LISTEN":         "0.0.0.0:7340",
		"HEYROBOTS_SERVER_URL":     "https://robots.example.com",
		"HEYROBOTS_GUARD_HEADER":   "Cf-Access-Jwt-Assertion",
		"HEYROBOTS_GUARD_ISSUER":   "https://team.cloudflareaccess.com",
		"HEYROBOTS_GUARD_AUDIENCE": "aud-tag",
		"HEYROBOTS_GUARD_JWKS_URL": "https://team.cloudflareaccess.com/cdn-cgi/access/certs",
		"HEYROBOTS_GUARD_OWNER":    "me@example.com",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Guard.OwnerClaim != "email" {
		t.Fatalf("OwnerClaim = %q, want email", c.Guard.OwnerClaim)
	}
}
