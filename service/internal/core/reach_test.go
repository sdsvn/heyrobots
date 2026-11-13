package core

import "testing"

func TestNeedsApproval(t *testing.T) {
	for r, want := range map[Reach]bool{
		ReachInternal: false, ReachOwner: false, ReachRequester: false,
		ReachOthers: true, ReachIrreversible: true, Reach("unknown"): true, // fail closed
	} {
		if got := r.NeedsApproval(); got != want {
			t.Errorf("%s: got %v, want %v", r, got, want)
		}
	}
}
