package main

import (
	"net/netip"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPairingAddressesPutTheTailnetFirst(t *testing.T) {
	listen := withTailnet([]string{"127.0.0.1:7340", "192.168.1.110:7340"}, []netip.Addr{netip.MustParseAddr("100.101.102.103")})
	got := reachable("", listen, "http")
	want := []string{"http://100.101.102.103:7340", "http://192.168.1.110:7340"}
	if !slices.Equal(got, want) {
		t.Fatalf("reachable = %v, want %v", got, want)
	}
	if got := reachable("https://me.example", []string{"127.0.0.1:7340"}, "http"); !slices.Equal(got, []string{"https://me.example"}) {
		t.Fatalf("with a server URL = %v", got)
	}
	if got := reachable("", []string{"127.0.0.1:7340"}, "http"); len(got) != 0 {
		t.Fatalf("loopback only = %v, want none", got)
	}
}

func TestThePairingBlockOffersEveryAddressAndTheCode(t *testing.T) {
	out := pairingBlock("ABCDE-FGHJK", time.Date(2026, 11, 13, 9, 30, 0, 0, time.Local), []string{"http://100.101.102.103:7340"}, "http://127.0.0.1:7340")
	for _, want := range []string{"ABCDE-FGHJK", "http://100.101.102.103:7340", "http://127.0.0.1:7340   (on this machine)", "09:30", "█"} {
		if !strings.Contains(out, want) {
			t.Fatalf("pairing block lacks %q:\n%s", want, out)
		}
	}
}

func TestOneServerPerDataFolder(t *testing.T) {
	dir := t.TempDir()
	if dataInUse(dir) {
		t.Fatal("an unused folder reads as in use")
	}
	if err := lockData(dir); err != nil {
		t.Fatal(err)
	}
	// A collected file would release the lock.
	runtime.GC()
	runtime.GC()
	if !dataInUse(dir) {
		t.Fatal("a locked folder reads as free")
	}
}

func TestSignInsCanAlwaysComeBackToThisMachine(t *testing.T) {
	if got := withLoopback([]string{"192.168.1.110:7340"}); !slices.Equal(got, []string{"192.168.1.110:7340", "127.0.0.1:7340"}) {
		t.Fatalf("network address only: %v", got)
	}
	for _, already := range [][]string{{"127.0.0.1:7340"}, {"192.168.1.110:7340", "127.0.0.1:7340"}, {"localhost:7340"}} {
		if got := withLoopback(already); !slices.Equal(got, already) {
			t.Fatalf("%v became %v", already, got)
		}
	}
}
