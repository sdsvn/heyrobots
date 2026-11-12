// Package netguard limits where the agent's network traffic may go. It
// refuses addresses on the owner's own machine and network, such as
// loopback, private ranges, link-local and cloud metadata, the Tailscale
// range and unique-local IPv6, so content the agent reads can't steer it
// at the owner's router, NAS or the heyrobots server itself.
//
// The dialer checks the address it actually connects to, after DNS, so a
// name can't resolve to a public address for the check and a private one
// for the connection.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// Policy says where traffic for one toolset may go.
type Policy struct {
	// Public allows any public internet host.
	Public bool
	// Hosts allows these host names, and their subdomains, when Public is
	// false.
	Hosts []string
	// AllowPrefixes lets tests reach otherwise refused ranges. It is never
	// set in production.
	AllowPrefixes []netip.Prefix
}

// None allows no traffic at all.
var None = Policy{}

// PublicWeb allows the public internet only.
var PublicWeb = Policy{Public: true}

// ErrRefused is returned for traffic the Policy does not allow.
var ErrRefused = errors.New("network destination refused")

var blocked = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "::ffff:0:0/96", "64:ff9b::/96", "100::/64", "2001:db8::/32",
		"fc00::/7", "fe80::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// AddrAllowed reports whether an IP address may be reached.
func (p Policy) AddrAllowed(a netip.Addr) bool {
	a = a.Unmap()
	for _, pre := range p.AllowPrefixes {
		if pre.Contains(a) {
			return true
		}
	}
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() {
		return false
	}
	for _, pre := range blocked {
		if pre.Contains(a) {
			return false
		}
	}
	return true
}

// HostAllowed reports whether a host name may be reached at all.
func (p Policy) HostAllowed(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return len(p.AllowPrefixes) > 0 && host != ""
	}
	if p.Public {
		return true
	}
	return slices.ContainsFunc(p.Hosts, func(h string) bool {
		h = strings.ToLower(h)
		return host == h || strings.HasSuffix(host, "."+h)
	})
}

// Dialer dials only allowed destinations.
type Dialer struct {
	Policy   Policy
	Resolver *net.Resolver
	Timeout  time.Duration
}

// DialContext resolves the host, checks every address, and connects to an
// allowed one.
func (d Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if !d.Policy.HostAllowed(host) {
		if ip, perr := netip.ParseAddr(host); perr != nil || !d.Policy.AddrAllowed(ip) {
			return nil, fmt.Errorf("%w: %s", ErrRefused, host)
		}
	}
	var addrs []netip.Addr
	if ip, perr := netip.ParseAddr(host); perr == nil {
		addrs = []netip.Addr{ip}
	} else {
		r := d.Resolver
		if r == nil {
			r = net.DefaultResolver
		}
		addrs, err = r.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	timeout := d.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	nd := net.Dialer{Timeout: timeout}
	var lastErr error = fmt.Errorf("%w: %s has no allowed address", ErrRefused, host)
	for _, a := range addrs {
		if !d.Policy.AddrAllowed(a) {
			continue
		}
		c, err := nd.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// Client returns an HTTP client whose every connection, including each
// redirect, goes through the Policy. It ignores proxy environment settings.
func Client(p Policy, timeout time.Duration) *http.Client {
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           Dialer{Policy: p}.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("%w: scheme %s", ErrRefused, req.URL.Scheme)
			}
			return nil
		},
	}
}
