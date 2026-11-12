package api

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"heyrobots/service/internal/devices"
)

//go:embed setup.html
var setupHTML string

var setupTmpl = template.Must(template.New("setup").Parse(setupHTML))

// setupState holds the terminal setup code. It is created when setup is
// first needed, and again if setup reopens because every owner device was
// revoked.
type setupState struct {
	mu   sync.Mutex
	sc   *devices.SetupCode
	show func(string)
	now  func() time.Time
}

func (st *setupState) code() (*devices.SetupCode, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.sc == nil {
		sc, err := devices.NewSetupCode(st.now, st.show)
		if err != nil {
			return nil, err
		}
		st.sc = sc
	}
	return st.sc, nil
}

func (st *setupState) reset() {
	st.mu.Lock()
	st.sc = nil
	st.mu.Unlock()
}

type setupView struct {
	Error       string
	PairingCode string
	ServerURL   string
	ExpiresAt   string
	QRDataURI   template.URL
}

// setupOpen answers 404 when setup is closed, so the page doesn't exist
// once the owner has a device.
func (s *Server) setupOpen(w http.ResponseWriter) bool {
	open, err := s.opts.Devices.SetupOpen()
	if err != nil {
		s.internal(w, "check setup", err)
		return false
	}
	if !open {
		s.setup.reset()
		http.NotFound(w, nil)
		return false
	}
	return true
}

func (s *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	if !s.setupOpen(w) {
		return
	}
	if _, err := s.setup.code(); err != nil {
		s.internal(w, "setup code", err)
		return
	}
	s.renderSetup(w, http.StatusOK, setupView{})
}

func (s *Server) setupSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.setupOpen(w) {
		return
	}
	sc, err := s.setup.code()
	if err != nil {
		s.internal(w, "setup code", err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderSetup(w, http.StatusBadRequest, setupView{Error: "The form could not be read."})
		return
	}
	var view setupView

	// The terminal code is the only proof that whoever is here can see the
	// server's terminal. The request's address proves nothing.
	switch err := sc.Check(r.PostFormValue("setup_code")); {
	case errors.Is(err, devices.ErrRateLimited):
		view.Error = "Too many wrong codes. Wait ten minutes, then use the new code printed in the terminal."
		s.renderSetup(w, http.StatusTooManyRequests, view)
		return
	case err != nil:
		s.opts.Log.Warn("wrong setup code")
		view.Error = "That setup code is wrong. Use the code printed in the terminal where heyrobotsd runs."
		s.renderSetup(w, http.StatusUnauthorized, view)
		return
	}

	c, err := s.opts.Devices.NewCode(devices.RoleOwner, "", "setup")
	if err != nil {
		s.internal(w, "create pairing code", err)
		return
	}
	servers := s.serverURLs(r)
	serverURL := servers[0]
	payload, _ := json.Marshal(map[string]any{"v": 1, "server": serverURL, "servers": servers, "code": c.Code})
	png, err := qrcode.Encode(string(payload), qrcode.Medium, 288)
	if err != nil {
		s.internal(w, "render QR code", err)
		return
	}
	view.PairingCode = c.Code
	view.ServerURL = serverURL
	view.ExpiresAt = c.ExpiresAt.Local().Format("15:04")
	view.QRDataURI = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
	s.opts.Log.Info("setup code accepted; first pairing code issued")
	s.renderSetup(w, http.StatusOK, view)
}

func (s *Server) renderSetup(w http.ResponseWriter, status int, v setupView) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	if err := setupTmpl.Execute(w, v); err != nil {
		s.opts.Log.Error("render setup page", "err", err)
	}
}
