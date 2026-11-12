// Package taint decides, in plain code, whether an action that reaches
// other people needs a fresh approval because its controlling details came
// from content nobody trusted.
//
// It follows the CaMeL paper's idea: untrusted content may shape what the
// agent writes, but not where an action goes or what it commits to. So the
// check looks only at an action's controlling details: who it goes to, the
// links and attachments it carries, and the dates and amounts it commits
// to. Free text in the body may draw on anything, because its recipients
// are already fixed by trusted sources.
//
// A detail counts as trusted only when it appears in a trusted source: the
// owner's own words, a standing approval, the owner's memory or address
// book, or people the owner already wrote to. A detail found nowhere
// trusted, whether it came from an outside email or the model made it up,
// needs a fresh approval. This fails closed.
package taint

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Details are an action's controlling details.
type Details struct {
	Recipients []string
	Links      []string
	Dates      []string
	Amounts    []string
}

// Trusted holds what the owner vouches for.
type Trusted struct {
	// Texts are trusted writing, such as the owner's own messages.
	Texts []string
	// People are trusted addresses, such as standing approval people and
	// the owner's address book.
	People []string
	// Domains are trusted link hosts.
	Domains []string
}

// Finding is a controlling detail that came from no trusted source.
type Finding struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Why   string `json:"why"`
}

var (
	urlRE    = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"')\]]+`)
	emailRE  = regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)
	domainRE = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9\-]{0,61}[a-z0-9])?\.)+[a-z]{2,}\b`)
	spaceRE  = regexp.MustCompile(`\s+`)
)

// Links returns the URLs in a text.
func Links(text string) []string { return urlRE.FindAllString(text, -1) }

// Emails returns the email addresses in a text.
func Emails(text string) []string { return emailRE.FindAllString(text, -1) }

// Check returns the controlling details that no trusted source vouches for.
func (t Trusted) Check(d Details) []Finding {
	var out []Finding
	joined := normalize(strings.Join(t.Texts, "\n"))
	hosts := t.trustedHosts()
	for _, r := range d.Recipients {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" {
			continue
		}
		if !slices.ContainsFunc(t.People, func(p string) bool { return strings.EqualFold(strings.TrimSpace(p), r) }) && !strings.Contains(joined, r) {
			out = append(out, Finding{Kind: "recipient", Value: r, Why: "this recipient appears in no trusted source"})
		}
	}
	for _, l := range d.Links {
		host := linkHost(l)
		if host == "" {
			out = append(out, Finding{Kind: "link", Value: l, Why: "this link can't be read"})
			continue
		}
		if !slices.ContainsFunc(hosts, func(h string) bool { return host == h || strings.HasSuffix(host, "."+h) }) {
			out = append(out, Finding{Kind: "link", Value: l, Why: "this link's site " + host + " appears in no trusted source"})
		}
	}
	for _, v := range d.Dates {
		if n := normalize(v); n != "" && !strings.Contains(joined, n) {
			out = append(out, Finding{Kind: "date", Value: v, Why: "this date appears in no trusted source"})
		}
	}
	for _, v := range d.Amounts {
		if n := normalize(v); n != "" && !strings.Contains(joined, n) {
			out = append(out, Finding{Kind: "amount", Value: v, Why: "this amount appears in no trusted source"})
		}
	}
	return out
}

func (t Trusted) trustedHosts() []string {
	var hosts []string
	add := func(h string) {
		h = strings.TrimPrefix(strings.ToLower(h), "www.")
		if h != "" && !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	for _, d := range t.Domains {
		add(d)
	}
	for _, text := range t.Texts {
		for _, l := range Links(text) {
			add(linkHost(l))
		}
		// Bare domains the owner wrote, such as "book it on opentable.com".
		withoutEmails := emailRE.ReplaceAllString(text, " ")
		for _, d := range domainRE.FindAllString(withoutEmails, -1) {
			add(d)
		}
	}
	return hosts
}

func linkHost(l string) string {
	u, err := url.Parse(strings.TrimSpace(l))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

func normalize(s string) string {
	return strings.TrimSpace(spaceRE.ReplaceAllString(strings.ToLower(s), " "))
}
