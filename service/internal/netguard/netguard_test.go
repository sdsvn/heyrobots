package netguard

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestOwnersNetworkIsRefused(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "10.1.2.3", "172.16.5.5", "192.168.1.1", "169.254.169.254", // cloud metadata
		"100.100.100.100", "100.64.0.7", // Tailscale
		"0.0.0.0", "224.0.0.1", "::1", "fd7a:115c:a1e0::1", "fe80::1", "::ffff:127.0.0.1",
	} {
		if PublicWeb.AddrAllowed(netip.MustParseAddr(s)) {
			t.Errorf("%s must be refused", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "142.250.72.14", "2606:4700:4700::1111"} {
		if !PublicWeb.AddrAllowed(netip.MustParseAddr(s)) {
			t.Errorf("%s must be allowed", s)
		}
	}
}

func TestHostRules(t *testing.T) {
	for _, h := range []string{"localhost", "printer.local", "metadata.google.internal", "api.localhost"} {
		if PublicWeb.HostAllowed(h) {
			t.Errorf("%s must be refused", h)
		}
	}
	only := Policy{Hosts: []string{"example.com"}}
	if !only.HostAllowed("docs.example.com") || only.HostAllowed("example.com.evil.net") || only.HostAllowed("evil.net") {
		t.Fatal("host allowlist matching is wrong")
	}
	if None.HostAllowed("example.com") {
		t.Fatal("None must allow nothing")
	}
}

func TestClientRefusesLocalServerEvenByRedirect(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "secret") }))
	defer local.Close()
	_, err := Client(PublicWeb, 0).Get(local.URL)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("direct: err = %v, want ErrRefused", err)
	}
	// A server we are allowed to reach redirects to a local address.
	loopback := netip.MustParsePrefix("127.0.0.1/32")
	redirector := httptest.NewServer(http.RedirectHandler("http://169.254.169.254/latest/meta-data/", http.StatusFound))
	defer redirector.Close()
	_, err = Client(Policy{Public: true, AllowPrefixes: []netip.Prefix{loopback}}, 0).Get(redirector.URL)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("redirect: err = %v, want ErrRefused", err)
	}
}

func TestDNSAnswerIsCheckedNotJustTheName(t *testing.T) {
	// A public-looking name that resolves to loopback must be refused.
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return nil, errors.New("no dns in tests")
	}}
	d := Dialer{Policy: PublicWeb, Resolver: r}
	if _, err := d.DialContext(context.Background(), "tcp", "127.0.0.1:80"); !errors.Is(err, ErrRefused) {
		t.Fatalf("literal loopback: %v", err)
	}
}

func TestProxyRefusesAndLogs(t *testing.T) {
	var log strings.Builder
	p := &Proxy{Policy: PublicWeb, Log: slog.New(slog.NewTextHandler(&log, nil)), Label: "as_1"}
	srv := httptest.NewServer(p)
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "CONNECT 127.0.0.1:22 HTTP/1.1\r\nHost: 127.0.0.1:22\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("CONNECT to loopback: %d, want 403", resp.StatusCode)
	}
	if !strings.Contains(log.String(), "proxy refused") || !strings.Contains(log.String(), "as_1") {
		t.Fatalf("refusal not logged: %s", log.String())
	}
}

func TestProxyForwardsAllowedTraffic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello") }))
	defer upstream.Close()
	var log strings.Builder
	p := &Proxy{Policy: Policy{Public: true, AllowPrefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}, Log: slog.New(slog.NewTextHandler(&log, nil)), Label: "as_2"}
	srv := httptest.NewServer(p)
	defer srv.Close()
	proxyURL, _ := url.Parse(srv.URL)
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := c.Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "hello" || !strings.Contains(log.String(), "proxy request") {
		t.Fatalf("body %q log %s", b, log.String())
	}
}
