package netguard

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
)

// Proxy is a forward HTTP proxy for sandboxed commands. Containers can
// reach nothing but this proxy, and it applies the toolset's Policy to
// every request and logs each one.
type Proxy struct {
	Policy Policy
	Log    *slog.Logger
	// Label names the Assignment in the log.
	Label string
}

func (p *Proxy) dialer() Dialer { return Dialer{Policy: p.Policy} }

// ServeHTTP handles CONNECT tunnels for HTTPS and absolute-URL requests
// for plain HTTP.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if r.Method != http.MethodConnect {
		host = r.URL.Host
	}
	if r.Method != http.MethodConnect && (r.URL.Scheme != "http" || r.URL.Host == "") {
		p.Log.Warn("proxy refused", "assignment", p.Label, "reason", "not an absolute http URL")
		http.Error(w, "proxy accepts CONNECT or absolute http URLs only", http.StatusBadRequest)
		return
	}
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	conn, err := p.dialer().DialContext(r.Context(), "tcp", host)
	if err != nil {
		p.Log.Warn("proxy refused", "assignment", p.Label, "host", host, "err", err)
		http.Error(w, "destination refused", http.StatusForbidden)
		return
	}
	p.Log.Info("proxy request", "assignment", p.Label, "method", r.Method, "host", host)
	if r.Method == http.MethodConnect {
		p.tunnel(w, conn)
		return
	}
	defer conn.Close()
	r.RequestURI = ""
	r.Header.Del("Proxy-Connection")
	r.Header.Del("Proxy-Authorization")
	if err := r.Write(conn); err != nil {
		http.Error(w, "upstream write failed", http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	buf.Flush()
	io.Copy(client, conn)
}

func (p *Proxy) tunnel(w http.ResponseWriter, upstream net.Conn) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	var wg sync.WaitGroup
	wg.Add(2)
	pipe := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
		if tc, ok := dst.(interface{ CloseWrite() error }); ok {
			tc.CloseWrite()
		}
	}
	go pipe(upstream, client)
	go pipe(client, upstream)
	go func() {
		wg.Wait()
		client.Close()
		upstream.Close()
	}()
}
