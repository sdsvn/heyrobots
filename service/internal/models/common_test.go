package models

import (
	"strings"
	"testing"
)

func TestTheWorkerSystemPromptCachesItsGlobalAndAgentBlocks(t *testing.T) {
	got, ok := systemBlocks("global rules\n\n# You are Coder\n\nfix things\n\n# Your task\n\nTask: build").([]map[string]any)
	text := func(i int) string { return strings.TrimSpace(got[i]["text"].(string)) }
	if !ok || len(got) != 3 || text(0) != "global rules" || text(1) != "# You are Coder\n\nfix things" || text(2) != "# Your task\n\nTask: build" {
		t.Fatalf("blocks = %v", got)
	}
	if got[0]["cache_control"] == nil || got[1]["cache_control"] == nil || got[2]["cache_control"] != nil {
		t.Fatalf("breakpoints = %v", got)
	}
	if s, ok := systemBlocks("just a router prompt").(string); !ok || s != "just a router prompt" {
		t.Fatalf("plain prompt = %v", s)
	}
}
