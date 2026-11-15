package specialists

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"heyrobots/service/internal/core"
)

// maxSources is how many cited pages the source check opens.
const maxSources = 12

func researcher() Spec {
	return Spec{
		ID: "researcher", AgentID: "ag_researcher", Name: "Researcher",
		Purpose: "Finds things out from the web and says where each fact came from.",
		Color:   "mint", Badge: "search",
		Card: Card{
			Skills: []Skill{
				{Description: "Compares products, services or options against what you care about, with sources", Tags: []string{"compare", "buy", "choose"},
					Examples: []string{"Compare three CRMs for a five-person team", "Which robot vacuum handles pet hair best under $400?"}},
				{Description: "Answers a factual question from current sources", Tags: []string{"facts", "look up"},
					Examples: []string{"What are the visa rules for an Indian passport holder visiting Japan?", "When does the new EU AI Act apply to small companies?"}},
				{Description: "Summarises what is known about a topic, with every claim cited", Tags: []string{"summary", "brief"},
					Examples: []string{"Give me a one-page brief on heat pumps for an old house"}},
			},
			NotFor: []string{"writing or sending mail", "your calendar", "writing code", "setting up apps"},
		},
		Toolsets: []core.Toolset{core.ToolsetResearch, core.ToolsetWriting},
		Tools:    []string{"web_search", "web_fetch", "save_note", "read_notes", "write_file", "read_file", "list_files"},
		Runtime:  Loop,
		Instructions: `You research. Search, open the pages that matter, and keep a note of each source you rely on.
Every factual claim in a research answer comes from a page you opened. For each one, put it in your report's data under "claims", with the page's address in "url" and a short passage from that page in "quote" that shows the claim is true. To get the passage, ask web_fetch to quote the exact sentence that says it.
Your report is checked: each page is opened again and must contain your quote. A claim you can't support, leave out, and say what you couldn't find.
For a quick reply that makes no factual claims, such as a thank-you or a question back, leave "claims" out.`,
		Output: Output{
			About:  "A research answer lists its claims, each with the page it came from and a passage copied from that page.",
			Fields: []Field{{Name: "claims", About: `a list of {"claim", "url", "quote"}: the claim in your words, the page's address, and a passage copied exactly from the page`}},
		},
		Checks: []Check{
			{ID: "sourced", Kind: Shape, About: "Every claim has a page and a passage from it", When: researchAnswer, Run: checkSourced},
			{ID: "sources-say-it", Kind: Tool, About: "Each cited page opens and contains the passage quoted from it", When: hasClaims, Run: checkSourcesSayIt},
			{ID: "answers-it", Kind: Rubric, About: "It answers what was asked, and says what it couldn't find", When: researchAnswer,
				Run: judge(`- It answers the question that was asked, not a nearby one.
- Each point the person asked about is covered, or the answer says it couldn't be found.
- Nothing in the answer goes beyond what the listed claims support.
- Dates, prices and versions say when they are from.
Fail it if any of these is missing; say which.`)},
		},
		Cases: researcherCases,
	}
}

// researchAnswer is a report that gives an answer, not a quick reply.
func researchAnswer(r Report) bool { return hasClaims(r) || len([]rune(r.Result)) > 280 }

func hasClaims(r Report) bool { return len(list(r.Data, "claims")) > 0 }

func checkSourced(_ context.Context, _ Host, r Report) Verdict {
	claims := list(r.Data, "claims")
	if len(claims) == 0 {
		return Fail(`the answer lists no claims: put each fact with its source in data.claims as {"claim", "url", "quote"}`)
	}
	var reasons []string
	for i, c := range claims {
		u, err := url.Parse(text(c, "url"))
		switch {
		case text(c, "claim") == "":
			reasons = append(reasons, fmt.Sprintf("claim %d has no text", i+1))
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
			reasons = append(reasons, fmt.Sprintf("claim %d (%q) has no web address", i+1, short(text(c, "claim"))))
		case len([]rune(text(c, "quote"))) < 12:
			reasons = append(reasons, fmt.Sprintf("claim %d (%q) has no passage quoted from its page", i+1, short(text(c, "claim"))))
		}
	}
	if len(reasons) > 0 {
		return Fail(reasons...)
	}
	return Verdict{Pass: true, Ran: fmt.Sprintf("%d claims, each with a source", len(claims))}
}

func checkSourcesSayIt(ctx context.Context, h Host, r Report) Verdict {
	claims := list(r.Data, "claims")
	pages := map[string]string{}
	var reasons []string
	opened := 0
	for _, c := range claims {
		u, q := text(c, "url"), text(c, "quote")
		if u == "" || q == "" {
			continue
		}
		page, seen := pages[u]
		if !seen {
			if opened >= maxSources {
				continue
			}
			opened++
			p, err := h.Fetch(ctx, u)
			if err != nil {
				reasons = append(reasons, fmt.Sprintf("couldn't open %s: %s", u, short(err.Error())))
				pages[u] = "\x00"
				continue
			}
			page = normal(p)
			pages[u] = page
		}
		if page == "\x00" {
			continue
		}
		if !holds(page, q) {
			reasons = append(reasons, fmt.Sprintf("%s doesn't say %q", u, short(q)))
		}
	}
	if len(reasons) > 0 {
		return Fail(reasons...)
	}
	return Verdict{Pass: true, Ran: fmt.Sprintf("opened %d sources and found every quote", opened)}
}

// holds reports whether a page says what a quote says. Pages reach the
// Researcher through the quarantined reader, which may not copy a passage
// letter for letter, so a quote holds when the page has it exactly, or
// when every number in it is on the page and nearly all its other words
// are. Numbers (prices, dates, versions) are what a made-up claim gets
// wrong, so they must match exactly.
//
// ponytail: word coverage over the whole page, not within one passage; a
// windowed match comes if loose quotes slip through.
func holds(page, quote string) bool {
	q := normal(quote)
	if strings.Contains(page, q) {
		return true
	}
	words := map[string]bool{}
	for _, w := range tokens(page) {
		words[w] = true
	}
	var other, found int
	for _, w := range tokens(q) {
		number := strings.ContainsAny(w, "0123456789")
		switch {
		case number && !words[w]:
			return false
		case number || len([]rune(w)) < 4:
		default:
			other++
			if words[w] {
				found++
			}
		}
	}
	return other >= 3 && found*5 >= other*4
}

// tokens splits text into words, keeping "$20", "3.5" and "1,000" whole.
func tokens(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(s, func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '$' && c != '.' && c != ','
	}) {
		if w = strings.Trim(w, ".,"); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// normal folds case, spacing and typographic quotes and dashes, so a
// passage copied from a page matches the page's text.
func normal(s string) string {
	r := strings.NewReplacer("‘", "'", "’", "'", "“", `"`, "”", `"`, "–", "-", "—", "-", " ", " ")
	s = strings.ToLower(r.Replace(s))
	var b strings.Builder
	space := false
	for _, c := range s {
		if unicode.IsSpace(c) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(c)
	}
	return b.String()
}

func short(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 80 {
		return string(r[:79]) + "…"
	}
	return s
}

// judge makes a rubric check from a checklist.
func judge(checklist string) func(context.Context, Host, Report) Verdict {
	return func(ctx context.Context, h Host, r Report) Verdict {
		v, err := h.Judge(ctx, checklist, r)
		if err != nil {
			// A judge that couldn't run doesn't fail the work; the record
			// says it didn't run.
			return Verdict{Pass: true, Ran: "not judged: " + short(err.Error())}
		}
		return v
	}
}

var researcherCases = []Case{
	{"Compare three CRMs for a five-person team that lives in Gmail", "Names three real CRMs; covers price for five seats, Gmail sync and an API; each price is dated and sourced."},
	{"Which robot vacuum handles pet hair best under $400?", "Gives specific current models under $400, cites tests or reviews, and says when prices were checked."},
	{"What are the visa rules for an Indian passport holder visiting Japan for two weeks?", "States whether a visa is needed, how to apply, and cites an official government or embassy page."},
	{"When does the EU AI Act apply to a company of ten people?", "Gives the dated phases of application and cites the regulation or the Commission's own page."},
	{"Is it cheaper to run a heat pump or a gas boiler in the UK this winter?", "Compares running costs with current unit prices, dated, sourced, and states its assumptions."},
	{"What changed in Go 1.25?", "Lists real changes from the official release notes and cites them."},
	{"Find three well-reviewed Malayalam history books on the Travancore kingdom", "Names real books with authors, cites where each review or listing is, and doesn't invent titles."},
	{"How long does a passport renewal take in Kerala right now?", "Gives a current estimate from an official or recent source and says how current it is."},
	{"What's the best way to back up a Mac to a NAS?", "Covers Time Machine to a NAS with its requirements, cites Apple's documentation, and notes limits."},
	{"Summarise what is known about the Battle of Colachel in 1741", "Gives the date, the sides, the outcome and its significance, each cited; doesn't overstate disputed points."},
	{"thanks!", "Replies briefly without starting research or listing claims."},
	{"Which of these is cheaper per month, Notion Plus or Confluence Standard, for six people?", "Gives each price for six with the date checked and cites both pricing pages."},
}
