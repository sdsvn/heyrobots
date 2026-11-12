// Package mail turns the owner's mailbox into a Source: it reads new mail,
// decides who could really have sent each message, and stores it as a
// Trigger. It also builds outgoing mail.
package mail

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-msgauth/dkim"
	"golang.org/x/net/publicsuffix"
)

// MaxText caps how much of a message's text is stored.
const MaxText = 16000

// Parsed is the part of a message the Assistant uses.
type Parsed struct {
	From       string
	FromName   string
	To         []string
	Cc         []string
	DeliverTo  []string
	Subject    string
	Date       time.Time
	MessageID  string
	References string
	Text       string
	// SentByUs is set on mail the Assistant sent itself.
	SentByUs bool
}

// SentHeader marks mail the Assistant sent, so it is never read back as a
// request.
const SentHeader = "X-Heyrobots-Effect"

// Parse reads a raw message.
func Parse(raw []byte) (Parsed, error) {
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return Parsed{}, err
	}
	var p Parsed
	h := m.Header
	if from, err := mail.ParseAddress(decodeHeader(h.Get("From"))); err == nil {
		p.From, p.FromName = strings.ToLower(from.Address), from.Name
	}
	p.To = addresses(h, "To")
	p.Cc = addresses(h, "Cc")
	p.DeliverTo = addresses(h, "Delivered-To")
	p.Subject = decodeHeader(h.Get("Subject"))
	p.Date, _ = h.Date()
	p.MessageID = strings.TrimSpace(h.Get("Message-Id"))
	p.References = strings.TrimSpace(h.Get("References"))
	p.SentByUs = h.Get(SentHeader) != ""
	text, err := bodyText(h.Get("Content-Type"), h.Get("Content-Transfer-Encoding"), m.Body, 0)
	if err != nil && text == "" {
		text = "(The message's text couldn't be read.)"
	}
	p.Text = shorten(strings.TrimSpace(text), MaxText)
	return p, nil
}

// Recipients returns every address the message was sent or delivered to.
func (p Parsed) Recipients() []string {
	return append(append(append([]string{}, p.To...), p.Cc...), p.DeliverTo...)
}

var dec = &mime.WordDecoder{}

func decodeHeader(s string) string {
	if d, err := dec.DecodeHeader(s); err == nil {
		return d
	}
	return s
}

func addresses(h mail.Header, key string) []string {
	var out []string
	for _, v := range h[key] {
		list, err := mail.ParseAddressList(decodeHeader(v))
		if err != nil {
			continue
		}
		for _, a := range list {
			out = append(out, strings.ToLower(a.Address))
		}
	}
	return out
}

// bodyText finds the message's text, preferring text/plain.
func bodyText(contentType, encoding string, body io.Reader, depth int) (string, error) {
	if depth > 8 {
		return "", errors.New("too deeply nested")
	}
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = "text/plain"
	}
	switch {
	case strings.HasPrefix(mt, "multipart/"):
		r := multipart.NewReader(body, params["boundary"])
		var plain, html string
		for {
			part, err := r.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return firstNonEmpty(plain, html), err
			}
			if strings.HasPrefix(part.Header.Get("Content-Disposition"), "attachment") {
				continue
			}
			t, _ := bodyText(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), part, depth+1)
			pt, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			switch {
			case pt == "text/html" && html == "":
				html = t
			case plain == "" && t != "" && pt != "text/html":
				plain = t
			}
		}
		return firstNonEmpty(plain, html), nil
	case mt == "text/plain" || mt == "text/html":
		b, err := io.ReadAll(io.LimitReader(decodeTransfer(encoding, body), 4<<20))
		s := string(b)
		if !utf8.ValidString(s) {
			s = strings.ToValidUTF8(s, "�")
		}
		if mt == "text/html" {
			s = htmlToText(s)
		}
		return s, err
	default:
		return "", nil
	}
}

func decodeTransfer(enc string, r io.Reader) io.Reader {
	if strings.EqualFold(strings.TrimSpace(enc), "base64") {
		return base64.NewDecoder(base64.StdEncoding, &stripSpace{r: r})
	}
	return r
}

// stripSpace drops line breaks inside base64.
type stripSpace struct{ r io.Reader }

func (s *stripSpace) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	j := 0
	for _, c := range p[:n] {
		if c != '\r' && c != '\n' && c != ' ' && c != '\t' {
			p[j] = c
			j++
		}
	}
	return j, err
}

var (
	tagRE   = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>|<[^>]+>`)
	blankRE = regexp.MustCompile(`\n{3,}`)
)

func htmlToText(s string) string {
	s = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</li>`).ReplaceAllString(s, "\n")
	s = tagRE.ReplaceAllString(s, "")
	s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(s)
	return blankRE.ReplaceAllString(s, "\n\n")
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func shorten(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// DKIMAligned reports whether a valid DKIM signature on the message comes
// from the From address's own domain (relaxed alignment: the same
// organizational domain). That is what proves the sender's domain sent
// it; a From line alone proves nothing.
//
// A signature that covers only part of the body (the l= tag) isn't
// accepted, since text added after the signed part would pass.
func DKIMAligned(raw []byte, from string, lookup func(string) ([]string, error)) (bool, string) {
	at := strings.LastIndexByte(from, '@')
	if at < 0 {
		return false, "no sender address"
	}
	fromOrg, err := publicsuffix.EffectiveTLDPlusOne(from[at+1:])
	if err != nil {
		return false, "sender domain isn't valid"
	}
	if partialBody(raw) {
		return false, "a DKIM signature covers only part of the message"
	}
	vs, err := dkim.VerifyWithOptions(bytes.NewReader(raw), &dkim.VerifyOptions{LookupTXT: lookup, MaxVerifications: 5})
	if err != nil {
		return false, "DKIM couldn't be checked"
	}
	if len(vs) == 0 {
		return false, "no DKIM signature"
	}
	for _, v := range vs {
		if v.Err != nil || !signsFrom(v.HeaderKeys) {
			continue
		}
		org, err := publicsuffix.EffectiveTLDPlusOne(strings.TrimSuffix(strings.ToLower(v.Domain), "."))
		if err == nil && org == fromOrg {
			return true, "DKIM signed by " + v.Domain
		}
	}
	return false, "no valid DKIM signature from " + fromOrg
}

func signsFrom(keys []string) bool {
	for _, k := range keys {
		if strings.EqualFold(strings.TrimSpace(k), "from") {
			return true
		}
	}
	return false
}

var lengthTagRE = regexp.MustCompile(`(?i)(^|;)\s*l\s*=`)

func partialBody(raw []byte) bool {
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	for _, v := range m.Header["Dkim-Signature"] {
		if lengthTagRE.MatchString(strings.ReplaceAll(v, "\r\n", "")) {
			return true
		}
	}
	return false
}
