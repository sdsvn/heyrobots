// Package notify sends push notifications through ntfy.
//
// A push holds only a short title and a link that opens the companion app
// in the right place. Message text, results and other people's names never
// go in a push: ntfy servers, and the phone's lock screen, are outside the
// owner's control.
//
// The Notifier doesn't keep its own queue. It reads what waits on the owner
// from the store, pushes what hasn't had a push yet, and records the keys.
// Quiet hours and the hourly cap therefore delay pushes without losing
// them: once allowed, everything that waited goes out as one push.
package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

// Push is one notification.
type Push struct {
	Title string
	// Click is the companion app link the push opens.
	Click string
	Body  string
}

// Notifier pushes what waits on the owner.
type Notifier struct {
	Store *store.Store
	// Token returns the ntfy access token, if any.
	Token  func() string
	Client *http.Client
	Log    *slog.Logger
	Now    func() time.Time

	mu   sync.Mutex
	kick chan struct{}
}

// Kick asks the Notifier to check soon.
func (n *Notifier) Kick() {
	n.mu.Lock()
	if n.kick == nil {
		n.kick = make(chan struct{}, 1)
	}
	ch := n.kick
	n.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Run checks on each kick and every minute, so that pushes held by quiet
// hours or the hourly cap go out once allowed.
func (n *Notifier) Run(ctx context.Context) {
	n.Kick()
	n.mu.Lock()
	ch := n.kick
	n.mu.Unlock()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
		case <-tick.C:
		}
		if err := n.Check(ctx); err != nil {
			n.Log.Warn("push notification", "err", err)
		}
	}
}

// Check sends one push for everything that waits on the owner and hasn't
// had one, when settings allow it now.
func (n *Notifier) Check(ctx context.Context) error {
	p, err := n.Store.Policy()
	if err != nil {
		return err
	}
	cfg := p.Notify.Effective()
	if !cfg.Enabled || cfg.Topic == "" {
		return nil
	}
	now := n.Now()
	var items []store.AttentionItem
	var fresh []store.AttentionItem
	var digestKey string
	var pushedLastHour int64
	err = n.Store.Tx(func(tx *store.Tx) error {
		var err error
		if items, err = tx.Attention(true, ""); err != nil {
			return err
		}
		keys := make([]string, 0, len(items)+1)
		for _, it := range items {
			keys = append(keys, it.Key)
		}
		if d, err := tx.LastDigest(); err == nil {
			digestKey = "digest:" + d.ID
			keys = append(keys, digestKey)
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		done, err := tx.Pushed(keys)
		if err != nil {
			return err
		}
		for _, it := range items {
			if !done[it.Key] {
				fresh = append(fresh, it)
			}
		}
		if digestKey != "" && done[digestKey] {
			digestKey = ""
		}
		pushedLastHour, err = tx.PushesSince(now.Add(-time.Hour))
		return err
	})
	if err != nil {
		return err
	}
	if len(fresh) == 0 && digestKey == "" {
		return nil
	}
	if cfg.Quiet(now.In(p.Limits.Location()).Hour()) || pushedLastHour >= int64(cfg.PerHour) {
		return nil
	}
	push := Compose(fresh, digestKey != "")
	if err := n.Send(ctx, cfg, push); err != nil {
		return err
	}
	keys := make([]string, 0, len(fresh)+1)
	for _, it := range fresh {
		keys = append(keys, it.Key)
	}
	if digestKey != "" {
		keys = append(keys, digestKey)
	}
	return n.Store.Tx(func(tx *store.Tx) error { return tx.MarkPushed(keys, now.UTC()) })
}

// Compose writes the push for some waiting items, without their content.
func Compose(items []store.AttentionItem, digest bool) Push {
	total := len(items)
	if digest {
		total++
	}
	if total > 1 {
		return Push{Title: fmt.Sprintf("%d things wait for you", total), Click: "heyrobots://home", Body: "Open heyrobots to see them."}
	}
	if digest {
		return Push{Title: "Your daily digest is ready", Click: "heyrobots://digest", Body: "Open heyrobots to read it."}
	}
	it := items[0]
	click := "heyrobots://thread/" + url.PathEscape(it.ThreadID)
	switch it.Kind {
	case store.AttentionApproval:
		return Push{Title: "An action needs your approval", Click: "heyrobots://approval/ef_" + fmt.Sprint(it.EffectID), Body: "Open heyrobots to review it."}
	case store.AttentionQuestion:
		return Push{Title: "A task has a question for you", Click: click, Body: "Open heyrobots to answer."}
	default:
		return Push{Title: "Something couldn't be sent", Click: click, Body: "Open heyrobots to see what happened."}
	}
}

// ErrNotConfigured is returned when there is no topic to push to.
var ErrNotConfigured = errors.New("push notifications have no topic")

// Send publishes one push.
func (n *Notifier) Send(ctx context.Context, cfg policy.Notify, p Push) error {
	cfg = cfg.Effective()
	if cfg.Topic == "" {
		return ErrNotConfigured
	}
	u, err := TopicURL(cfg.ServerURL, cfg.Topic)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(p.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", p.Title)
	req.Header.Set("Click", p.Click)
	req.Header.Set("Tags", "robot")
	if n.Token != nil {
		if t := n.Token(); t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
	}
	client := n.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("reach ntfy: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ntfy answered %s", resp.Status)
	}
	return nil
}

// TopicURL checks a server address and topic and joins them.
func TopicURL(server, topic string) (string, error) {
	u, err := url.Parse(strings.TrimRight(server, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the ntfy server must be an http or https address")
	}
	if !ValidTopic(topic) {
		return "", errors.New("a topic uses only letters, digits, - and _, up to 64 characters")
	}
	return u.String() + "/" + topic, nil
}

// ValidTopic reports whether a topic name is one ntfy accepts.
func ValidTopic(t string) bool {
	if t == "" || len(t) > 64 {
		return false
	}
	for _, r := range t {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
