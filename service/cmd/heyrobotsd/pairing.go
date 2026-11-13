package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
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
