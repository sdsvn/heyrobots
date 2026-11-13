// Package policy holds who may ask the Assistant for work, its accept mode
// and its toolsets. Like core, it imports no ADK or model code.
package policy

import (
	"slices"
	"time"

	"heyrobots/service/internal/core"
)

// AcceptMode decides what happens to inferred candidates.
type AcceptMode string

const (
	// ModeOff admits no inferred candidates.
	ModeOff AcceptMode = "off"
	// ModeObserve assesses inferred candidates and records "would accept",
	// but never starts the work. It is the default.
	ModeObserve AcceptMode = "observe"
	// ModeAuto accepts an inferred candidate when a rule matches it.
	ModeAuto AcceptMode = "auto"
)

// Delegate is a person the owner invited to ask the Assistant for work.
type Delegate struct {
	Name string `json:"name"`
	// Identities are the delegate's identities, each on one Source.
	Identities []core.Identity `json:"identities"`
	// Toolsets limits what the delegate may ask for. It is further limited
	// to the owner's enabled toolsets.
	Toolsets []core.Toolset `json:"toolsets"`
	// Agents are the ids of the agents the delegate's requests may go to.
	// None means only the General agent.
	Agents []string `json:"agents,omitempty"`
}

// Rule describes, in the owner's words, a kind of proactive work the
// Assistant may start without being asked.
type Rule struct {
	Name    string `json:"name"`
	Text    string `json:"text"`
	Enabled bool   `json:"enabled"`
	// Agents are the ids of the agents work under this rule may go to.
	// None means only the General agent.
	Agents []string `json:"agents,omitempty"`
}

// Policy is one version of the Assistant's authorization settings.
type Policy struct {
	Version int        `json:"version"`
	Mode    AcceptMode `json:"mode"`
	// Owner lists the owner's proven identities, each on one Source.
	Owner []core.Identity `json:"owner"`
	// Toolsets are the toolsets the owner has enabled.
	Toolsets  []core.Toolset `json:"toolsets"`
	Delegates []Delegate     `json:"delegates"`
	Rules     []Rule         `json:"rules"`
	// Bots lists bot identities whose messages may become inferred
	// candidates. Other bots are never admitted.
	Bots []core.Identity `json:"bots"`
	// Limits are the owner's caps on spending and sending. Zero values mean
	// the defaults; use Effective to read them.
	Limits Limits `json:"limits,omitempty"`
	// Notify says where and when to send push notifications.
	Notify Notify `json:"notify,omitempty"`
}

// Notify configures push notifications through ntfy. The access token is
// a secret and lives in the vault, not here.
type Notify struct {
	Enabled bool `json:"enabled,omitempty"`
	// ServerURL is the ntfy server, such as https://ntfy.sh.
	ServerURL string `json:"server_url,omitempty"`
	// Topic should be long and random on a shared server like ntfy.sh,
	// since anyone who knows it can read it there without a token.
	Topic string `json:"topic,omitempty"`
	// QuietStart and QuietEnd are local hours. Pushes wait during quiet
	// hours and are sent as one when they end. Equal hours mean no quiet
	// hours. QuietSet distinguishes 0 from unset.
	QuietStart int  `json:"quiet_start,omitempty"`
	QuietEnd   int  `json:"quiet_end,omitempty"`
	QuietSet   bool `json:"quiet_set,omitempty"`
	// PerHour caps pushes in any hour. Extra items wait and go out together.
	PerHour int `json:"per_hour,omitempty"`
}

// Default notification settings.
const (
	DefaultNtfyServer  = "https://ntfy.sh"
	DefaultQuietStart  = 22
	DefaultQuietEnd    = 7
	DefaultPushPerHour = 6
)

// Effective returns the settings with defaults filled in.
func (n Notify) Effective() Notify {
	if n.ServerURL == "" {
		n.ServerURL = DefaultNtfyServer
	}
	if !n.QuietSet {
		n.QuietStart, n.QuietEnd, n.QuietSet = DefaultQuietStart, DefaultQuietEnd, true
	}
	if n.PerHour <= 0 {
		n.PerHour = DefaultPushPerHour
	}
	return n
}

// Quiet reports whether a local hour falls in the quiet hours.
func (n Notify) Quiet(hour int) bool {
	n = n.Effective()
	switch {
	case n.QuietStart == n.QuietEnd:
		return false
	case n.QuietStart < n.QuietEnd:
		return hour >= n.QuietStart && hour < n.QuietEnd
	default:
		return hour >= n.QuietStart || hour < n.QuietEnd
	}
}

// Limits caps what the Assistant may spend and send.
type Limits struct {
	// MonthlyBudgetUSD is the most the Assistant may spend on models in a
	// calendar month. A task that reaches it stops and asks.
	MonthlyBudgetUSD float64 `json:"monthly_budget_usd,omitempty"`
	// SendsPerHour and SendsPerDay cap messages and actions that reach other
	// people through one Source, so a mistake can't get the owner's
	// accounts flagged.
	SendsPerHour int `json:"sends_per_hour,omitempty"`
	SendsPerDay  int `json:"sends_per_day,omitempty"`
	// DigestHour is the local hour, 0 to 23, when the daily digest is put
	// together. DigestHourSet distinguishes midnight from unset.
	DigestHour    int  `json:"digest_hour,omitempty"`
	DigestHourSet bool `json:"digest_hour_set,omitempty"`
	// Timezone is an IANA name such as Europe/Berlin. Empty means the
	// server's local time.
	Timezone string `json:"timezone,omitempty"`
}

// Default limits.
const (
	DefaultMonthlyBudgetUSD = 20
	DefaultSendsPerHour     = 10
	DefaultSendsPerDay      = 50
	DefaultDigestHour       = 8
)

// Effective returns the limits with defaults filled in.
func (l Limits) Effective() Limits {
	if l.MonthlyBudgetUSD <= 0 {
		l.MonthlyBudgetUSD = DefaultMonthlyBudgetUSD
	}
	if l.SendsPerHour <= 0 {
		l.SendsPerHour = DefaultSendsPerHour
	}
	if l.SendsPerDay <= 0 {
		l.SendsPerDay = DefaultSendsPerDay
	}
	if !l.DigestHourSet {
		l.DigestHour, l.DigestHourSet = DefaultDigestHour, true
	}
	return l
}

// Location returns the owner's time zone, or the server's when it is unset
// or unknown.
func (l Limits) Location() *time.Location {
	if l.Timezone != "" {
		if loc, err := time.LoadLocation(l.Timezone); err == nil {
			return loc
		}
	}
	return time.Local
}

// AppOwner is the owner's identity on the app Source. Every owner device
// acts as this identity.
var AppOwner = core.Identity{Source: core.SourceApp, ID: "owner"}

// AppDelegate is a delegate's identity on the app Source.
func AppDelegate(name string) core.Identity {
	return core.Identity{Source: core.SourceApp, ID: "delegate:" + name}
}

// Default is the policy a new Assistant starts with: the owner on the app,
// research, writing and setup enabled, and proactive work only observed.
func Default() Policy {
	return Policy{
		Version:  1,
		Mode:     ModeObserve,
		Owner:    []core.Identity{AppOwner},
		Toolsets: []core.Toolset{core.ToolsetResearch, core.ToolsetWriting, core.ToolsetSetup},
	}
}

// EnabledRule returns the enabled rule with the given name.
func (p Policy) EnabledRule(name string) (Rule, bool) {
	for _, r := range p.Rules {
		if r.Name == name && r.Enabled {
			return r, true
		}
	}
	return Rule{}, false
}

// RoleOf returns who an identity is to this Assistant. It matches only on
// the exact Source and ID, never across Sources. For a delegate it also
// returns the matching Delegate.
func (p Policy) RoleOf(id core.Identity) (core.Role, *Delegate) {
	if slices.Contains(p.Owner, id) {
		return core.RoleOwner, nil
	}
	for i := range p.Delegates {
		if slices.Contains(p.Delegates[i].Identities, id) {
			return core.RoleDelegate, &p.Delegates[i]
		}
	}
	return core.RoleOther, nil
}

// IsListedBot reports whether a bot identity is on the bot list.
func (p Policy) IsListedBot(id core.Identity) bool {
	return slices.Contains(p.Bots, id)
}

// DelegateNamed returns the delegate with the given name.
func (p Policy) DelegateNamed(name string) (*Delegate, bool) {
	for i := range p.Delegates {
		if p.Delegates[i].Name == name {
			return &p.Delegates[i], true
		}
	}
	return nil, false
}

// DeviceRole is the role a paired device's token carries.
type DeviceRole string

const (
	DeviceOwner    DeviceRole = "owner"
	DeviceDelegate DeviceRole = "delegate"
)

// CanApprove reports whether a device may approve an action. Actions run on
// the owner's accounts, so only the owner's devices may approve, including
// for work a delegate asked for.
func CanApprove(r DeviceRole) bool {
	return r == DeviceOwner
}
