package devices

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"heyrobots/service/internal/store"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newService(t *testing.T) (*Service, *clock) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c := &clock{t: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}
	return New(s, c.now), c
}

func TestPairAndAuthenticate(t *testing.T) {
	svc, _ := newService(t)
	code, err := svc.NewCode(RoleOwner, "", "setup")
	if err != nil {
		t.Fatal(err)
	}
	// Typing is forgiving: lower case, no dash, an O for a zero.
	typed := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(code.Code, "-", ""), "0", "o"))
	d, token, err := svc.Pair(typed, "Sarath's iPhone", "ios")
	if err != nil {
		t.Fatal(err)
	}
	if d.Role != RoleOwner || !strings.HasPrefix(token, tokenPrefix) {
		t.Fatalf("device %+v token %q", d, token)
	}
	got, err := svc.Authenticate(token)
	if err != nil || got.ID != d.ID {
		t.Fatalf("Authenticate = %+v, %v", got, err)
	}
	if _, err := svc.Authenticate(token + "x"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("tampered token err = %v", err)
	}
	if open, _ := svc.SetupOpen(); open {
		t.Fatal("setup must close once an owner device is paired")
	}
}

func TestCodeExpires(t *testing.T) {
	svc, c := newService(t)
	code, _ := svc.NewCode(RoleOwner, "", "setup")
	c.advance(PairingTTL + time.Second)
	if _, _, err := svc.Pair(code.Code, "phone", ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("err = %v, want ErrInvalidCode", err)
	}
}

func TestDelegateCodeCarriesRoleAndName(t *testing.T) {
	svc, _ := newService(t)
	if _, err := svc.NewCode(RoleDelegate, "  ", "dev_1"); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("delegate invite without a name err = %v", err)
	}
	code, _ := svc.NewCode(RoleDelegate, "partner", "dev_1")
	d, _, err := svc.Pair(code.Code, "partner's phone", "android")
	if err != nil {
		t.Fatal(err)
	}
	if d.Role != RoleDelegate || d.DelegateName != "partner" {
		t.Fatalf("device %+v", d)
	}
	if open, _ := svc.SetupOpen(); !open {
		t.Fatal("a delegate device must not close setup")
	}
}

func TestPairingIsRateLimited(t *testing.T) {
	svc, c := newService(t)
	good, _ := svc.NewCode(RoleOwner, "", "setup")
	for range 10 {
		svc.Pair("AAAAA-AAAAA", "x", "")
	}
	if _, _, err := svc.Pair(good.Code, "phone", ""); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited even for a good code", err)
	}
	c.advance(10*time.Minute + time.Second)
	fresh, _ := svc.NewCode(RoleOwner, "", "setup")
	if _, _, err := svc.Pair(fresh.Code, "phone", ""); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

func TestSetupCodeRotatesAfterTooManyFailures(t *testing.T) {
	c := &clock{t: time.Now()}
	var shown []string
	sc, err := NewSetupCode(c.now, func(code string) { shown = append(shown, code) })
	if err != nil {
		t.Fatal(err)
	}
	first := shown[0]
	if err := sc.Check(first); err != nil {
		t.Fatalf("correct code refused: %v", err)
	}
	for range 5 {
		sc.Check("WRONG-WRONG")
	}
	if len(shown) != 2 {
		t.Fatalf("setup code shown %d times, want a new one after 5 failures", len(shown))
	}
	if err := sc.Check(first); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited during lockout", err)
	}
	c.advance(10*time.Minute + time.Second)
	if err := sc.Check(first); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("old code err = %v, want ErrInvalidCode", err)
	}
	if err := sc.Check(shown[1]); err != nil {
		t.Fatalf("new code refused: %v", err)
	}
}

func TestCodeAlphabetIsReadable(t *testing.T) {
	for range 50 {
		code, _ := randomCode()
		if strings.ContainsAny(code, "ILOU") || len(code) != 11 || code[5] != '-' {
			t.Fatalf("bad code %q", code)
		}
	}
}
