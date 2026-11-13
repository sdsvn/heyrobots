// Package config reads the server's settings from the environment and
// checks them before anything starts listening.
package config

import (
	"heyrobots/service/internal/usage"

	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultListen is where the server listens when nothing else is set: the
// machine itself only.
const DefaultListen = "127.0.0.1:7340"

// Config is the server's configuration.
type Config struct {
	// DataDir holds the database, backups, vault and workspaces.
	DataDir string
	// Listen lists the addresses to listen on, such as 127.0.0.1:7340 or a
	// Tailscale address. An address on all interfaces needs public mode.
	Listen []string
	// ServerURL is the address the companion app should use, put into
	// pairing QR codes. When empty, the address of the setup request is used.
	ServerURL string
	// TLSCert and TLSKey enable HTTPS on the listeners when both are set.
	TLSCert, TLSKey string
	Public          bool
	Guard           Guard
	// VaultKey is where the vault's key lives: "keychain" (the default on
	// macOS) or "file" (the default elsewhere, in the data folder).
	VaultKey string
	// RouterModel is the Gemini model the router and the quarantined
	// reader use.
	RouterModel string
	// WorkerModel is the Gemini model the Executors use.
	WorkerModel string
	// ACPCommand starts the claude-agent-acp adapter, from
	// HEYROBOTS_ACP_COMMAND; empty finds it on PATH or runs it with npx.
	ACPCommand []string
	// AgentModel is Claude Code's model, from HEYROBOTS_AGENT_MODEL; empty
	// keeps its default.
	AgentModel string
	// ClaudeConfigDir is Claude Code's config folder for heyrobots, from
	// HEYROBOTS_CLAUDE_CONFIG_DIR; empty uses the owner's own sign-in.
	ClaudeConfigDir string
	// ClusterListen is where this machine's link to the owner's other
	// machines listens (mutual TLS), from HEYROBOTS_CLUSTER_LISTEN; empty
	// only dials out.
	ClusterListen string
	// ClusterAddress is the address other machines dial, from
	// HEYROBOTS_CLUSTER_ADDRESS; empty uses ClusterListen when it names a host.
	ClusterAddress string
	// MachineName names this machine to the others, from
	// HEYROBOTS_MACHINE_NAME; empty uses the host name.
	MachineName string
	// WebDir serves the app's web build from a folder instead of the
	// embedded one, for development.
	WebDir string
	// ModelPrices overrides or adds model prices, from
	// HEYROBOTS_MODEL_PRICES as "model=in/out,…" in dollars per million
	// tokens.
	ModelPrices usage.Prices
}

// DefaultWorkerModel suits multi-step work with tools.
const DefaultWorkerModel = "gemini-3.5-flash"

// DefaultRouterModel is a small, fast model, which suits routing.
const DefaultRouterModel = "gemini-3.1-flash-lite"

// Guard configures the access proxy that must sign in the owner before a
// request reaches the server in public mode.
type Guard struct {
	// Header carries the proxy's signed identity token, for example
	// Cf-Access-Jwt-Assertion for Cloudflare Access.
	Header   string
	Issuer   string
	Audience string
	JWKSURL  string
	// OwnerClaim names the token claim that identifies the person, such as
	// "email" or "sub", and Owner is the value it must have.
	OwnerClaim string
	Owner      string
}

// FromEnv reads the configuration through getenv, normally os.Getenv.
func FromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		DataDir:         getenv("HEYROBOTS_DATA_DIR"),
		ServerURL:       getenv("HEYROBOTS_SERVER_URL"),
		TLSCert:         getenv("HEYROBOTS_TLS_CERT"),
		TLSKey:          getenv("HEYROBOTS_TLS_KEY"),
		Public:          getenv("HEYROBOTS_PUBLIC") == "true",
		VaultKey:        getenv("HEYROBOTS_VAULT_KEY"),
		RouterModel:     getenv("HEYROBOTS_ROUTER_MODEL"),
		WorkerModel:     getenv("HEYROBOTS_WORKER_MODEL"),
		WebDir:          getenv("HEYROBOTS_WEB_DIR"),
		ACPCommand:      strings.Fields(getenv("HEYROBOTS_ACP_COMMAND")),
		ClusterListen:   getenv("HEYROBOTS_CLUSTER_LISTEN"),
		AgentModel:      getenv("HEYROBOTS_AGENT_MODEL"),
		ClusterAddress:  getenv("HEYROBOTS_CLUSTER_ADDRESS"),
		MachineName:     getenv("HEYROBOTS_MACHINE_NAME"),
		ClaudeConfigDir: getenv("HEYROBOTS_CLAUDE_CONFIG_DIR"),
		Guard: Guard{
			Header:     getenv("HEYROBOTS_GUARD_HEADER"),
			Issuer:     getenv("HEYROBOTS_GUARD_ISSUER"),
			Audience:   getenv("HEYROBOTS_GUARD_AUDIENCE"),
			JWKSURL:    getenv("HEYROBOTS_GUARD_JWKS_URL"),
			OwnerClaim: getenv("HEYROBOTS_GUARD_OWNER_CLAIM"),
			Owner:      getenv("HEYROBOTS_GUARD_OWNER"),
		},
	}
	if c.DataDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return Config{}, fmt.Errorf("find a data folder: %w; set HEYROBOTS_DATA_DIR", err)
		}
		c.DataDir = filepath.Join(base, "heyrobots")
	}
	if v := getenv("HEYROBOTS_LISTEN"); v != "" {
		for _, a := range strings.Split(v, ",") {
			if a = strings.TrimSpace(a); a != "" {
				c.Listen = append(c.Listen, a)
			}
		}
	} else {
		c.Listen = []string{DefaultListen}
	}
	if c.RouterModel == "" {
		c.RouterModel = DefaultRouterModel
	}
	if c.WorkerModel == "" {
		c.WorkerModel = DefaultWorkerModel
	}
	prices, err := usage.ParsePrices(getenv("HEYROBOTS_MODEL_PRICES"))
	if err != nil {
		return Config{}, fmt.Errorf("HEYROBOTS_MODEL_PRICES: %w", err)
	}
	c.ModelPrices = prices
	if c.VaultKey == "" {
		c.VaultKey = "file"
		if runtime.GOOS == "darwin" {
			c.VaultKey = "keychain"
		}
	}
	if c.Guard.OwnerClaim == "" {
		c.Guard.OwnerClaim = "email"
	}
	return c, c.Validate()
}

// Validate checks the configuration. It refuses anything that would expose
// the server without public mode's guard.
func (c Config) Validate() error {
	var errs []error
	if len(c.Listen) == 0 {
		errs = append(errs, errors.New("no listen address"))
	}
	for _, a := range c.Listen {
		host, _, err := net.SplitHostPort(a)
		if err != nil {
			errs = append(errs, fmt.Errorf("listen address %q: %w", a, err))
			continue
		}
		if !c.Public && allInterfaces(host) {
			errs = append(errs, fmt.Errorf("listen address %q is on all interfaces; that needs public mode with an access guard", a))
		}
	}
	if c.VaultKey != "keychain" && c.VaultKey != "file" {
		errs = append(errs, fmt.Errorf("HEYROBOTS_VAULT_KEY must be keychain or file, not %q", c.VaultKey))
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		errs = append(errs, errors.New("set both HEYROBOTS_TLS_CERT and HEYROBOTS_TLS_KEY, or neither"))
	}
	if c.ServerURL != "" {
		if u, err := url.Parse(c.ServerURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("HEYROBOTS_SERVER_URL %q is not an http or https URL", c.ServerURL))
		}
	}
	if c.Public {
		g := c.Guard
		for name, v := range map[string]string{
			"HEYROBOTS_GUARD_HEADER":   g.Header,
			"HEYROBOTS_GUARD_ISSUER":   g.Issuer,
			"HEYROBOTS_GUARD_AUDIENCE": g.Audience,
			"HEYROBOTS_GUARD_JWKS_URL": g.JWKSURL,
			"HEYROBOTS_GUARD_OWNER":    g.Owner,
		} {
			if v == "" {
				errs = append(errs, fmt.Errorf("public mode needs %s", name))
			}
		}
		if g.JWKSURL != "" && !strings.HasPrefix(g.JWKSURL, "https://") {
			errs = append(errs, errors.New("HEYROBOTS_GUARD_JWKS_URL must use https"))
		}
		if !strings.HasPrefix(c.ServerURL, "https://") {
			errs = append(errs, errors.New("public mode needs an https HEYROBOTS_SERVER_URL"))
		}
	}
	return errors.Join(errs...)
}

func allInterfaces(host string) bool {
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}
