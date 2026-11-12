package usage

import (
	"path/filepath"
	"testing"
	"time"

	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

func TestParsePrices(t *testing.T) {
	p, err := ParsePrices("gemini-3.5-flash=0.5/3, my-model = 1 / 2")
	if err != nil {
		t.Fatal(err)
	}
	if p["gemini-3.5-flash"] != (Price{0.5, 3}) || p["my-model"] != (Price{1, 2}) || p["gemini-3.1-flash-lite"] != (Price{0.25, 1.5}) {
		t.Fatalf("prices %+v", p)
	}
	for _, bad := range []string{"x", "x=1", "x=a/b", "=1/2", "x=-1/2"} {
		if _, err := ParsePrices(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestCostRoundsUpAndUnknownModelsCostMore(t *testing.T) {
	l := &Ledger{Prices: Prices{"a": {In: 0.25, Out: 1.5}}}
	if c := l.Cost("a", 1, 1, 0); c != 2 {
		t.Fatalf("cost %d, want 2 (1.75 rounded up)", c)
	}
	if l.Cost("unknown", 1000, 1000, 0) <= l.Cost("a", 1000, 1000, 0) {
		t.Fatal("an unknown model should be estimated high")
	}
}

func TestBudgetStopsAtTheLimitAndResetsMonthly(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "d"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Tx(func(tx *store.Tx) error {
		p, _ := tx.Policy()
		p.Limits = policy.Limits{MonthlyBudgetUSD: 1, Timezone: "UTC"}
		_, err := tx.SavePolicy(p, time.Now())
		return err
	})
	now := time.Date(2026, 11, 30, 23, 0, 0, 0, time.UTC)
	l := &Ledger{Store: st, Prices: Prices{"m": {In: 1, Out: 1}}, Now: func() time.Time { return now }}
	ctx := WithTicket(t.Context(), "tk1")
	l.Record(ctx, "m", 600_000, 0, 0)
	if msg := l.BudgetStop(); msg != "" {
		t.Fatalf("stopped at $0.60: %s", msg)
	}
	l.Record(ctx, "m", 400_000, 0, 0)
	if msg := l.BudgetStop(); msg == "" {
		t.Fatal("not stopped at $1.00")
	}
	now = now.Add(2 * time.Hour) // December
	if msg := l.BudgetStop(); msg != "" {
		t.Fatalf("a new month should start fresh: %s", msg)
	}
	spend, _ := st.TicketSpend(time.Time{}, []string{"tk1"}, 0)
	if spend["tk1"] != 1_000_000 {
		t.Fatalf("ticket spend %d", spend["tk1"])
	}
}

func TestCachedTokensCostATenth(t *testing.T) {
	l := &Ledger{Prices: Prices{"a": {In: 1, Out: 1}}}
	fresh := l.Cost("a", 1000, 0, 0)
	cached := l.Cost("a", 1000, 0, 1000)
	if fresh != 1000 || cached != 100 {
		t.Fatalf("fresh %d cached %d", fresh, cached)
	}
	// More cached than sent counts as all cached, never negative.
	if l.Cost("a", 100, 0, 500) != 10 {
		t.Fatal("over-reported cache")
	}
}
