package acp

import (
	"context"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestAQuickCallGetsOnlyTheToolsItWasGiven(t *testing.T) {
	ctx := context.Background()
	call := func(c *textClient, id, name string) {
		t.Helper()
		u := &acp.SessionUpdateToolCall{ToolCallId: acp.ToolCallId(id), Title: name}
		if name != "" {
			u.Meta = map[string]any{"claudeCode": map[string]any{"toolName": name}}
		}
		if err := c.SessionUpdate(ctx, acp.SessionNotification{Update: acp.SessionUpdate{ToolCall: u}}); err != nil {
			t.Fatal(err)
		}
	}
	allowed := func(c *textClient, id string) bool {
		t.Helper()
		resp, err := c.RequestPermission(ctx, acp.RequestPermissionRequest{
			ToolCall: acp.ToolCallUpdate{ToolCallId: acp.ToolCallId(id)},
			Options: []acp.PermissionOption{
				{OptionId: "yes", Kind: acp.PermissionOptionKindAllowOnce},
				{OptionId: "no", Kind: acp.PermissionOptionKindRejectOnce},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return resp.Outcome.Selected != nil && resp.Outcome.Selected.OptionId == "yes"
	}

	search := &textClient{allow: []string{"WebSearch"}}
	call(search, "t1", "WebSearch")
	if !allowed(search, "t1") || search.Stray() != "" {
		t.Fatal("a search may use web search")
	}
	if allowed(search, "unknown") {
		t.Fatal("a call the client never saw named is refused")
	}
	call(search, "t2", "WebFetch")
	if allowed(search, "t2") || search.Stray() != "WebFetch" {
		t.Fatalf("a search may not fetch pages: stray %q", search.Stray())
	}

	read := &textClient{allow: []string{}}
	call(read, "t3", "")
	if allowed(read, "t3") || read.Stray() == "" {
		t.Fatal("a read uses no tools, even one the adapter can't name")
	}
}
