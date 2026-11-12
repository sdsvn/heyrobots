package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"time"
)

// Outgoing is a message to send or draft.
type Outgoing struct {
	From    string
	To      []string
	Cc      []string
	Subject string
	Body    string
	// InReplyTo and References thread a reply.
	InReplyTo  string
	References string
	// Effect is the outbox id, recorded in a header so the Assistant never
	// reads its own mail back as a request.
	Effect string
}

// Compose writes the raw message.
func (o Outgoing) Compose(now time.Time) ([]byte, error) {
	if len(o.To) == 0 {
		return nil, errors.New("a message needs at least one recipient")
	}
	var b bytes.Buffer
	header := func(k, v string) error {
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("the %s line can't contain a line break", k)
		}
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		return nil
	}
	list := func(addrs []string) (string, error) {
		var out []string
		for _, a := range addrs {
			p, err := mail.ParseAddress(strings.TrimSpace(a))
			if err != nil {
				return "", fmt.Errorf("%q isn't an email address", a)
			}
			out = append(out, p.String())
		}
		return strings.Join(out, ", "), nil
	}
	to, err := list(o.To)
	if err != nil {
		return nil, err
	}
	steps := []struct{ k, v string }{
		{"From", o.From},
		{"To", to},
		{"Subject", mime.QEncoding.Encode("utf-8", o.Subject)},
		{"Date", now.Format(time.RFC1123Z)},
		{"Message-ID", "<" + randomID() + "@heyrobots>"},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/plain; charset=utf-8"},
		{"Content-Transfer-Encoding", "base64"},
	}
	if len(o.Cc) > 0 {
		cc, err := list(o.Cc)
		if err != nil {
			return nil, err
		}
		steps = append(steps, struct{ k, v string }{"Cc", cc})
	}
	if o.InReplyTo != "" {
		steps = append(steps, struct{ k, v string }{"In-Reply-To", o.InReplyTo})
		refs := strings.TrimSpace(o.References + " " + o.InReplyTo)
		steps = append(steps, struct{ k, v string }{"References", refs})
	}
	if o.Effect != "" {
		steps = append(steps, struct{ k, v string }{SentHeader, o.Effect})
	}
	for _, s := range steps {
		if s.v == "" && s.k != "Subject" {
			continue
		}
		if err := header(s.k, s.v); err != nil {
			return nil, err
		}
	}
	b.WriteString("\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(o.Body))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return b.Bytes(), nil
}

func randomID() string {
	x := make([]byte, 12)
	rand.Read(x)
	return hex.EncodeToString(x)
}

// ReplySubject adds "Re: " unless the subject has it.
func ReplySubject(s string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "re:") {
		return s
	}
	return "Re: " + s
}
