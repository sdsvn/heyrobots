// Package usage records what model calls cost and enforces the owner's
// monthly budget. It imports no ADK code.
package usage

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"heyrobots/service/internal/store"
)

// Price is what a model costs, in US dollars per million tokens. Output
// includes thinking tokens.
type Price struct {
	In, Out float64
}

// Prices maps model names to prices.
type Prices map[string]Price

// DefaultPrices are Google's standard paid-tier prices, checked on
// 2026-11-11 at ai.google.dev/gemini-api/docs/pricing. Models not listed
// use Fallback. Set HEYROBOTS_MODEL_PRICES to change or add prices.
func DefaultPrices() Prices {
	return Prices{
		"gemini-3.1-flash-lite": {In: 0.25, Out: 1.50},
		"gemini-3.5-flash-lite": {In: 0.30, Out: 2.50},
		// Anthropic's first-party prices, checked 2026-11-03. Haiku's are for
		// prompts up to 100K tokens.
		"claude-haiku-5-5":  {In: 0.10, Out: 0.50},
		"claude-sonnet-5-5": {In: 2.00, Out: 10.00},
		"claude-opus-5-5":   {In: 4.00, Out: 20.00},
	}
}

// Fallback is used for a model with no known price. It is set high on
// purpose, so an unknown price makes the budget stop early rather than late.
var Fallback = Price{In: 2.00, Out: 12.00}

// ParsePrices reads "model=in/out,model=in/out" into prices, on top of the
// defaults.
func ParsePrices(s string) (Prices, error) {
	p := DefaultPrices()
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, val, ok := strings.Cut(part, "=")
		in, out, ok2 := strings.Cut(val, "/")
		if !ok || !ok2 || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("model price %q: want model=input/output in dollars per million tokens", part)
		}
		i, err1 := strconv.ParseFloat(strings.TrimSpace(in), 64)
		o, err2 := strconv.ParseFloat(strings.TrimSpace(out), 64)
		if err1 != nil || err2 != nil || i < 0 || o < 0 {
			return nil, fmt.Errorf("model price %q: prices must be numbers of dollars", part)
		}
		p[strings.TrimSpace(name)] = Price{In: i, Out: o}
	}
	return p, nil
}

type ticketKey struct{}

// WithTicket marks model calls made with ctx as work for a Ticket.
func WithTicket(ctx context.Context, ticketID string) context.Context {
	return context.WithValue(ctx, ticketKey{}, ticketID)
}

// TicketFrom returns the Ticket a context works for, if any.
func TicketFrom(ctx context.Context) string {
	id, _ := ctx.Value(ticketKey{}).(string)
	return id
}

// Ledger records model use and answers budget questions.
type Ledger struct {
	Store  *store.Store
	Prices Prices
	// Override gives a price the owner set, or zero for a model on their
	// own machine, ahead of Prices.
	Override func(model string) (Price, bool)
	Log      *slog.Logger
	Now      func() time.Time

	warned sync.Map
}

func (l *Ledger) now() time.Time {
	if l.Now == nil {
		return time.Now()
	}
	return l.Now()
}

// CachedRate is what a cached input token costs as a share of a fresh
// one. Anthropic, Gemini and DeepSeek all charge about a tenth.
const CachedRate = 0.1

// Cost returns a call's cost in millionths of a dollar, rounded up. cached
// of the in tokens were read from the provider's prompt cache.
func (l *Ledger) Cost(model string, in, out, cached int64) int64 {
	p, ok := Price{}, false
	if l.Override != nil {
		p, ok = l.Override(model)
	}
	if !ok {
		p, ok = l.Prices[model]
	}
	if !ok {
		p = Fallback
		if _, seen := l.warned.LoadOrStore(model, true); !seen && l.Log != nil {
			l.Log.Warn("no price known for this model; using a high estimate. Set HEYROBOTS_MODEL_PRICES to fix it", "model", model)
		}
	}
	// Dollars per million tokens is millionths of a dollar per token.
	cached = min(max(cached, 0), in)
	return int64(math.Ceil(float64(in-cached)*p.In + float64(cached)*p.In*CachedRate + float64(out)*p.Out))
}

// Record stores one model call. Errors are logged, never returned: losing
// a usage row must not fail the work.
func (l *Ledger) Record(ctx context.Context, model string, in, out, cached int64) {
	if in == 0 && out == 0 {
		return
	}
	u := store.Usage{At: l.now().UTC(), TicketID: TicketFrom(ctx), Model: model, InputTokens: in, OutputTokens: out, CachedTokens: cached, CostMicros: l.Cost(model, in, out, cached)}
	if err := l.Store.RecordUsage(u); err != nil && l.Log != nil {
		l.Log.Error("record model usage", "err", err)
	}
}

// MonthStart is the start of the owner's current calendar month.
func MonthStart(now time.Time, loc *time.Location) time.Time {
	t := now.In(loc)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
}

// Status is this month's spending against the budget.
type Status struct {
	SpentUSD  float64
	BudgetUSD float64
	Since     time.Time
}

// Over reports whether the budget is used up.
func (s Status) Over() bool { return s.SpentUSD >= s.BudgetUSD }

// Month returns this month's spending and the budget.
func (l *Ledger) Month() (Status, error) {
	p, err := l.Store.Policy()
	if err != nil {
		return Status{}, err
	}
	lim := p.Limits.Effective()
	since := MonthStart(l.now(), lim.Location())
	spent, err := l.Store.SpendSince(since.UTC())
	if err != nil {
		return Status{}, err
	}
	return Status{SpentUSD: float64(spent) / 1e6, BudgetUSD: lim.MonthlyBudgetUSD, Since: since}, nil
}

// BudgetStop returns a message for the owner when this month's budget is
// used up, or "" when work may go on. If the spending can't be read, work
// goes on and the error is logged.
func (l *Ledger) BudgetStop() string {
	s, err := l.Month()
	if err != nil {
		if l.Log != nil {
			l.Log.Error("check model budget", "err", err)
		}
		return ""
	}
	if !s.Over() {
		return ""
	}
	return fmt.Sprintf("This month's model budget of $%.2f is used up ($%.2f spent), so I've paused this task.", s.BudgetUSD, s.SpentUSD)
}
