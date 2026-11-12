package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/core"
	"heyrobots/service/internal/outbox"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/taint"
	"heyrobots/service/internal/telemetry"
	"heyrobots/service/internal/usage"
)

// effectID turns an API id such as ef_12 into an Effect id.
func effectID(s string) (uint, bool) {
	n, err := strconv.ParseUint(strings.TrimPrefix(s, "ef_"), 10, 32)
	return uint(n), err == nil && strings.HasPrefix(s, "ef_")
}

func apiEffectID(id uint) string { return "ef_" + strconv.FormatUint(uint64(id), 10) }

var errNeedsVerification = errors.New("approving an action that reaches other people or can't be undone needs Face ID, Touch ID or a fingerprint")

// ListApprovals lists actions waiting for the owner. Owner devices only.
func (s *Server) ListApprovals(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	out := gen.ApprovalList{Approvals: []gen.Approval{}}
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		es, err := tx.AwaitingApproval()
		if err != nil {
			return err
		}
		for _, e := range es {
			out.Approvals = append(out.Approvals, approvalView(tx, e))
		}
		return nil
	})
	if err != nil {
		s.internal(w, "list approvals", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// DecideApproval approves or rejects a waiting action. Owner devices only:
// actions run on the owner's accounts, so a delegate can never approve,
// even an action they asked for.
func (s *Server) DecideApproval(w http.ResponseWriter, r *http.Request, approvalID string) {
	d := deviceFrom(r)
	if !policy.CanApprove(policy.DeviceRole(d.Role)) {
		writeError(w, http.StatusForbidden, "only the owner's devices can approve actions")
		return
	}
	id, ok := effectID(approvalID)
	if !ok {
		writeError(w, http.StatusNotFound, "no such action")
		return
	}
	var req gen.ApprovalDecision
	if !decode(w, r, &req) {
		return
	}
	if !req.Decision.Valid() {
		writeError(w, http.StatusBadRequest, "decision must be approve or reject")
		return
	}
	verified := req.UserVerified != nil && *req.UserVerified
	note := ""
	if req.Note != nil {
		note = *req.Note
	}
	var view gen.Approval
	var threadID string
	var ticketID string
	var copied store.Effect
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		if e, err := tx.Effect(id); err == nil && e.Origin != "" {
			// Another machine holds this action: pass the answer on.
			copied, view = e, approvalView(tx, e)
			return nil
		}
		if req.Decision == gen.Approve && !verified {
			e, err := tx.Effect(id)
			if err != nil {
				return err
			}
			// The server can't check a face or fingerprint itself. It holds
			// the app to asking, and records that it did.
			if r := core.Reach(e.Reach); r == core.ReachOthers || r == core.ReachIrreversible {
				return errNeedsVerification
			}
		}
		e, err := outbox.Decide(tx, outbox.Decision{EffectID: id, Approve: req.Decision == gen.Approve, DeviceID: d.ID, Note: note, UserVerified: verified, At: s.opts.Now()})
		if err != nil {
			return err
		}
		threadID, view, ticketID = e.ThreadID, approvalView(tx, e), e.TicketID
		return nil
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such action")
		return
	case errors.Is(err, errNeedsVerification):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case errors.Is(err, outbox.ErrNotAwaiting):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		s.internal(w, "decide approval", err)
		return
	}
	if copied.Origin != "" {
		if s.opts.Cluster == nil {
			writeError(w, http.StatusConflict, "this action belongs to another machine")
			return
		}
		if err := s.opts.Cluster.ForwardApproval(copied, req.Decision == gen.Approve, note, verified); err != nil {
			s.internal(w, "forward approval", err)
			return
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	s.opts.Log.Info("action decided", "effect", id, "decision", req.Decision, "device", d.ID)
	if ticketID != "" {
		_, span := telemetry.Tracer().Start(telemetry.TaskContext(r.Context(), ticketID), "heyrobots.approval", trace.WithAttributes(
			telemetry.ObservationType.String("event"), telemetry.EffectID.Int64(int64(id)), telemetry.TaskID.String(ticketID), telemetry.Outcome.String(string(req.Decision)),
			attribute.Bool("heyrobots.user_verified", verified), attribute.String("heyrobots.device.id", d.ID),
		))
		span.End()
	}
	if s.opts.OnDecided != nil {
		s.opts.OnDecided()
	}
	s.ThreadUpdated(threadID)
	s.opts.Broker.Publish(Event{Type: "approvals.changed", Data: map[string]string{"id": approvalID}, Audience: policy.AppOwner.String()})
	writeJSON(w, http.StatusOK, view)
}

func approvalView(tx *store.Tx, e store.Effect) gen.Approval {
	v := gen.Approval{
		Id: apiEffectID(e.ID), ThreadId: e.ThreadID, Source: e.Source, Action: e.Action, Reach: e.Reach,
		Body: e.Body, Status: e.Status, CreatedAt: e.CreatedAt, Findings: []string{},
	}
	if e.Approval != "" {
		v.Approval = &e.Approval
	}
	var d taint.Details
	if json.Unmarshal([]byte(e.Details), &d) == nil {
		v.Recipients, v.Links, v.Dates, v.Amounts = nonEmpty(d.Recipients), nonEmpty(d.Links), nonEmpty(d.Dates), nonEmpty(d.Amounts)
	}
	var payload struct {
		Subject string `json:"subject"`
	}
	if json.Unmarshal([]byte(e.Payload), &payload) == nil && payload.Subject != "" {
		v.Subject = &payload.Subject
	}
	var findings []taint.Finding
	if json.Unmarshal([]byte(e.Findings), &findings) == nil {
		for _, f := range findings {
			v.Findings = append(v.Findings, f.Kind+" "+f.Value+": "+f.Why)
		}
	}
	if e.TicketID != "" {
		v.TicketId = &e.TicketID
		if tk, err := tx.Ticket(e.TicketID); err == nil {
			if m, err := tk.DecodeMandate(); err == nil {
				scope, by := m.Scope, "the owner"
				switch {
				case m.Strength == core.Inferred:
					by = "a proactive rule"
				case m.Role == core.RoleDelegate:
					by = "delegate " + m.Delegate
				}
				v.Scope, v.RequestedBy = &scope, &by
			}
		}
	}
	return v
}

func nonEmpty(s []string) *[]string {
	if len(s) == 0 {
		return nil
	}
	return &s
}

// ListStandingApprovals lists standing approvals. Owner devices only.
func (s *Server) ListStandingApprovals(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	p, err := s.opts.Store.Policy()
	if err != nil {
		s.internal(w, "load policy", err)
		return
	}
	writeJSON(w, http.StatusOK, standingView(p))
}

// CreateStandingApproval adds a standing approval. Owner devices only.
func (s *Server) CreateStandingApproval(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	var req gen.StandingApproval
	if !decode(w, r, &req) {
		return
	}
	sa := policy.StandingApproval{Name: strings.TrimSpace(req.Name), Source: core.SourceKind(req.Source), Action: strings.TrimSpace(req.Action)}
	for _, p := range req.People {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(sa.People, p) {
			sa.People = append(sa.People, p)
		}
	}
	switch {
	case sa.Name == "" || len(sa.Name) > 80:
		writeError(w, http.StatusBadRequest, "a standing approval needs a name of up to 80 characters")
		return
	case !slices.Contains(core.KnownSources, sa.Source) || sa.Source == core.SourceApp:
		writeError(w, http.StatusBadRequest, "unknown source")
		return
	case sa.Action == "" || len(sa.Action) > 60:
		writeError(w, http.StatusBadRequest, "a standing approval needs an action")
		return
	case len(sa.People) == 0 || len(sa.People) > 50:
		writeError(w, http.StatusBadRequest, "a standing approval names between 1 and 50 people")
		return
	}
	s.changeStanding(w, http.StatusCreated, func(p *policy.Policy) (int, string) {
		if slices.ContainsFunc(p.StandingApprovals, func(x policy.StandingApproval) bool { return x.Name == sa.Name }) {
			return http.StatusConflict, "a standing approval with that name exists"
		}
		p.StandingApprovals = append(p.StandingApprovals, sa)
		return 0, ""
	})
}

// DeleteStandingApproval removes a standing approval. Owner devices only.
// Actions it let through that haven't been sent yet go back for approval.
func (s *Server) DeleteStandingApproval(w http.ResponseWriter, r *http.Request, name string) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	s.changeStanding(w, http.StatusNoContent, func(p *policy.Policy) (int, string) {
		i := slices.IndexFunc(p.StandingApprovals, func(x policy.StandingApproval) bool { return x.Name == name })
		if i < 0 {
			return http.StatusNotFound, "no such standing approval"
		}
		p.StandingApprovals = slices.Delete(p.StandingApprovals, i, i+1)
		return 0, ""
	})
}

func (s *Server) changeStanding(w http.ResponseWriter, okStatus int, change func(p *policy.Policy) (int, string)) {
	var saved policy.Policy
	var status int
	var msg string
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		p, err := tx.Policy()
		if err != nil {
			return err
		}
		if status, msg = change(&p); status != 0 {
			return nil
		}
		saved, err = tx.SavePolicy(p, s.opts.Now().UTC())
		return err
	})
	if status != 0 {
		writeError(w, status, msg)
		return
	}
	if err != nil {
		s.internal(w, "save policy", err)
		return
	}
	s.opts.Log.Info("standing approvals changed", "version", saved.Version)
	s.opts.Broker.Publish(Event{Type: "policy.updated", Data: map[string]int{"version": saved.Version}, Audience: policy.AppOwner.String()})
	if s.opts.OnDecided != nil {
		s.opts.OnDecided()
	}
	if okStatus == http.StatusNoContent {
		w.WriteHeader(okStatus)
		return
	}
	writeJSON(w, okStatus, standingView(saved))
}

func standingView(p policy.Policy) gen.StandingApprovalList {
	out := gen.StandingApprovalList{Version: p.Version, StandingApprovals: []gen.StandingApproval{}}
	for _, sa := range p.StandingApprovals {
		out.StandingApprovals = append(out.StandingApprovals, gen.StandingApproval{Name: sa.Name, Source: string(sa.Source), Action: sa.Action, People: sa.People})
	}
	return out
}

// ListDigests lists daily digests. Owner devices only.
func (s *Server) ListDigests(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	out := gen.DigestList{Digests: []gen.Digest{}}
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		ds, err := tx.Digests(30)
		for _, d := range ds {
			out.Digests = append(out.Digests, gen.Digest{Id: d.ID, Body: d.Body, Count: d.Count, CreatedAt: d.CreatedAt})
		}
		return err
	})
	if err != nil {
		s.internal(w, "list digests", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// DigestReady tells the owner's devices a digest is ready.
func (s *Server) DigestReady(d store.Digest) {
	s.opts.Broker.Publish(Event{Type: "digest.ready", Data: map[string]string{"id": d.ID}, Audience: policy.AppOwner.String()})
}

// GetUsage returns this month's model spending. Owner devices only.
func (s *Server) GetUsage(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	if s.opts.Usage == nil {
		writeError(w, http.StatusNotFound, "usage isn't recorded on this server")
		return
	}
	st, err := s.opts.Usage.Month()
	if err != nil {
		s.internal(w, "read usage", err)
		return
	}
	out := gen.UsageView{SpentUsd: st.SpentUSD, BudgetUsd: st.BudgetUSD, Since: st.Since, Tickets: []gen.TicketSpend{}}
	spend, err := s.opts.Store.TicketSpend(st.Since.UTC(), nil, 50)
	if err != nil {
		s.internal(w, "read usage", err)
		return
	}
	_ = s.opts.Store.Tx(func(tx *store.Tx) error {
		for id, micros := range spend {
			scope := ""
			if tk, err := tx.Ticket(id); err == nil {
				if m, err := tk.DecodeMandate(); err == nil {
					scope = m.Scope
				}
			}
			out.Tickets = append(out.Tickets, gen.TicketSpend{TicketId: id, Scope: scope, SpentUsd: float64(micros) / 1e6})
		}
		return nil
	})
	slices.SortFunc(out.Tickets, func(a, b gen.TicketSpend) int {
		switch {
		case a.SpentUsd > b.SpentUsd:
			return -1
		case a.SpentUsd < b.SpentUsd:
			return 1
		}
		return strings.Compare(a.TicketId, b.TicketId)
	})
	writeJSON(w, http.StatusOK, out)
}

// UsageReader is what the API needs from the usage ledger.
type UsageReader interface {
	Month() (usage.Status, error)
}

// limitsFrom applies an update to the current limits.
func limitsFrom(cur policy.Limits, in *gen.Limits) (policy.Limits, string) {
	if in == nil {
		return cur, ""
	}
	l := cur
	if in.MonthlyBudgetUsd != nil {
		if *in.MonthlyBudgetUsd < 0.01 || *in.MonthlyBudgetUsd > 100000 {
			return l, "the monthly budget must be between $0.01 and $100,000"
		}
		l.MonthlyBudgetUSD = *in.MonthlyBudgetUsd
	}
	if in.SendsPerHour != nil {
		if *in.SendsPerHour < 1 || *in.SendsPerHour > 1000 {
			return l, "sends per hour must be between 1 and 1000"
		}
		l.SendsPerHour = *in.SendsPerHour
	}
	if in.SendsPerDay != nil {
		if *in.SendsPerDay < 1 || *in.SendsPerDay > 10000 {
			return l, "sends per day must be between 1 and 10000"
		}
		l.SendsPerDay = *in.SendsPerDay
	}
	if in.DigestHour != nil {
		if *in.DigestHour < 0 || *in.DigestHour > 23 {
			return l, "the digest hour must be between 0 and 23"
		}
		l.DigestHour, l.DigestHourSet = *in.DigestHour, true
	}
	if in.Timezone != nil {
		tz := strings.TrimSpace(*in.Timezone)
		if tz != "" {
			if _, err := time.LoadLocation(tz); err != nil {
				return l, "unknown time zone " + tz
			}
		}
		l.Timezone = tz
	}
	return l, ""
}

func limitsView(l policy.Limits) *gen.Limits {
	l = l.Effective()
	tz := l.Timezone
	if tz == "" {
		tz = time.Local.String()
	}
	return &gen.Limits{MonthlyBudgetUsd: &l.MonthlyBudgetUSD, SendsPerHour: &l.SendsPerHour, SendsPerDay: &l.SendsPerDay, DigestHour: &l.DigestHour, Timezone: &tz}
}
