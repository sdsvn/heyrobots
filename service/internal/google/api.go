package google

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ErrCursorExpired means Gmail no longer has history that old, or Calendar
// dropped the sync token. Polling starts again from now.
var ErrCursorExpired = errors.New("the saved position is too old")

// Gmail calls the Gmail API as the connected account.
type Gmail struct {
	c    *http.Client
	base string
}

// Gmail returns the Gmail API for the connected account.
func (g *Google) Gmail(ctx context.Context) (*Gmail, error) {
	c, err := g.HTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	return &Gmail{c: c, base: g.Ends.GmailBase}, nil
}

// HistoryID returns the mailbox's current position.
func (m *Gmail) HistoryID(ctx context.Context) (string, error) {
	var p struct {
		HistoryID string `json:"historyId"`
	}
	err := getJSON(ctx, m.c, m.base+"/profile", &p)
	return p.HistoryID, err
}

// Added returns the ids of messages added since a position, oldest first,
// and the new position.
func (m *Gmail) Added(ctx context.Context, since string) ([]string, string, error) {
	var ids []string
	seen := map[string]bool{}
	page, last := "", since
	for {
		q := url.Values{"startHistoryId": {since}, "historyTypes": {"messageAdded"}, "maxResults": {"500"}}
		if page != "" {
			q.Set("pageToken", page)
		}
		var r struct {
			History []struct {
				MessagesAdded []struct {
					Message struct {
						ID string `json:"id"`
					} `json:"message"`
				} `json:"messagesAdded"`
			} `json:"history"`
			HistoryID     string `json:"historyId"`
			NextPageToken string `json:"nextPageToken"`
		}
		err := getJSON(ctx, m.c, m.base+"/history?"+q.Encode(), &r)
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			return nil, "", ErrCursorExpired
		}
		if err != nil {
			return nil, "", err
		}
		for _, h := range r.History {
			for _, a := range h.MessagesAdded {
				if !seen[a.Message.ID] {
					seen[a.Message.ID] = true
					ids = append(ids, a.Message.ID)
				}
			}
		}
		if r.HistoryID != "" {
			last = r.HistoryID
		}
		if r.NextPageToken == "" {
			return ids, last, nil
		}
		page = r.NextPageToken
	}
}

// Message is a message in its raw RFC 5322 form, so the server can check
// its DKIM signatures itself.
type Message struct {
	ID       string
	ThreadID string
	Labels   []string
	Raw      []byte
	At       time.Time
}

// Raw fetches one message.
func (m *Gmail) Raw(ctx context.Context, id string) (Message, error) {
	var r struct {
		ID           string   `json:"id"`
		ThreadID     string   `json:"threadId"`
		LabelIDs     []string `json:"labelIds"`
		Raw          string   `json:"raw"`
		InternalDate string   `json:"internalDate"`
	}
	if err := getJSON(ctx, m.c, m.base+"/messages/"+url.PathEscape(id)+"?format=raw", &r); err != nil {
		return Message{}, err
	}
	raw, err := base64.URLEncoding.DecodeString(r.Raw)
	if err != nil {
		raw, err = base64.RawURLEncoding.DecodeString(r.Raw)
	}
	if err != nil {
		return Message{}, fmt.Errorf("decode message %s: %w", id, err)
	}
	ms, _ := strconv.ParseInt(r.InternalDate, 10, 64)
	return Message{ID: r.ID, ThreadID: r.ThreadID, Labels: r.LabelIDs, Raw: raw, At: time.UnixMilli(ms).UTC()}, nil
}

// Search returns ids of messages matching a Gmail search, newest first.
func (m *Gmail) Search(ctx context.Context, query string, max int) ([]string, error) {
	q := url.Values{"q": {query}, "maxResults": {strconv.Itoa(max)}}
	var r struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := getJSON(ctx, m.c, m.base+"/messages?"+q.Encode(), &r); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(r.Messages))
	for _, x := range r.Messages {
		ids = append(ids, x.ID)
	}
	return ids, nil
}

// ThreadMessages returns the ids of a thread's messages, oldest first.
func (m *Gmail) ThreadMessages(ctx context.Context, threadID string) ([]string, error) {
	var r struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := getJSON(ctx, m.c, m.base+"/threads/"+url.PathEscape(threadID)+"?format=minimal", &r); err != nil {
		return nil, err
	}
	var ids []string
	for _, x := range r.Messages {
		ids = append(ids, x.ID)
	}
	return ids, nil
}

// Send sends a raw message, in a thread when threadID is set, and returns
// the new message's id.
func (m *Gmail) Send(ctx context.Context, raw []byte, threadID string) (string, error) {
	body := map[string]string{"raw": base64.URLEncoding.EncodeToString(raw)}
	if threadID != "" {
		body["threadId"] = threadID
	}
	var r struct {
		ID string `json:"id"`
	}
	err := doJSON(ctx, m.c, http.MethodPost, m.base+"/messages/send", body, &r)
	return r.ID, err
}

// Draft saves a raw message as a draft and returns the draft's id.
func (m *Gmail) Draft(ctx context.Context, raw []byte, threadID string) (string, error) {
	msg := map[string]string{"raw": base64.URLEncoding.EncodeToString(raw)}
	if threadID != "" {
		msg["threadId"] = threadID
	}
	var r struct {
		ID string `json:"id"`
	}
	err := doJSON(ctx, m.c, http.MethodPost, m.base+"/drafts", map[string]any{"message": msg}, &r)
	return r.ID, err
}

// Calendar calls the Calendar API on the account's primary calendar.
type Calendar struct {
	c    *http.Client
	base string
}

// Calendar returns the Calendar API for the connected account.
func (g *Google) Calendar(ctx context.Context) (*Calendar, error) {
	c, err := g.HTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	return &Calendar{c: c, base: g.Ends.CalendarBase + "/calendars/primary/events"}, nil
}

// Event is a calendar event, as the Calendar API describes it.
type Event struct {
	ID          string     `json:"id,omitempty"`
	Status      string     `json:"status,omitempty"`
	Summary     string     `json:"summary,omitempty"`
	Description string     `json:"description,omitempty"`
	Location    string     `json:"location,omitempty"`
	Start       EventTime  `json:"start"`
	End         EventTime  `json:"end"`
	Organizer   *Person    `json:"organizer,omitempty"`
	Attendees   []Attendee `json:"attendees,omitempty"`
	HTMLLink    string     `json:"htmlLink,omitempty"`
}

// EventTime is a timed or an all-day moment.
type EventTime struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

// Time returns when it is; all-day dates start at midnight UTC.
func (t EventTime) Time() time.Time {
	if t.DateTime != "" {
		v, _ := time.Parse(time.RFC3339, t.DateTime)
		return v
	}
	v, _ := time.Parse("2006-01-02", t.Date)
	return v
}

// Person is an organizer.
type Person struct {
	Email string `json:"email"`
	Self  bool   `json:"self,omitempty"`
}

// Attendee is someone invited.
type Attendee struct {
	Email          string `json:"email"`
	Self           bool   `json:"self,omitempty"`
	Organizer      bool   `json:"organizer,omitempty"`
	ResponseStatus string `json:"responseStatus,omitempty"`
	Optional       bool   `json:"optional,omitempty"`
}

// Between lists events that overlap a time range, earliest first.
func (c *Calendar) Between(ctx context.Context, from, to time.Time) ([]Event, error) {
	q := url.Values{"timeMin": {from.Format(time.RFC3339)}, "timeMax": {to.Format(time.RFC3339)},
		"singleEvents": {"true"}, "orderBy": {"startTime"}, "maxResults": {"250"}}
	var r struct {
		Items []Event `json:"items"`
	}
	err := getJSON(ctx, c.c, c.base+"?"+q.Encode(), &r)
	return r.Items, err
}

// Changes lists events changed since a sync token, and the next token.
// With an empty token it starts from now and returns no events.
func (c *Calendar) Changes(ctx context.Context, token string) ([]Event, string, error) {
	var out []Event
	page := ""
	for {
		q := url.Values{"singleEvents": {"true"}, "maxResults": {"250"}}
		if token == "" {
			q.Set("timeMin", time.Now().UTC().Format(time.RFC3339))
		} else {
			q.Set("syncToken", token)
		}
		if page != "" {
			q.Set("pageToken", page)
		}
		var r struct {
			Items         []Event `json:"items"`
			NextPageToken string  `json:"nextPageToken"`
			NextSyncToken string  `json:"nextSyncToken"`
		}
		err := getJSON(ctx, c.c, c.base+"?"+q.Encode(), &r)
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusGone {
			return nil, "", ErrCursorExpired
		}
		if err != nil {
			return nil, "", err
		}
		if token != "" {
			out = append(out, r.Items...)
		}
		if r.NextPageToken == "" {
			return out, r.NextSyncToken, nil
		}
		page = r.NextPageToken
	}
}

// Get returns one event.
func (c *Calendar) Get(ctx context.Context, id string) (Event, error) {
	var e Event
	err := getJSON(ctx, c.c, c.base+"/"+url.PathEscape(id), &e)
	return e, err
}

// Insert creates an event and sends invitations. The caller picks the id,
// so trying again after a lost answer can't create a second event.
func (c *Calendar) Insert(ctx context.Context, e Event) (Event, error) {
	var out Event
	err := doJSON(ctx, c.c, http.MethodPost, c.base+"?sendUpdates=all", e, &out)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusConflict {
		// Already created by an earlier try.
		return c.Get(ctx, e.ID)
	}
	return out, err
}

// Respond answers an invitation as the account's own attendee.
func (c *Calendar) Respond(ctx context.Context, id, response string) error {
	e, err := c.Get(ctx, id)
	if err != nil {
		return err
	}
	found := false
	for i := range e.Attendees {
		if e.Attendees[i].Self {
			e.Attendees[i].ResponseStatus = response
			found = true
		}
	}
	if !found {
		return errors.New("you aren't invited to that event")
	}
	return doJSON(ctx, c.c, http.MethodPatch, c.base+"/"+url.PathEscape(id)+"?sendUpdates=all",
		map[string]any{"attendees": e.Attendees}, nil)
}
