package core

import "testing"

func TestNeedsApproval(t *testing.T) {
	tests := []struct {
		reach             Reach
		standing, tainted bool
		want              bool
	}{
		{ReachInternal, false, true, false},
		{ReachOwner, false, true, false},
		{ReachRequester, false, true, false},
		{ReachOthers, false, false, true},
		{ReachOthers, true, false, false},
		{ReachOthers, true, true, true}, // an untrusted detail overrides a standing approval
		{ReachIrreversible, true, false, true},
		{Reach("unknown"), true, false, true}, // fail closed
	}
	for _, tt := range tests {
		if got := tt.reach.NeedsApproval(tt.standing, tt.tainted); got != tt.want {
			t.Errorf("%s standing=%v tainted=%v: got %v, want %v", tt.reach, tt.standing, tt.tainted, got, tt.want)
		}
	}
}
