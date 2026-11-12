package notify

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

type received struct {
	path, title, click, auth, body string
}

type fixture struct {
	t    *testing.T
	st   *store.Store
	n    *Notifier
	now  time.Time
	mu   sync.Mutex
	got  []received
	fail bool
	srv  *httptest.Server
	seq  int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &fixture{t: t, st: st}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.fail {
			http.Error(w, "nope", http.StatusTooManyRequests)
			return
		}
		f.got = append(f.got, received{r.URL.Path, r.Header.Get("Title"), r.Header.Get("Click"), r.Header.Get("Authorization"), string(b)})
	}))
	t.Cleanup(f.srv.Close)
	// Noon in UTC: outside the default quiet hours.
	f.now = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	f.n = &Notifier{Store: st, Token: func() string { return "tk_abc" }, Client: f.srv.Client(),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return f.now }}
	f.setPolicy(func(p *policy.Policy) {
		p.Limits.Timezone = "UTC"
		p.Notify = policy.Notify{Enabled: true, ServerURL: f.srv.URL, Topic: "hr-test"}
	})
	return f
}

func (f *fixture) setPolicy(change func(*policy.Policy)) {
	f.t.Helper()
	err := f.st.Tx(func(tx *store.Tx) error {
		p, err := tx.Policy()
		if err != nil {
			return err
		}
		change(&p)
		_, err = tx.SavePolicy(p, f.now)
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

// approval queues an email to Bob with private content.
func (f *fixture) approval() {
	f.t.Helper()
	f.seq++
	th := "th" + string(rune('a'+f.seq))
	err := f.st.Tx(func(tx *store.Tx) error {
		if err := tx.CreateThread(store.Thread{ID: th, Source: "app", Key: th, CreatedBy: "app:owner", CreatedAt: f.now, UpdatedAt: f.now}); err != nil {
			return err
		}
		return tx.CreateEffect(store.Effect{ThreadID: th, Source: "mail", Kind: "action", Action: "send_email", Reach: "others",
			Body: "Private body for Bob", Recipients: "bob@example.com", Status: store.EffectAwaitingApproval,
			Approval: store.ApprovalAwaiting, CreatedAt: f.now})
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) check() {
	f.t.Helper()
	if err := f.n.Check(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) pushes() []received {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]received(nil), f.got...)
}

func TestAPushNamesNothingPrivateAndIsSentOnce(t *testing.T) {
	f := newFixture(t)
	f.approval()
	f.check()
	f.check()
	got := f.pushes()
	if len(got) != 1 {
		t.Fatalf("pushes %+v", got)
	}
	p := got[0]
	if p.path != "/hr-test" || p.title != "An action needs your approval" || !strings.HasPrefix(p.click, "heyrobots://approval/ef_") || p.auth != "Bearer tk_abc" {
		t.Fatalf("push %+v", p)
	}
	for _, secret := range []string{"Bob", "bob@", "Private", "send_email"} {
		if strings.Contains(p.title+p.body+p.click, secret) {
			t.Fatalf("push leaks %q: %+v", secret, p)
		}
	}
}

func TestQuietHoursHoldPushesAndSendThemAsOne(t *testing.T) {
	f := newFixture(t)
	f.now = time.Date(2026, 11, 11, 23, 30, 0, 0, time.UTC)
	f.approval()
	f.approval()
	f.check()
	if n := len(f.pushes()); n != 0 {
		t.Fatalf("%d pushes during quiet hours", n)
	}
	f.now = time.Date(2026, 11, 12, 7, 1, 0, 0, time.UTC)
	f.check()
	got := f.pushes()
	if len(got) != 1 || got[0].title != "2 things wait for you" || got[0].click != "heyrobots://home" {
		t.Fatalf("after quiet hours %+v", got)
	}
}

func TestTheHourlyCapDelaysPushes(t *testing.T) {
	f := newFixture(t)
	f.setPolicy(func(p *policy.Policy) { p.Notify.PerHour = 2 })
	for i := 0; i < 3; i++ {
		f.approval()
		f.check()
		f.now = f.now.Add(time.Minute)
	}
	if n := len(f.pushes()); n != 2 {
		t.Fatalf("%d pushes, want 2 under the cap", n)
	}
	f.now = f.now.Add(time.Hour)
	f.check()
	if n := len(f.pushes()); n != 3 {
		t.Fatalf("%d pushes after the hour", n)
	}
}

func TestAFailedPushIsRetriedAndOffMeansOff(t *testing.T) {
	f := newFixture(t)
	f.fail = true
	f.approval()
	if err := f.n.Check(context.Background()); err == nil {
		t.Fatal("no error from a failed push")
	}
	f.fail = false
	f.check()
	if n := len(f.pushes()); n != 1 {
		t.Fatalf("%d pushes after retry", n)
	}

	f.setPolicy(func(p *policy.Policy) { p.Notify.Enabled = false })
	f.approval()
	f.check()
	if n := len(f.pushes()); n != 1 {
		t.Fatalf("pushed while off: %d", n)
	}
}

func TestQuestionsAndDigestsArePushed(t *testing.T) {
	f := newFixture(t)
	err := f.st.Tx(func(tx *store.Tx) error {
		if err := tx.CreateThread(store.Thread{ID: "thq", Source: "app", Key: "q", CreatedBy: "app:owner", CreatedAt: f.now, UpdatedAt: f.now}); err != nil {
			return err
		}
		m := core.Mandate{Scope: "plan a trip", Strength: core.Explicit, Role: core.RoleOwner, Requester: policy.AppOwner}
		return tx.CreateTicket(store.Ticket{ID: "tkq", Open: true, Stage: string(core.StageQuestion), OriginThreadID: "thq"}, m, "thq", f.now)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.check()
	if got := f.pushes(); len(got) != 1 || got[0].title != "A task has a question for you" || got[0].click != "heyrobots://thread/thq" {
		t.Fatalf("question push %+v", got)
	}
	err = f.st.Tx(func(tx *store.Tx) error {
		return tx.CreateDigest(store.Digest{ID: "dg1", Body: "Private digest", Count: 1, CreatedAt: f.now}, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.check()
	if got := f.pushes(); len(got) != 2 || got[1].title != "Your daily digest is ready" || strings.Contains(got[1].body, "Private") {
		t.Fatalf("digest push %+v", got)
	}
}

func TestQuietHours(t *testing.T) {
	n := policy.Notify{QuietStart: 22, QuietEnd: 7, QuietSet: true}
	for h, want := range map[int]bool{21: false, 22: true, 0: true, 6: true, 7: false, 12: false} {
		if n.Quiet(h) != want {
			t.Errorf("hour %d", h)
		}
	}
	day := policy.Notify{QuietStart: 9, QuietEnd: 17, QuietSet: true}
	if !day.Quiet(12) || day.Quiet(18) {
		t.Error("daytime quiet hours")
	}
	if (policy.Notify{QuietStart: 5, QuietEnd: 5, QuietSet: true}).Quiet(5) {
		t.Error("equal hours mean none")
	}
}

func TestTopicURL(t *testing.T) {
	if u, err := TopicURL("https://ntfy.sh/", "abc_1-2"); err != nil || u != "https://ntfy.sh/abc_1-2" {
		t.Fatal(u, err)
	}
	for _, bad := range [][2]string{{"https://ntfy.sh", "a/b"}, {"https://ntfy.sh", ""}, {"file:///x", "a"}, {"https://u:p@ntfy.sh", "a"}, {"https://ntfy.sh?x=1", "a"}} {
		if _, err := TopicURL(bad[0], bad[1]); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}
