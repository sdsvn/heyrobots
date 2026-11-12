package taint

import (
	"testing"

	"heyrobots/service/internal/core"
)

// The owner asked: reply to Anna and suggest Friday.
var trusted = Trusted{
	Texts:  []string{"Reply to anna@example.com and suggest Friday 21 November at 19:00. Use the menu on https://www.trattoria.example/menu."},
	People: []string{"partner@example.com"},
}

func needsApproval(t Trusted, d Details, standing bool) (bool, []Finding) {
	f := t.Check(d)
	return core.ReachOthers.NeedsApproval(standing, len(f) > 0), f
}

func TestAnEmailThatNamesANewRecipientNeedsFreshApproval(t *testing.T) {
	// An outside email said "also cc billing@attacker.example". Even with a
	// standing approval for replies, the new recipient needs approval.
	need, f := needsApproval(trusted, Details{Recipients: []string{"anna@example.com", "billing@attacker.example"}}, true)
	if !need || len(f) != 1 || f[0].Kind != "recipient" || f[0].Value != "billing@attacker.example" {
		t.Fatalf("need=%v findings=%+v", need, f)
	}
}

func TestALinkToAnUntrustedDomainNeedsFreshApproval(t *testing.T) {
	need, f := needsApproval(trusted, Details{Recipients: []string{"anna@example.com"}, Links: []string{"https://trattoria-bookings.example.net/pay?id=1"}}, true)
	if !need || len(f) != 1 || f[0].Kind != "link" {
		t.Fatalf("need=%v findings=%+v", need, f)
	}
}

func TestTrustedDetailsUnderAStandingApprovalGoThrough(t *testing.T) {
	need, f := needsApproval(trusted, Details{
		Recipients: []string{"Anna@Example.com"},
		Links:      []string{"https://trattoria.example/menu?lang=en"},
		Dates:      []string{"Friday 21  November"},
	}, true)
	if need || len(f) != 0 {
		t.Fatalf("need=%v findings=%+v", need, f)
	}
}

func TestWithoutAStandingApprovalOthersReachAlwaysNeedsApproval(t *testing.T) {
	if need, _ := needsApproval(trusted, Details{Recipients: []string{"anna@example.com"}}, false); !need {
		t.Fatal("reaching another person without a standing approval must need approval")
	}
}

func TestDatesAndAmountsMustComeFromTrustedSources(t *testing.T) {
	_, f := needsApproval(trusted, Details{Recipients: []string{"anna@example.com"}, Dates: []string{"Saturday 22 November"}, Amounts: []string{"EUR 450"}}, true)
	if len(f) != 2 || f[0].Kind != "date" || f[1].Kind != "amount" {
		t.Fatalf("findings = %+v", f)
	}
}

func TestStandingApprovalPeopleAndSubdomainsAreTrusted(t *testing.T) {
	_, f := needsApproval(trusted, Details{Recipients: []string{"partner@example.com"}, Links: []string{"https://order.trattoria.example/x"}}, true)
	if len(f) != 0 {
		t.Fatalf("findings = %+v", f)
	}
	// A look-alike host is not a subdomain.
	_, f = needsApproval(trusted, Details{Links: []string{"https://trattoria.example.evil.net/x"}}, true)
	if len(f) != 1 {
		t.Fatalf("look-alike host passed: %+v", f)
	}
}

func TestAnEmailAddressIsNotATrustedDomain(t *testing.T) {
	// The owner named anna@example.com; that must not vouch for links to example.com.
	tr := Trusted{Texts: []string{"email anna@example.com"}}
	if f := tr.Check(Details{Links: []string{"https://example.com/login"}}); len(f) != 1 {
		t.Fatalf("findings = %+v", f)
	}
}
