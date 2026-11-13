package main

import (
	"net"
	"net/netip"
	"slices"
)

// tailnet holds Tailscale's addresses: 100.64.0.0/10 and fd7a:115c:a1e0::/48.
var tailnet = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")}

// tailnetIPs returns this machine's addresses on a Tailscale network. Only
// the owner's own devices can reach them, through WireGuard.
func tailnetIPs() []netip.Addr {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		ip := p.Addr().Unmap()
		if slices.ContainsFunc(tailnet, func(t netip.Prefix) bool { return t.Contains(ip) }) {
			out = append(out, ip)
		}
	}
	return out
}

// withTailnet adds a listen address on each tailnet IP, on the port of the
// first listen address, unless one is there already.
func withTailnet(listen []string, ips []netip.Addr) []string {
	if len(listen) == 0 {
		return listen
	}
	_, port, err := net.SplitHostPort(listen[0])
	if err != nil {
		return listen
	}
	out := slices.Clone(listen)
	for _, ip := range ips {
		a := net.JoinHostPort(ip.String(), port)
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

// reachable lists the addresses another device can pair with, best first:
// the configured public one, then tailnet addresses, then other network
// addresses the server listens on. This machine's own loopback is left out.
func reachable(serverURL string, listen []string, scheme string) []string {
	var out []string
	add := func(u string) {
		if u != "" && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	add(serverURL)
	var lan []string
	for _, a := range listen {
		host, _, err := net.SplitHostPort(a)
		if err != nil {
			continue
		}
		ip, err := netip.ParseAddr(host)
		if err == nil && (ip.IsLoopback() || ip.IsUnspecified()) || host == "localhost" {
			continue
		}
		u := scheme + "://" + a
		if err == nil && slices.ContainsFunc(tailnet, func(t netip.Prefix) bool { return t.Contains(ip) }) {
			add(u)
		} else {
			lan = append(lan, u)
		}
	}
	for _, u := range lan {
		add(u)
	}
	return out
}

// localAddr is a listen address as this machine's browser reaches it.
func localAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if ip, err := netip.ParseAddr(host); err == nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// withLoopback adds 127.0.0.1 on the first address's port unless the server
// already listens on this machine's own address. Sign-ins to Google and
// other apps come back to http://127.0.0.1:<port>, so without it a server
// listening only on a network address could never finish one.
func withLoopback(listen []string) []string {
	if len(listen) == 0 {
		return listen
	}
	_, port, err := net.SplitHostPort(listen[0])
	if err != nil {
		return listen
	}
	for _, a := range listen {
		host, p, err := net.SplitHostPort(a)
		if err != nil || p != port {
			continue
		}
		if ip, err := netip.ParseAddr(host); host == "localhost" || err == nil && (ip.IsLoopback() || ip.IsUnspecified()) {
			return listen
		}
	}
	return append(slices.Clone(listen), net.JoinHostPort("127.0.0.1", port))
}
