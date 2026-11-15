package api

import (
	"testing"

	"heyrobots/service/internal/store"
)

func TestTheRecordLineSaysFailuresAsPlainlyAsPasses(t *testing.T) {
	for _, c := range []struct {
		o    store.OutcomeSummary
		want string
	}{
		{store.OutcomeSummary{}, "No checked work yet"},
		{store.OutcomeSummary{Checked: 12, FirstTry: 10}, "10 of 12 passed first time"},
		{store.OutcomeSummary{Checked: 12, FirstTry: 9, Failed: 2}, "9 of 12 passed first time · 2 didn't pass"},
	} {
		if got := recordLine(c.o); got != c.want {
			t.Errorf("recordLine(%+v) = %q, want %q", c.o, got, c.want)
		}
	}
}
