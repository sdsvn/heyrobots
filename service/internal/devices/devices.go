// Package devices pairs companion apps with the server and authenticates
// them. Codes and tokens are random, shown once, and stored only as hashes.
package devices

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"heyrobots/service/internal/store"
)

// Roles a device can have.
const (
	RoleOwner    = "owner"
	RoleDelegate = "delegate"
)

// PairingTTL is how long a pairing code stays valid.
const PairingTTL = 10 * time.Minute

// Errors from pairing and authentication.
var (
	ErrInvalidCode  = errors.New("code is invalid, used or expired")
	ErrRateLimited  = errors.New("too many failed attempts; try again later")
	ErrInvalidToken = errors.New("device token is invalid or revoked")
	ErrBadRequest   = errors.New("bad request")
)

// Service pairs and authenticates devices.
type Service struct {
	store   *store.Store
	now     func() time.Time
	limiter *Limiter
}

// New returns a Service. now is normally time.Now.
func New(s *store.Store, now func() time.Time) *Service {
	return &Service{store: s, now: now, limiter: NewLimiter(10, 10*time.Minute, now)}
}

// Code is a pairing code as shown to a person.
type Code struct {
	Code         string
	Role         string
	DelegateName string
	ExpiresAt    time.Time
}

// NewCode creates a one-time pairing code. createdBy is the device that
// asked for it, or "setup" for the first device.
func (s *Service) NewCode(role, delegateName, createdBy string) (Code, error) {
	switch role {
	case RoleOwner:
		delegateName = ""
	case RoleDelegate:
		delegateName = strings.TrimSpace(delegateName)
		if delegateName == "" || len(delegateName) > 80 {
			return Code{}, fmt.Errorf("%w: a delegate invite needs a name of up to 80 characters", ErrBadRequest)
		}
	default:
		return Code{}, fmt.Errorf("%w: unknown role %q", ErrBadRequest, role)
	}
	code, err := randomCode()
	if err != nil {
		return Code{}, err
	}
	now := s.now().UTC()
	c := Code{Code: code, Role: role, DelegateName: delegateName, ExpiresAt: now.Add(PairingTTL)}
	err = s.store.CreatePairingCode(store.PairingCode{
		CodeHash: hashCode(code), Role: role, DelegateName: delegateName,
		CreatedBy: createdBy, CreatedAt: now, ExpiresAt: c.ExpiresAt,
	})
	return c, err
}

// Pair uses a pairing code to create a device and returns the device and
// its token. The token is never stored and can't be shown again.
func (s *Service) Pair(code, name, platform string) (store.Device, string, error) {
	name, platform = strings.TrimSpace(name), strings.TrimSpace(platform)
	if name == "" || len(name) > 80 || len(platform) > 40 {
		return store.Device{}, "", fmt.Errorf("%w: device name must be 1 to 80 characters and platform up to 40", ErrBadRequest)
	}
	if s.limiter.Blocked() {
		return store.Device{}, "", ErrRateLimited
	}
	token, err := randomToken()
	if err != nil {
		return store.Device{}, "", err
	}
	id, err := randomID()
	if err != nil {
		return store.Device{}, "", err
	}
	d, err := s.store.Pair(hashCode(code), s.now().UTC(), store.Device{
		ID: id, Name: name, Platform: platform, TokenHash: HashToken(token),
	})
	if errors.Is(err, store.ErrCodeInvalid) {
		s.limiter.Fail()
		return store.Device{}, "", ErrInvalidCode
	}
	if err != nil {
		return store.Device{}, "", err
	}
	return d, token, nil
}

// Authenticate returns the active device that owns a token.
func (s *Service) Authenticate(token string) (store.Device, error) {
	if !strings.HasPrefix(token, tokenPrefix) {
		return store.Device{}, ErrInvalidToken
	}
	d, err := s.store.DeviceByTokenHash(HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return store.Device{}, ErrInvalidToken
	}
	if err != nil {
		return store.Device{}, err
	}
	now := s.now().UTC()
	if d.LastSeenAt == nil || now.Sub(*d.LastSeenAt) > time.Minute {
		_ = s.store.TouchDevice(d.ID, now)
	}
	return d, nil
}

// SetupOpen reports whether first-run setup is open: no owner device is
// active. If the owner revokes every owner device, setup opens again, and
// still needs the code printed in the terminal.
func (s *Service) SetupOpen() (bool, error) {
	n, err := s.store.ActiveOwnerDevices()
	return n == 0, err
}

// SetupCode is the one-time code printed in the terminal that unlocks the
// setup page. It never trusts the request's source address: tunnels and
// proxies make every request look local.
type SetupCode struct {
	mu      sync.Mutex
	hash    []byte
	limiter *Limiter
	onNew   func(code string)
}

// NewSetupCode creates a setup code and passes it to show, which prints it
// to the terminal and log. After too many failed attempts the code is
// replaced and the new one is shown.
func NewSetupCode(now func() time.Time, show func(code string)) (*SetupCode, error) {
	sc := &SetupCode{limiter: NewLimiter(5, 10*time.Minute, now), onNew: show}
	return sc, sc.rotate()
}

func (sc *SetupCode) rotate() error {
	code, err := randomCode()
	if err != nil {
		return err
	}
	sc.hash = hashCode(code)
	sc.onNew(code)
	return nil
}

// Check reports whether code is the current setup code.
func (sc *SetupCode) Check(code string) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.limiter.Blocked() {
		return ErrRateLimited
	}
	if subtle.ConstantTimeCompare(hashCode(code), sc.hash) == 1 {
		return nil
	}
	sc.limiter.Fail()
	if sc.limiter.Blocked() {
		if err := sc.rotate(); err != nil {
			return err
		}
	}
	return ErrInvalidCode
}

// Codes use Crockford's base32 alphabet, which has no I, L, O or U, so they
// are easy to read aloud and type. Ten characters carry 50 bits.
const codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func randomCode() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 0, 11)
	for i, x := range b {
		if i == 5 {
			out = append(out, '-')
		}
		out = append(out, codeAlphabet[int(x)%32])
	}
	return string(out), nil
}

// normalizeCode makes typing forgiving: case, spaces and dashes don't
// matter, and letters that look like digits are read as digits.
func normalizeCode(code string) string {
	r := strings.NewReplacer("-", "", " ", "", "O", "0", "I", "1", "L", "1")
	return r.Replace(strings.ToUpper(strings.TrimSpace(code)))
}

func hashCode(code string) []byte {
	h := sha256.Sum256([]byte("heyrobots-code:" + normalizeCode(code)))
	return h[:]
}

const tokenPrefix = "hr_"

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken returns the stored form of a device token.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "dev_" + hex.EncodeToString(b), nil
}
