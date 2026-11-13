package schedule

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/jobs"
	"heyrobots/service/internal/store"
)

func TestDue(t *testing.T) {
	loc := time.UTC
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	sc := store.Schedule{Days: "mon,tue,wed,thu,fri", Minute: 8 * 60, CreatedAt: created}
	wed := func(h, m int) time.Time { return time.Date(2026, 11, 11, h, m, 0, 0, loc) }
	if _, due := Due(sc, wed(7, 59), loc); due {
		t.Fatal("due before its time")
	}
	at, due := Due(sc, wed(8, 0), loc)
	if !due || !at.Equal(wed(8, 0)) {
		t.Fatalf("not due at its time: %v %v", at, due)
	}
	ran := wed(8, 0)
	sc.LastRunAt = &ran
	if _, due := Due(sc, wed(9, 0), loc); due {
		t.Fatal("due twice")
	}
	// Saturday: not a chosen day.
	sc.LastRunAt = nil
	if _, due := Due(sc, time.Date(2026, 11, 14, 8, 30, 0, 0, loc), loc); due {
		t.Fatal("due on a weekend")
	}
	// A server that was off all morning catches up once, not later.
	if _, due := Due(sc, wed(13, 0), loc); !due {
		t.Fatal("missed run not caught up")
	}
	if _, due := Due(sc, wed(15, 0), loc); due {
		t.Fatal("a run seven hours late fired")
	}
	// Not before the schedule existed.
	sc.CreatedAt = wed(8, 30)
	if _, due := Due(sc, wed(9, 0), loc); due {
		t.Fatal("fired for a time before it was made")
	}
}

func TestParse(t *testing.T) {
	if d, err := ParseDays("fri, weekdays"); err != nil || d != "mon,tue,wed,thu,fri" {
		t.Fatal(d, err)
	}
	if _, err := ParseDays("someday"); err == nil {
		t.Fatal("accepted a bad day")
	}
	if m, err := ParseTime("08:30"); err != nil || m != 510 {
		t.Fatal(m, err)
	}
	if _, err := ParseTime("8"); err == nil {
		t.Fatal("accepted a bad time")
	}
}

type fakeTools struct {
	calls int
	text  string
}

func (f *fakeTools) Call(_ context.Context, server, tool string, args map[string]any) (string, error) {
	f.calls++
	if server != "linear" || tool != "issues" || args["team"] != "ENG" {
		return "", fmt.Errorf("unexpected call %s/%s %v", server, tool, args)
	}
	return f.text, nil
}

func TestATriggeredJobFiresOncePerNewItem(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	lib := &jobs.Library{Dir: t.TempDir()}
	if err := lib.Save("triage", []byte("---\nname: triage\ndescription: Triage new issues.\nagent: ag_general\nrule: triage\ntrigger: {tool: linear/issues, args: {team: ENG}, every: 5m, key: id}\nsteps: [{name: look, kind: model, instruction: Label it.}]\n---\n")); err != nil {
		t.Fatal(err)
	}
	tools := &fakeTools{text: `{"issues": [{"id": "ENG-1", "title": "old"}]}`}
	src := &Source{Store: st, Intake: intake.New(st, func() time.Time { return now }), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }, Jobs: lib, Tools: tools}
	count := func() int {
		var n int
		st.Tx(func(tx *store.Tx) error {
			ths, _ := tx.Threads("", 10)
			n = len(ths)
			return nil
		})
		return n
	}
	// The first poll learns what is there and fires nothing; the next one
	// inside the interval doesn't call.
	src.Poll(context.Background())
	src.Poll(context.Background())
	if tools.calls != 1 || count() != 0 {
		t.Fatalf("after first polls: %d calls, %d threads", tools.calls, count())
	}
	// A new item fires the job once, however often it's seen.
	tools.text = `{"issues": [{"id": "ENG-1", "title": "old"}, {"id": "ENG-2", "title": "login fails"}]}`
	now = now.Add(5 * time.Minute)
	src.Poll(context.Background())
	now = now.Add(5 * time.Minute)
	src.Poll(context.Background())
	if tools.calls != 3 || count() != 1 {
		t.Fatalf("after a new item: %d calls, %d threads", tools.calls, count())
	}
	var text string
	st.Tx(func(tx *store.Tx) error {
		ths, _ := tx.Threads("", 10)
		v, _ := tx.ThreadView(ths[0].ID)
		text = v.Triggers[0].Text
		return nil
	})
	if !strings.HasPrefix(text, "job:triage\n") || !strings.Contains(text, "login fails") || strings.Contains(text, "old") {
		t.Fatalf("trigger text %q", text)
	}
}
