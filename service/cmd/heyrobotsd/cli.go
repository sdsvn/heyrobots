package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"

	"heyrobots/service/internal/config"
)

const usageText = `heyrobotsd runs heyrobots: your agents, their brains, and the server the app talks to.

Usage:
  heyrobotsd               start the server (the install script runs it as a background service)
  heyrobotsd pair          print a one-time code to pair the app or a browser on this machine
  heyrobotsd pair --link   print just the heyrobots:// link that signs the app in
  heyrobotsd pair --web    print just the web address that signs a browser in
  heyrobotsd status        say whether a server is running with this data folder (exit 3 if not)
  heyrobotsd config        show the settings in effect, and where each comes from
  heyrobotsd version       print the version
  heyrobotsd help          show this

Settings are environment variables (HEYROBOTS_*); "heyrobotsd config" lists them all.
The background service has its own copy of them, in %s.
`

func helpCommand() {
	fmt.Printf(usageText, serviceFile())
}

// serviceFile is where the install script keeps the background service's
// settings on this machine.
func serviceFile() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library/LaunchAgents/dev.heyrobots.heyrobotsd.plist")
	}
	return filepath.Join(home, ".config/systemd/user/heyrobots.service")
}

// configCommand prints each setting, its value, and whether it was set or
// is the default.
func configCommand() error {
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		return fmt.Errorf("configuration:\n%w", err)
	}
	none := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	rows := []struct{ env, value, about string }{
		{"HEYROBOTS_DATA_DIR", cfg.DataDir, "database, vault, workspaces and files"},
		{"HEYROBOTS_LISTEN", strings.Join(cfg.Listen, ","), "addresses the server listens on"},
		{"HEYROBOTS_SERVER_URL", none(cfg.ServerURL), "address put into pairing codes"},
		{"HEYROBOTS_TLS_CERT", none(cfg.TLSCert), "HTTPS certificate (with HEYROBOTS_TLS_KEY)"},
		{"HEYROBOTS_TLS_KEY", none(cfg.TLSKey), "HTTPS key"},
		{"HEYROBOTS_PUBLIC", fmt.Sprint(cfg.Public), "listen beyond private networks, behind an access guard"},
		{"HEYROBOTS_VAULT_KEY", cfg.VaultKey, "where the vault key lives: keychain or file"},
		{"HEYROBOTS_ROUTER_MODEL", cfg.RouterModel, "Gemini model for routing, until one is chosen in the app"},
		{"HEYROBOTS_WORKER_MODEL", cfg.WorkerModel, "Gemini model for tasks, until one is chosen in the app"},
		{"HEYROBOTS_ACP_COMMAND", none(strings.Join(cfg.ACPCommand, " ")), "how to start Claude Code's adapter"},
		{"HEYROBOTS_AGENT_MODEL", none(cfg.AgentModel), "Claude Code's model"},
		{"HEYROBOTS_CLAUDE_CONFIG_DIR", none(cfg.ClaudeConfigDir), "Claude Code's config folder (- uses your own sign-in)"},
		{"HEYROBOTS_CLUSTER_LISTEN", none(cfg.ClusterListen), "where the link to your other machines listens"},
		{"HEYROBOTS_CLUSTER_ADDRESS", none(cfg.ClusterAddress), "address your other machines dial"},
		{"HEYROBOTS_MACHINE_NAME", none(cfg.MachineName), "this machine's name to the others"},
		{"HEYROBOTS_MODEL_PRICES", none(os.Getenv("HEYROBOTS_MODEL_PRICES")), "extra model prices, model=in/out per million tokens"},
		{"HEYROBOTS_WEB_DIR", none(cfg.WebDir), "serve the web app from a folder (development)"},
		{"HEYROBOTS_APP_DIR", none(os.Getenv("HEYROBOTS_APP_DIR")), "where the Mac app is, for updates"},
		{"HEYROBOTS_UPDATE_API", none(os.Getenv("HEYROBOTS_UPDATE_API")), "a release server in place of GitHub's, for testing"},
	}
	if cfg.Public {
		g := cfg.Guard
		rows = append(rows,
			struct{ env, value, about string }{"HEYROBOTS_GUARD_HEADER", g.Header, "header with the access proxy's token"},
			struct{ env, value, about string }{"HEYROBOTS_GUARD_ISSUER", g.Issuer, "token issuer"},
			struct{ env, value, about string }{"HEYROBOTS_GUARD_AUDIENCE", g.Audience, "token audience"},
			struct{ env, value, about string }{"HEYROBOTS_GUARD_JWKS_URL", g.JWKSURL, "token signing keys"},
			struct{ env, value, about string }{"HEYROBOTS_GUARD_OWNER_CLAIM", g.OwnerClaim, "claim naming the owner"},
			struct{ env, value, about string }{"HEYROBOTS_GUARD_OWNER", g.Owner, "the owner's value of that claim"},
		)
	}
	fmt.Printf("heyrobotsd %s, settings from this shell (* = set here, the rest are defaults)\n\n", version)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		mark := " "
		if os.Getenv(r.env) != "" {
			mark = "*"
		}
		fmt.Fprintf(w, "%s %s\t%s\t%s\n", mark, r.env, r.value, r.about)
	}
	w.Flush()
	fmt.Printf("\nThe background service has its own settings, in %s.\n", serviceFile())
	return nil
}
