// Package api serves the companion app's HTTP API and the first-run setup
// page.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"heyrobots/service/internal/cluster"
	"heyrobots/service/internal/registry"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"heyrobots/service/internal/acp"
	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/devices"
	"heyrobots/service/internal/gateway"
	"heyrobots/service/internal/google"
	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/jobs"
	"heyrobots/service/internal/models"
	"heyrobots/service/internal/skills"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/vault"
)

// Options configures the Server.
type Options struct {
	Store   *store.Store
	Devices *devices.Service
	Vault   *vault.Vault
	// Web serves the companion app's web build at /, if there is one.
	Web http.Handler
	// Models gives each role its model. Without it, only Gemini is set up.
	Models *models.Resolver
	// ACP runs tasks in the owner's own Claude Code. Nil means it isn't
	// set up.
	ACP *acp.Worker
	// CheckModel replaces the test calls made before a model is saved, for
	// tests.
	CheckModel func(ctx context.Context, c models.Choice, key string) error
	// Skills is the owner's skills library.
	Skills *skills.Library
	// Jobs is the standing jobs folder.
	Jobs *jobs.Library
	// MCP reads the owner's MCP servers' tools.
	MCP *gateway.MCP
	// Files is where shared files are kept.
	Files string
	// Registry finds MCP servers in the official MCP registry.
	Registry *registry.Client
	Broker   *Broker
	Log      *slog.Logger
	Now      func() time.Time
	// ServerURL is put into pairing codes. When empty, the address of the
	// request is used.
	ServerURL string
	// Addresses are where other devices can reach this server, best first.
	// Pairing codes carry them all; empty uses the request's own address.
	Addresses []string
	// Guard, when set, wraps every route: public mode.
	Guard func(http.Handler) http.Handler
	// ShowSetupCode prints the setup code to the terminal and log.
	ShowSetupCode func(code string)
	// ShowPairingCode prints, at every start, a one-time code that pairs an
	// owner device, with the addresses other devices can reach.
	ShowPairingCode func(code string, expires time.Time, servers []string)
	// Heartbeat is how often an idle event stream sends a keep-alive.
	Heartbeat time.Duration
	// Intake stores chat messages.
	Intake *intake.Intake
	// OnMessage is called after a chat message is stored, to wake Eval.
	OnMessage func()
	// OnDecided is called after the owner approves or rejects an action or
	// changes standing approvals, to wake the outbox and Executors.
	OnDecided func()
	// Usage reads model spending.
	Usage UsageReader
	// Pusher sends push notifications.
	Pusher Pusher
	// Google connects the owner's Google account, and GoogleRedirect is
	// the loopback address Google sends the browser back to.
	Google         *google.Google
	GoogleRedirect string
	// MCPRedirect is the loopback address an MCP server's sign-in sends
	// the browser back to. Empty asks for a pasted token instead.
	MCPRedirect string
	// OnAccounts is called after an account is connected.
	OnAccounts func()
	// Cluster shares the team with the owner's other machines. Nil runs
	// this machine alone.
	Cluster *cluster.Node
	// Probe holds the addresses the catalogue looks things up at. Empty
	// fields use the real ones; tests point them at fakes.
	Probe Probe
}

// Server implements the generated API interface.
type Server struct {
	opts    Options
	setup   *setupState
	signIns mcpSignIns
	links   fileLinks
}

var _ gen.ServerInterface = (*Server)(nil)

// New returns a Server.
func New(o Options) *Server {
	if o.Heartbeat == 0 {
		o.Heartbeat = 25 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	o.Probe = o.Probe.withDefaults()
	return &Server{opts: o, setup: &setupState{show: o.ShowSetupCode, now: o.Now}}
}

// SetOnDecided sets the callback for approvals and standing approval
// changes, for wiring that needs the Server first.
func (s *Server) SetOnDecided(fn func()) { s.opts.OnDecided = fn }

// AnnounceSetup runs at start. It prints the setup code while setup is
// open, and on every start a one-time owner pairing code: whoever can read
// the server's terminal may pair a device, so a lost phone or a cleared
// browser never locks the owner out.
func (s *Server) AnnounceSetup() error {
	open, err := s.opts.Devices.SetupOpen()
	if err != nil {
		return err
	}
	if open {
		if _, err := s.setup.code(); err != nil {
			return err
		}
	}
	if s.opts.ShowPairingCode == nil {
		return nil
	}
	c, err := s.opts.Devices.NewCode(devices.RoleOwner, "", "terminal")
	if err != nil {
		return err
	}
	s.opts.ShowPairingCode(c.Code, c.ExpiresAt, append([]string{}, s.opts.Addresses...))
	return nil
}

// maxBody caps request bodies.
const maxBody = 64 << 10

// Handler returns the server's HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /setup", s.setupPage)
	mux.HandleFunc("POST /setup", s.setupSubmit)
	mux.HandleFunc("GET /oauth/google/callback", s.GoogleCallback)
	mux.HandleFunc("GET /oauth/mcp/callback", s.MCPCallback)
	mux.HandleFunc("GET /files/{key}", s.DownloadFile)
	gen.HandlerWithOptions(s, gen.StdHTTPServerOptions{
		BaseRouter:  mux,
		Middlewares: []gen.MiddlewareFunc{s.authenticate},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, http.StatusBadRequest, err.Error())
		},
	})
	if s.opts.Web != nil {
		// The companion app's web build, from the same address as the API.
		mux.Handle("GET /", s.opts.Web)
	}
	var h http.Handler = mux
	h = limitBody(h)
	if s.opts.Guard != nil {
		h = s.opts.Guard(h)
	}
	return securityHeaders(h)
}

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

type ctxKey struct{}

// publicPaths need no device token. In public mode the guard still applies.
var publicPaths = map[string]bool{
	"/api/v1/health": true,
	"/api/v1/pair":   true,
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeError(w, http.StatusUnauthorized, "device token required")
			return
		}
		d, err := s.opts.Devices.Authenticate(strings.TrimSpace(token))
		if err != nil {
			if !errors.Is(err, devices.ErrInvalidToken) {
				s.opts.Log.Error("authenticate device", "err", err)
			}
			writeError(w, http.StatusUnauthorized, "device token is invalid or revoked")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, d)))
	})
}

func deviceFrom(r *http.Request) store.Device {
	d, _ := r.Context().Value(ctxKey{}).(store.Device)
	return d
}

func requireOwner(w http.ResponseWriter, r *http.Request) (store.Device, bool) {
	d := deviceFrom(r)
	if d.Role != devices.RoleOwner {
		writeError(w, http.StatusForbidden, "only the owner's devices can do this")
		return d, false
	}
	return d, true
}

// GetHealth reports that the server is running.
func (s *Server) GetHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, gen.Health{Status: "ok"})
}

// PairDevice exchanges a pairing code for a device token.
func (s *Server) PairDevice(w http.ResponseWriter, r *http.Request) {
	var req gen.PairRequest
	if !decode(w, r, &req) {
		return
	}
	platform := ""
	if req.Platform != nil {
		platform = *req.Platform
	}
	d, token, err := s.opts.Devices.Pair(req.Code, req.DeviceName, platform)
	switch {
	case errors.Is(err, devices.ErrBadRequest):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, devices.ErrInvalidCode):
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	case errors.Is(err, devices.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	case err != nil:
		s.internal(w, "pair device", err)
		return
	}
	s.opts.Log.Info("device paired", "device", d.ID, "role", d.Role)
	if d.Role == devices.RoleOwner {
		// The terminal code is spent once an owner device exists. If setup
		// ever reopens, it gets a new one.
		s.setup.reset()
	}
	dev := toAPI(d)
	s.opts.Broker.Publish(Event{Type: "device.paired", Data: dev, About: d.ID})
	writeJSON(w, http.StatusOK, gen.PairResponse{Device: dev, Token: token})
}

// GetMe returns the calling device.
func (s *Server) GetMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toAPI(deviceFrom(r)))
}

// ListDevices lists every device. Owner devices only.
func (s *Server) ListDevices(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	ds, err := s.opts.Store.ListDevices()
	if err != nil {
		s.internal(w, "list devices", err)
		return
	}
	out := gen.DeviceList{Devices: make([]gen.Device, 0, len(ds))}
	for _, d := range ds {
		out.Devices = append(out.Devices, toAPI(d))
	}
	writeJSON(w, http.StatusOK, out)
}

// RevokeDevice revokes a device. Owner devices only.
func (s *Server) RevokeDevice(w http.ResponseWriter, r *http.Request, deviceID string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	err := s.opts.Store.RevokeDevice(deviceID, s.opts.Now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no active device with that id")
		return
	}
	if err != nil {
		s.internal(w, "revoke device", err)
		return
	}
	s.opts.Log.Info("device revoked", "device", deviceID, "by", deviceFrom(r).ID)
	if err := s.AnnounceSetup(); err != nil {
		s.opts.Log.Error("reopen setup", "err", err)
	}
	s.opts.Broker.Publish(Event{Type: "device.revoked", Data: map[string]string{"id": deviceID}, About: deviceID})
	w.WriteHeader(http.StatusNoContent)
}

// CreatePairingCode makes a code for another owner device or a delegate.
// Owner devices only.
func (s *Server) CreatePairingCode(w http.ResponseWriter, r *http.Request) {
	d, ok := requireOwner(w, r)
	if !ok {
		return
	}
	var req gen.PairingCodeRequest
	if !decode(w, r, &req) {
		return
	}
	name := ""
	if req.DelegateName != nil {
		name = *req.DelegateName
	}
	if req.Role == gen.Delegate {
		pol, err := s.opts.Store.Policy()
		if err != nil {
			s.internal(w, "load policy", err)
			return
		}
		if _, ok := pol.DelegateNamed(strings.TrimSpace(name)); !ok {
			writeError(w, http.StatusBadRequest, "add the delegate to the policy before inviting them")
			return
		}
	}
	c, err := s.opts.Devices.NewCode(string(req.Role), name, d.ID)
	if errors.Is(err, devices.ErrBadRequest) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		s.internal(w, "create pairing code", err)
		return
	}
	servers := s.serverURLs(r)
	out := gen.PairingCode{Code: c.Code, Role: gen.Role(c.Role), ExpiresAt: c.ExpiresAt, ServerUrl: servers[0], Servers: &servers}
	if c.DelegateName != "" {
		out.DelegateName = &c.DelegateName
	}
	writeJSON(w, http.StatusCreated, out)
}

// StreamEvents sends live updates as server-sent events.
func (s *Server) StreamEvents(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r)
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	events, cancel := s.opts.Broker.Subscribe(d.ID, appIdentity(d).String(), d.Role == devices.RoleOwner)
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, ": connected\n\n")
	flusher.Flush()
	tick := time.NewTicker(s.opts.Heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			io.WriteString(w, ": ping\n\n")
			flusher.Flush()
		case e, open := <-events:
			if !open {
				return
			}
			b, err := encodeEvent(e)
			if err != nil {
				s.opts.Log.Error("encode event", "err", err)
				continue
			}
			w.Write(b)
			flusher.Flush()
			if e.Type == "device.revoked" && e.About == d.ID {
				return
			}
		}
	}
}

func (s *Server) serverURL(r *http.Request) string {
	if s.opts.ServerURL != "" {
		return s.opts.ServerURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// serverURLs lists where a new device can reach this server, best first:
// the addresses it was given, else the one this request came in on.
func (s *Server) serverURLs(r *http.Request) []string {
	if len(s.opts.Addresses) > 0 {
		return append([]string{}, s.opts.Addresses...)
	}
	return []string{s.serverURL(r)}
}

func (s *Server) internal(w http.ResponseWriter, what string, err error) {
	s.opts.Log.Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func toAPI(d store.Device) gen.Device {
	out := gen.Device{Id: d.ID, Name: d.Name, Role: gen.Role(d.Role), CreatedAt: d.CreatedAt, LastSeenAt: d.LastSeenAt, RevokedAt: d.RevokedAt}
	if d.Platform != "" {
		out.Platform = &d.Platform
	}
	if d.DelegateName != "" {
		out.DelegateName = &d.DelegateName
	}
	return out
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, gen.Error{Error: msg})
}
