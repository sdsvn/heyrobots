package devices

import (
	"sync"
	"time"
)

// Limiter blocks an action after too many failures within a window. It
// counts failures for the whole server rather than per address, because
// behind a tunnel every request has the same address.
type Limiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	now      func() time.Time
	failures []time.Time
}

// NewLimiter allows up to max failures per window.
func NewLimiter(max int, window time.Duration, now func() time.Time) *Limiter {
	return &Limiter{max: max, window: window, now: now}
}

// Fail records a failure.
func (l *Limiter) Fail() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune()
	l.failures = append(l.failures, l.now())
}

// Blocked reports whether the limit is reached.
func (l *Limiter) Blocked() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune()
	return len(l.failures) >= l.max
}

func (l *Limiter) prune() {
	cutoff := l.now().Add(-l.window)
	i := 0
	for i < len(l.failures) && l.failures[i].Before(cutoff) {
		i++
	}
	l.failures = l.failures[i:]
}
