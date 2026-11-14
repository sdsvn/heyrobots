// Package googletest is a fake of the Google endpoints heyrobots uses:
// OAuth, Gmail and Calendar. Tests drive it directly.
package googletest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"heyrobots/service/internal/google"
)

// Message is a stored mail message.
type Message struct {
	ID, ThreadID string
	Labels       []string
	Raw          []byte
	At           time.Time
	History      int
}

// Fake is the fake Google.
type Fake struct {
	Server *httptest.Server
	Email  string
	// Scopes are granted on the next code exchange.
	Scopes string
	// NoRefresh makes the exchange omit the refresh token.
	NoRefresh bool

	mu       sync.Mutex
	codes    map[string]string // code -> PKCE challenge
	history  int
	messages []*Message
	Sent     [][]byte
	SentIn   []string
	Drafts   [][]byte
	Events   map[string]*google.Event
	Patches  []string
	Revoked  []string
	Inserted int
}

// New starts a fake Google.
func New(t *testing.T) *Fake {
	f := &Fake{Email: "ann@gmail.com", codes: map[string]string{}, history: 100, Events: map[string]*google.Event{},
		Scopes: strings.Join(append(append([]string{}, google.ReadScopes...), google.ActScopes...), " ")}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Server.Close)
	return f
}

// Endpoints points the client at the fake.
func (f *Fake) Endpoints() google.Endpoints {
	u := f.Server.URL
	return google.Endpoints{AuthURL: u + "/auth", TokenURL: u + "/token", RevokeURL: u + "/revoke",
		UserInfoURL: u + "/userinfo", GmailBase: u + "/gmail/v1/users/me", CalendarBase: u + "/calendar/v3"}
}

// Authorize plays the browser: it follows an auth URL and returns the
// redirect Google would send, with a code.
func (f *Fake) Authorize(authURL string) string {
	u, _ := url.Parse(authURL)
	q := u.Query()
	code := "code-" + strconv.Itoa(len(f.codes)+1)
	f.mu.Lock()
	f.codes[code] = q.Get("code_challenge")
	f.mu.Unlock()
	return q.Get("redirect_uri") + "?state=" + url.QueryEscape(q.Get("state")) + "&code=" + code
}

// Deliver adds a message to the mailbox.
func (f *Fake) Deliver(id, thread string, labels []string, raw []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.history++
	f.messages = append(f.messages, &Message{ID: id, ThreadID: thread, Labels: labels, Raw: raw, At: time.Now().UTC(), History: f.history})
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	w.Header().Set("Content-Type", "application/json")
	switch {
	case p == "/token":
		r.ParseForm()
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if _, ok := f.codes[r.Form.Get("code")]; !ok || r.Form.Get("code_verifier") == "" {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			delete(f.codes, r.Form.Get("code"))
			tok := map[string]any{"access_token": "at-1", "token_type": "Bearer", "expires_in": 3600, "scope": f.Scopes}
			if !f.NoRefresh {
				tok["refresh_token"] = "rt-1"
			}
			json.NewEncoder(w).Encode(tok)
		case "refresh_token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at-2", "token_type": "Bearer", "expires_in": 3600})
		}
	case p == "/userinfo":
		json.NewEncoder(w).Encode(map[string]any{"email": f.Email, "email_verified": true})
	case p == "/revoke":
		r.ParseForm()
		f.Revoked = append(f.Revoked, r.Form.Get("token"))
	case strings.HasPrefix(p, "/gmail/v1/users/me"):
		f.gmail(w, r, strings.TrimPrefix(p, "/gmail/v1/users/me"))
	case strings.HasPrefix(p, "/calendar/v3/calendars/primary/events"):
		f.calendar(w, r, strings.TrimPrefix(strings.TrimPrefix(p, "/calendar/v3/calendars/primary/events"), "/"))
	default:
		http.NotFound(w, r)
	}
}

func (f *Fake) gmail(w http.ResponseWriter, r *http.Request, p string) {
	enc := json.NewEncoder(w)
	switch {
	case p == "/profile":
		enc.Encode(map[string]any{"emailAddress": f.Email, "historyId": strconv.Itoa(f.history)})
	case p == "/history":
		since, _ := strconv.Atoi(r.URL.Query().Get("startHistoryId"))
		if since < 50 {
			w.WriteHeader(404)
			enc.Encode(map[string]any{"error": map[string]any{"message": "history too old"}})
			return
		}
		var hist []map[string]any
		for _, m := range f.messages {
			if m.History > since {
				hist = append(hist, map[string]any{"messagesAdded": []map[string]any{{"message": map[string]any{"id": m.ID}}}})
			}
		}
		enc.Encode(map[string]any{"history": hist, "historyId": strconv.Itoa(f.history)})
	case strings.HasPrefix(p, "/messages/") && r.Method == http.MethodGet:
		id := strings.TrimPrefix(p, "/messages/")
		for _, m := range f.messages {
			if m.ID == id {
				enc.Encode(map[string]any{"id": m.ID, "threadId": m.ThreadID, "labelIds": m.Labels,
					"raw": base64.URLEncoding.EncodeToString(m.Raw), "internalDate": strconv.FormatInt(m.At.UnixMilli(), 10)})
				return
			}
		}
		w.WriteHeader(404)
		enc.Encode(map[string]any{"error": map[string]any{"message": "not found"}})
	case p == "/messages" && r.Method == http.MethodGet:
		var out []map[string]any
		for i := len(f.messages) - 1; i >= 0; i-- {
			out = append(out, map[string]any{"id": f.messages[i].ID})
		}
		enc.Encode(map[string]any{"messages": out})
	case p == "/messages/send":
		var b struct{ Raw, ThreadID string }
		json.NewDecoder(r.Body).Decode(&b)
		raw, _ := base64.URLEncoding.DecodeString(b.Raw)
		f.Sent = append(f.Sent, raw)
		f.SentIn = append(f.SentIn, b.ThreadID)
		id := fmt.Sprintf("sent-%d", len(f.Sent))
		if b.ThreadID == "" {
			// A new conversation, as Gmail starts one.
			b.ThreadID = "t-" + id
		}
		f.history++
		f.messages = append(f.messages, &Message{ID: id, ThreadID: b.ThreadID, Labels: []string{"SENT"}, Raw: raw, At: time.Now().UTC(), History: f.history})
		enc.Encode(map[string]any{"id": id})
	case p == "/drafts":
		var b struct {
			Message struct{ Raw string } `json:"message"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		raw, _ := base64.URLEncoding.DecodeString(b.Message.Raw)
		f.Drafts = append(f.Drafts, raw)
		enc.Encode(map[string]any{"id": fmt.Sprintf("draft-%d", len(f.Drafts))})
	case strings.HasPrefix(p, "/threads/"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/threads/"), "?format=minimal")
		var out []map[string]any
		for _, m := range f.messages {
			if m.ThreadID == id {
				out = append(out, map[string]any{"id": m.ID})
			}
		}
		enc.Encode(map[string]any{"messages": out})
	default:
		http.NotFound(w, r)
	}
}

func (f *Fake) calendar(w http.ResponseWriter, r *http.Request, id string) {
	enc := json.NewEncoder(w)
	switch {
	case id == "" && r.Method == http.MethodGet:
		q := r.URL.Query()
		var items []*google.Event
		if q.Get("syncToken") != "" || q.Get("timeMax") != "" {
			for _, e := range f.Events {
				items = append(items, e)
			}
		}
		enc.Encode(map[string]any{"items": items, "nextSyncToken": "sync-" + strconv.Itoa(len(f.Events))})
	case id == "" && r.Method == http.MethodPost:
		var e google.Event
		json.NewDecoder(r.Body).Decode(&e)
		if _, ok := f.Events[e.ID]; ok {
			w.WriteHeader(409)
			enc.Encode(map[string]any{"error": map[string]any{"message": "duplicate"}})
			return
		}
		f.Inserted++
		f.Events[e.ID] = &e
		enc.Encode(e)
	case r.Method == http.MethodGet:
		e, ok := f.Events[id]
		if !ok {
			w.WriteHeader(404)
			enc.Encode(map[string]any{"error": map[string]any{"message": "not found"}})
			return
		}
		enc.Encode(e)
	case r.Method == http.MethodPatch:
		var b struct {
			Attendees []google.Attendee `json:"attendees"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		if e, ok := f.Events[id]; ok {
			e.Attendees = b.Attendees
		}
		f.Patches = append(f.Patches, id)
		enc.Encode(f.Events[id])
	default:
		http.NotFound(w, r)
	}
}
