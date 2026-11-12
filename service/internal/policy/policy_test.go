package policy

import (
	"testing"

	"heyrobots/service/internal/core"
)

func TestOnlyOwnerDevicesApprove(t *testing.T) {
	if !CanApprove(DeviceOwner) {
		t.Fatal("owner device must be able to approve")
	}
	if CanApprove(DeviceDelegate) {
		t.Fatal("delegate device must never approve")
	}
	if CanApprove("") {
		t.Fatal("a device with no role must never approve")
	}
}

func TestRoleOfMatchesExactSourceAndID(t *testing.T) {
	owner := core.Identity{Source: core.SourceMail, ID: "me@example.com"}
	p := Policy{Owner: []core.Identity{owner}}
	if r, _ := p.RoleOf(owner); r != core.RoleOwner {
		t.Fatalf("role = %s, want owner", r)
	}
	if r, _ := p.RoleOf(core.Identity{Source: core.SourceSlack, ID: owner.ID}); r != core.RoleOther {
		t.Fatalf("same ID on another Source must be someone else, got %s", r)
	}
}
