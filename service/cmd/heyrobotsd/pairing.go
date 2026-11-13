package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"heyrobots/service/internal/config"
	"heyrobots/service/internal/devices"
	"heyrobots/service/internal/store"
)

// pairingBlock is what the terminal shows at start: a QR code a phone
// scans, the addresses to open, and the code to type. The QR code holds
// the same payload as the setup page's: every address, best first, with
// this machine's own last.
func pairingBlock(code string, expires time.Time, servers []string, local string) string {
	all := slices.Clone(servers)
	if !slices.Contains(all, local) {
		all = append(all, local)
	}
	payload, _ := json.Marshal(map[string]any{"v": 1, "server": all[0], "servers": all, "code": code})
	var b strings.Builder
	b.WriteString("\n  Pair a device with heyrobots\n\n")
	if q, err := qrcode.New(string(payload), qrcode.Low); err == nil {
		// Dark modules are spaces and light ones blocks, so on a dark
		// terminal the code reads dark on a light border, as scanners expect.
		for _, line := range strings.Split(strings.TrimRight(q.ToSmallString(false), "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
		b.WriteString("\n  Scan it with the heyrobots app, or open one of these and enter the code:\n")
	} else {
		b.WriteString("  Open one of these and enter the code:\n")
	}
	for _, u := range all {
		note := ""
		if u == local {
			note = "   (on this machine)"
		}
		fmt.Fprintf(&b, "    %s%s\n", u, note)
	}
	fmt.Fprintf(&b, "\n  Pairing code: %s   works once, until %s\n  Restart the server for a new one.\n\n", code, expires.Local().Format("15:04"))
	return b.String()
}

// pairCommand runs "heyrobotsd pair": it makes a one-time owner pairing
// code for a device on this machine, next to a running server, and prints
// it. With --link it prints only a heyrobots:// link that signs the app in;
// with --web, only the address that signs a browser in. The installer uses
// these so the first start needs no pairing step.
func pairCommand(args []string) error {
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		return fmt.Errorf("configuration:\n%w", err)
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	c, err := devices.New(st, time.Now).NewCode(devices.RoleOwner, "", "this machine")
	if err != nil {
		return err
	}
	scheme := "http"
	if cfg.TLSCert != "" {
		scheme = "https"
	}
	local := scheme + "://" + localAddr(cfg.Listen[0])
	link := "heyrobots://pair?server=" + url.QueryEscape(local) + "&code=" + url.QueryEscape(c.Code)
	web := local + "/?pair=" + url.QueryEscape(c.Code)
	switch {
	case slices.Contains(args, "--link"):
		fmt.Println(link)
	case slices.Contains(args, "--web"):
		fmt.Println(web)
	default:
		fmt.Printf("Pairing code: %s   works once, until %s\n  In the app on this machine: %s\n  In a browser on this machine: %s\n", c.Code, c.ExpiresAt.Local().Format("15:04"), link, web)
	}
	return nil
}

// statusCommand runs "heyrobotsd status": it says whether a server is
// running with this data folder, and exits 3 when none is.
func statusCommand() error {
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		return fmt.Errorf("configuration:\n%w", err)
	}
	if dataInUse(cfg.DataDir) {
		fmt.Println("running, with", cfg.DataDir)
		return nil
	}
	fmt.Println("not running, with", cfg.DataDir)
	os.Exit(3)
	return nil
}
