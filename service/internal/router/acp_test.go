package router

import (
	"context"
	"testing"

	"heyrobots/service/internal/eval"
)

func TestACPRouterReadsCallsFromProse(t *testing.T) {
	r := &ACP{Prompt: "p", Ask: func(context.Context, string) (string, error) {
		return "Sure:\n```json\n[{\"tool\":\"route_accept\",\"trigger_ids\":[\"tr_1\"],\"scope\":\"Write a note\",\"toolsets\":[\"files\"],\"agent\":\"general\",\"note\":\"On it\"},{\"tool\":\"route_ignore\",\"trigger_ids\":[\"tr_bad\"],\"reason\":\"x\"}]\n```", nil
	}}
	routes, err := r.Route(context.Background(), eval.Candidate{Triggers: []eval.CandidateTrigger{{ID: "tr_1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Action != eval.ActionAccept || routes[0].Agent != "general" || routes[0].Scope != "Write a note" {
		t.Fatalf("routes = %+v; want one accept for general, and the unknown trigger dropped", routes)
	}
}
