package api

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/specialists"
	"heyrobots/service/internal/store"
)

// specialistView shows what makes an agent a specialist: its card, its
// tools, its checks and its record, as the server enforces them.
func (s *Server) specialistView(a store.Agent, spec specialists.Spec) *gen.Specialist {
	v := &gen.Specialist{Id: spec.ID, NotFor: slices.Clone(spec.Card.NotFor), Tools: slices.Clone(spec.Tools),
		AppTools: spec.AppTools, Runtime: gen.SpecialistRuntime(spec.Runtime), Output: spec.Output.About}
	for _, sk := range spec.Card.Skills {
		v.Skills = append(v.Skills, struct {
			Description string   `json:"description"`
			Examples    []string `json:"examples"`
		}{sk.Description, slices.Clone(sk.Examples)})
	}
	for _, c := range spec.Checks {
		v.Checks = append(v.Checks, struct {
			About string                   `json:"about"`
			Id    string                   `json:"id"`
			Kind  gen.SpecialistChecksKind `json:"kind"`
		}{c.About, c.ID, gen.SpecialistChecksKind(c.Kind)})
	}
	o, _ := s.opts.Store.Outcomes(a.ID, s.opts.Now().Add(-30*24*time.Hour))
	v.Record = gen.SpecialistRecord{Checked: o.Checked, FirstTry: o.FirstTry, Failed: o.Failed, Tokens: o.Tokens, Line: recordLine(o)}
	return v
}

// recordLine says how the checked work went, failures as plainly as
// passes.
func recordLine(o store.OutcomeSummary) string {
	if o.Checked == 0 {
		return "No checked work yet"
	}
	parts := []string{fmt.Sprintf("%d of %d passed first time", o.FirstTry, o.Checked)}
	if o.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d didn't pass", o.Failed))
	}
	return strings.Join(parts, " · ")
}
