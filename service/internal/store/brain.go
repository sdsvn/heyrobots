package store

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"
)

// An agent's brain is what it keeps between tasks, per person it works
// for: facts it learned, how-tos about its job, a diary of what it did and
// follow-ups it owes. Person is "owner" or "delegate-<name>": what an agent
// learns on one person's tasks never reaches another's.

// Fact sources: the person said it, the agent saw it happen, or it was
// read in outside content such as a mail or a page.
const (
	FromPerson  = "person"
	FromSeen    = "seen"
	FromOutside = "outside"
)

// Fact kinds: a fact about the world or the person, or a how-to about
// doing the job, which every task of the agent sees.
const (
	KindFact  = "fact"
	KindHowTo = "howto"
)

// BrainFact is one thing an agent knows.
type BrainFact struct {
	ID        int64  `gorm:"primaryKey"`
	AgentID   string `gorm:"index:idx_brain_facts_owner"`
	Person    string `gorm:"index:idx_brain_facts_owner"`
	Kind      string
	Subject   string
	Text      string
	Source    string
	ThreadID  string
	CreatedAt time.Time
	UpdatedAt time.Time
	// OldAt marks a fact that was corrected or put away. It stays on the
	// agent's page so it can be brought back.
	OldAt *time.Time
}

// BrainDiary is one line of what an agent did.
type BrainDiary struct {
	ID       int64  `gorm:"primaryKey"`
	AgentID  string `gorm:"index:idx_brain_diary_owner"`
	Person   string `gorm:"index:idx_brain_diary_owner"`
	TicketID string
	ThreadID string
	Text     string
	// Outcome is what became of the task's proposals, such as "Sent Thu
	// 12 Nov" or the owner's reason for declining; a later line for the
	// same task keeps it.
	Outcome   string
	CreatedAt time.Time
	// OldAt marks lines put away.
	OldAt *time.Time
}

// Follow-up states.
const (
	FollowUpOpen      = "open"
	FollowUpFired     = "fired"
	FollowUpDone      = "done"
	FollowUpCancelled = "cancelled"
)

// Follow-up kinds: one an agent set, or a check for a reply to a sent
// email, which the server sets when the outbox sends it.
const (
	FollowUpTask  = "task"
	FollowUpReply = "reply"
)

// BrainFollowUp is something an agent must do later.
type BrainFollowUp struct {
	ID      string `gorm:"primaryKey"`
	AgentID string `gorm:"index"`
	Person  string
	Kind    string
	// Mandate is the creating task's, as JSON: the follow-up's task runs
	// with it and never with more.
	Mandate string
	// Tainted is set when the creating task had read outside content.
	Tainted  bool
	ThreadID string
	TicketID string
	What     string
	Why      string
	// Check is what to look at first; when it shows the follow-up is no
	// longer needed, the task closes it instead of acting.
	Check string
	// Key names what the follow-up is about, such as a mail thread, so a
	// second follow-up for the same thing updates the first.
	Key string
	// Ref is the sent mail's message id, for a reply check.
	Ref string
	// Nudges counts the nudges already sent for a reply check's key.
	Nudges    int
	DueAt     time.Time `gorm:"index"`
	State     string    `gorm:"index"`
	Outcome   string
	CreatedAt time.Time
	UpdatedAt time.Time
	FiredAt   *time.Time
}

// stopWords are left out of a search: they match everything.
var stopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an and any anything are as at be but by can could did do does done for from get got had has have he her his how i if in into is it its just know let like make me more my need new no not now of on or our out please she so some than that the their them then there these they this to today tomorrow up us use want was we were what when where which who why will with would yet you your yes ok okay thanks thank hi hey hello email emails mail send sent message reply write draft check find tell ask`) {
		stopWords[w] = true
	}
}

// matchQuery turns free text into an FTS query that matches any of its
// words, so a message finds what shares its names and topics. It returns
// "" when nothing is worth searching for.
func matchQuery(text string) string {
	seen := map[string]bool{}
	var terms []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) < 3 || stopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		terms = append(terms, `"`+w+`"`)
		if len(terms) == 40 {
			break
		}
	}
	return strings.Join(terms, " OR ")
}

// AddFact stores a fact. A fact with the same text for the same agent and
// person isn't stored twice; the old one is brought back and touched.
func (tx *Tx) AddFact(f BrainFact) (BrainFact, error) {
	var same BrainFact
	err := tx.db.Where("agent_id = ? AND person = ? AND lower(text) = lower(?)", f.AgentID, f.Person, f.Text).First(&same).Error
	if err == nil {
		if f.Source == FromOutside && same.OldAt != nil {
			// Outside content can't bring back what was corrected.
			return same, nil
		}
		same.OldAt, same.UpdatedAt = nil, f.CreatedAt
		if rank(f.Source) > rank(same.Source) {
			same.Source = f.Source
		}
		return same, tx.db.Save(&same).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return BrainFact{}, err
	}
	f.UpdatedAt = f.CreatedAt
	return f, tx.db.Create(&f).Error
}

// Fact finds one of an agent's facts for a person.
func (tx *Tx) Fact(agentID, person string, id int64) (BrainFact, error) {
	var f BrainFact
	err := tx.db.Where("agent_id = ? AND person = ? AND id = ?", agentID, person, id).First(&f).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return f, ErrNotFound
	}
	return f, err
}

// SetFactOld puts a fact away, or brings it back when old is nil.
func (tx *Tx) SetFactOld(id int64, old *time.Time) error {
	return tx.db.Model(&BrainFact{}).Where("id = ?", id).Update("old_at", old).Error
}

// DeleteFact removes a fact for good, as when a person says to forget it.
func (tx *Tx) DeleteFact(agentID string, id int64) error {
	return tx.db.Where("agent_id = ? AND id = ?", agentID, id).Delete(&BrainFact{}).Error
}

// HowTos are an agent's how-tos for a person, which every task sees.
func (tx *Tx) HowTos(agentID, person string, limit int) ([]BrainFact, error) {
	var out []BrainFact
	err := tx.db.Where("agent_id = ? AND person = ? AND kind = ? AND old_at IS NULL", agentID, person, KindHowTo).
		Order("updated_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

// SearchFacts finds an agent's current facts for a person that share
// words with text, best first.
func (tx *Tx) SearchFacts(agentID, person, text string, limit int) ([]BrainFact, error) {
	q := matchQuery(text)
	if q == "" {
		return nil, nil
	}
	var out []BrainFact
	err := tx.db.Raw(`SELECT f.* FROM brain_facts_fts JOIN brain_facts f ON f.id = brain_facts_fts.rowid
		WHERE brain_facts_fts MATCH ? AND f.agent_id = ? AND f.person = ? AND f.kind = ? AND f.old_at IS NULL
		ORDER BY bm25(brain_facts_fts, 2.0, 1.0) LIMIT ?`, q, agentID, person, KindFact, limit).Scan(&out).Error
	return out, err
}

// Facts lists an agent's facts, newest first, for its page. With person
// empty it lists every person's.
func (tx *Tx) Facts(agentID, person string, withOld bool, limit int) ([]BrainFact, error) {
	q := tx.db.Where("agent_id = ?", agentID)
	if person != "" {
		q = q.Where("person = ?", person)
	}
	if !withOld {
		q = q.Where("old_at IS NULL")
	}
	var out []BrainFact
	return out, q.Order("updated_at DESC").Limit(limit).Find(&out).Error
}

// AddDiary writes a diary line.
func (tx *Tx) AddDiary(d BrainDiary) error { return tx.db.Create(&d).Error }

// SearchDiary finds an agent's diary lines for a person that share words
// with text, best first.
func (tx *Tx) SearchDiary(agentID, person, text string, limit int) ([]BrainDiary, error) {
	q := matchQuery(text)
	if q == "" {
		return nil, nil
	}
	var out []BrainDiary
	err := tx.db.Raw(`SELECT d.* FROM brain_diary_fts JOIN brain_diaries d ON d.id = brain_diary_fts.rowid
		WHERE brain_diary_fts MATCH ? AND d.agent_id = ? AND d.person = ? AND d.old_at IS NULL
		ORDER BY bm25(brain_diary_fts) LIMIT ?`, q, agentID, person, limit).Scan(&out).Error
	return out, err
}

// Diary lists an agent's diary lines, newest first. With person empty it
// lists every person's; with threadID set, only that chat's.
func (tx *Tx) Diary(agentID, person, threadID string, since time.Time, limit int) ([]BrainDiary, error) {
	q := tx.db.Where("agent_id = ? AND old_at IS NULL AND created_at >= ?", agentID, since)
	if person != "" {
		q = q.Where("person = ?", person)
	}
	if threadID != "" {
		q = q.Where("thread_id = ?", threadID)
	}
	var out []BrainDiary
	return out, q.Order("created_at DESC").Limit(limit).Find(&out).Error
}

// SetDiaryOld marks diary lines as folded into a summary.
func (tx *Tx) SetDiaryOld(ids []int64, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	return tx.db.Model(&BrainDiary{}).Where("id IN ?", ids).Update("old_at", at).Error
}

// SaveFollowUp creates or updates a follow-up. A new open follow-up with
// the same agent, person and key as an open one replaces that one's
// details instead of adding a second.
func (tx *Tx) SaveFollowUp(f BrainFollowUp) (BrainFollowUp, bool, error) {
	if f.Key != "" {
		var same BrainFollowUp
		err := tx.db.Where("agent_id = ? AND person = ? AND key = ? AND state IN ?", f.AgentID, f.Person, f.Key, []string{FollowUpOpen, FollowUpFired}).First(&same).Error
		if err == nil {
			same.What, same.Why, same.Check, same.DueAt, same.Ref, same.Nudges = f.What, f.Why, f.Check, f.DueAt, f.Ref, f.Nudges
			same.State, same.UpdatedAt = FollowUpOpen, f.CreatedAt
			if f.ThreadID != "" {
				same.ThreadID = f.ThreadID
			}
			return same, false, tx.db.Save(&same).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return f, false, err
		}
	}
	f.UpdatedAt = f.CreatedAt
	return f, true, tx.db.Create(&f).Error
}

// FollowUp finds a follow-up by id.
func (tx *Tx) FollowUp(id string) (BrainFollowUp, error) {
	var f BrainFollowUp
	err := tx.db.First(&f, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return f, ErrNotFound
	}
	return f, err
}

// UpdateFollowUp saves a changed follow-up.
func (tx *Tx) UpdateFollowUp(f BrainFollowUp) error { return tx.db.Save(&f).Error }

// FollowUps lists follow-ups in the given states, soonest first. With
// agentID empty it lists every agent's; with person set, only theirs.
func (tx *Tx) FollowUps(agentID, person string, states []string, limit int) ([]BrainFollowUp, error) {
	q := tx.db.Where("state IN ?", states)
	if agentID != "" {
		q = q.Where("agent_id = ?", agentID)
	}
	if person != "" {
		q = q.Where("person = ?", person)
	}
	var out []BrainFollowUp
	return out, q.Order("due_at").Limit(limit).Find(&out).Error
}

// DueFollowUps are open follow-ups due at or before now, soonest first.
func (tx *Tx) DueFollowUps(now time.Time, limit int) ([]BrainFollowUp, error) {
	var out []BrainFollowUp
	err := tx.db.Where("state = ? AND due_at <= ?", FollowUpOpen, now).
		Where("NOT EXISTS (SELECT 1 FROM agents a WHERE a.id = brain_follow_ups.agent_id AND (NOT a.enabled OR a.follow_ups_off))").
		Where(`NOT EXISTS (SELECT 1 FROM tickets t JOIN subscriptions s ON s.ticket_id = t.id
			WHERE s.thread_id = brain_follow_ups.thread_id AND t.open AND t.stage = 'working')`).
		Order("due_at").Limit(limit).Find(&out).Error
	return out, err
}

// ForgetPerson removes everything every agent kept for a person.
func (tx *Tx) ForgetPerson(person string) error {
	for _, m := range []any{&BrainFact{}, &BrainDiary{}, &BrainFollowUp{}, &Incident{}} {
		if err := tx.db.Where("person = ?", person).Delete(m).Error; err != nil {
			return err
		}
	}
	return nil
}

// FactHit is a fact a search found, with its full-text rank: lower is a
// better match.
type FactHit struct {
	BrainFact `gorm:"embedded"`
	Rank      float64
}

// FactsAbout finds an agent's current facts for a person whose subject or
// text names one of the keys, such as an email address.
func (tx *Tx) FactsAbout(agentID, person string, keys []string, limit int) ([]BrainFact, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	q := tx.db.Where("agent_id = ? AND person = ? AND kind = ? AND old_at IS NULL", agentID, person, KindFact)
	or := tx.db
	for i, k := range keys {
		like := "%" + strings.ToLower(k) + "%"
		if i == 0 {
			or = or.Where("lower(subject) LIKE ? OR lower(text) LIKE ?", like, like)
		} else {
			or = or.Or("lower(subject) LIKE ? OR lower(text) LIKE ?", like, like)
		}
	}
	var out []BrainFact
	return out, q.Where(or).Order("updated_at DESC").Limit(limit).Find(&out).Error
}

// RankedFacts is SearchFacts with each hit's rank.
func (tx *Tx) RankedFacts(agentID, person, text string, limit int) ([]FactHit, error) {
	q := matchQuery(text)
	if q == "" {
		return nil, nil
	}
	var out []FactHit
	err := tx.db.Raw(`SELECT f.*, bm25(brain_facts_fts, 2.0, 1.0) AS rank FROM brain_facts_fts JOIN brain_facts f ON f.id = brain_facts_fts.rowid
		WHERE brain_facts_fts MATCH ? AND f.agent_id = ? AND f.person = ? AND f.kind = ? AND f.old_at IS NULL
		ORDER BY rank LIMIT ?`, q, agentID, person, KindFact, limit).Scan(&out).Error
	return out, err
}

// PinnedFacts are an agent's how-tos and the facts the person told it,
// newest first: every task for that person sees them.
func (tx *Tx) PinnedFacts(agentID, person string, limit int) ([]BrainFact, error) {
	var out []BrainFact
	err := tx.db.Where("agent_id = ? AND person = ? AND old_at IS NULL AND (kind = ? OR source = ?)", agentID, person, KindHowTo, FromPerson).
		Order("kind DESC, updated_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

// WriteDiary keeps one diary line per Ticket: a later Assignment of the
// same Ticket replaces its line.
func (tx *Tx) WriteDiary(d BrainDiary) error {
	var cur BrainDiary
	err := tx.db.Where("ticket_id = ? AND agent_id = ?", d.TicketID, d.AgentID).First(&cur).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || d.TicketID == "" {
		return tx.db.Create(&d).Error
	}
	if err != nil {
		return err
	}
	cur.Text, cur.CreatedAt = d.Text, d.CreatedAt
	if cur.Outcome == "" {
		cur.Outcome = d.Outcome
	}
	return tx.db.Save(&cur).Error
}

// rank orders fact sources by trust.
func rank(source string) int {
	switch source {
	case FromPerson:
		return 2
	case FromSeen:
		return 1
	}
	return 0
}

// NoteDiaryOutcome adds what became of a Ticket's proposed action to its
// diary line, such as "sent 14 Nov".
func (tx *Tx) NoteDiaryOutcome(ticketID, note string) error {
	if ticketID == "" {
		return nil
	}
	var lines []BrainDiary
	if err := tx.db.Where("ticket_id = ?", ticketID).Find(&lines).Error; err != nil {
		return err
	}
	if len(lines) == 0 {
		// The task hasn't finished yet: start its line with the outcome.
		t, err := tx.Ticket(ticketID)
		if err != nil || t.AgentID == "" {
			return nil
		}
		m, _ := t.DecodeMandate()
		return tx.db.Create(&BrainDiary{AgentID: t.AgentID, Person: m.Person(), TicketID: ticketID, ThreadID: t.OriginThreadID,
			Text: shortText(m.Scope, 200), Outcome: note, CreatedAt: time.Now().UTC()}).Error
	}
	for _, d := range lines {
		d.Outcome = strings.TrimSpace(strings.ReplaceAll(d.Outcome, AwaitingOutcome, ""))
		if d.Outcome != "" {
			d.Outcome += " "
		}
		d.Outcome += strings.TrimRight(note, ". ") + "."
		if err := tx.db.Save(&d).Error; err != nil {
			return err
		}
	}
	return nil
}

// LastFollowUpFor finds the newest follow-up with a key, in any state.
func (tx *Tx) LastFollowUpFor(agentID, person, key string) (BrainFollowUp, error) {
	var f BrainFollowUp
	err := tx.db.Where("agent_id = ? AND person = ? AND key = ?", agentID, person, key).Order("created_at DESC").First(&f).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return f, ErrNotFound
	}
	return f, err
}

// ThreadBusy reports whether a task is at work on a Thread now.
func (tx *Tx) ThreadBusy(threadID string) (bool, error) {
	var n int64
	err := tx.db.Raw(`SELECT COUNT(*) FROM tickets t JOIN subscriptions s ON s.ticket_id = t.id
		WHERE s.thread_id = ? AND t.open AND (t.stage = 'working' OR EXISTS (SELECT 1 FROM assignments a WHERE a.ticket_id = t.id AND a.status = ?))`,
		threadID, AssignmentRunning).Scan(&n).Error
	return n > 0, err
}

// FollowUpDelivery starts the delivery id of a fired follow-up's Trigger:
// followup:<id>@<due>, so each due time fires once.
const FollowUpDelivery = "followup:"

// TicketAwaiting counts a Ticket's actions waiting for the owner's OK.
func (tx *Tx) TicketAwaiting(ticketID string) (int64, error) {
	var n int64
	err := tx.db.Model(&Effect{}).Where("ticket_id = ? AND status = ?", ticketID, EffectAwaitingApproval).Count(&n).Error
	return n, err
}

// HasDelivery reports whether a Trigger with this delivery id exists.
func (tx *Tx) HasDelivery(source, deliveryID string) (bool, error) {
	var n int64
	err := tx.db.Model(&Trigger{}).Where("source = ? AND delivery_id = ?", source, deliveryID).Count(&n).Error
	return n > 0, err
}

// AwaitingOutcome is a diary line's outcome while its proposals wait for
// the owner; what happens to them replaces it.
const AwaitingOutcome = "Waiting for the owner's OK."

func shortText(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}
