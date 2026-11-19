package agentfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"heyrobots/service/internal/store"
)

func TestSyncProfileAndHistory(t *testing.T) {
	root := t.TempDir()
	a := store.Agent{ID: "ag_test", Name: "Researcher", Purpose: "Finds things out.", Instructions: "Cite sources.", Toolsets: `["research"]`, Skills: `["web"]`}
	if err := SyncProfile(root, a); err != nil {
		t.Fatal(err)
	}
	profile, err := os.ReadFile(filepath.Join(root, "agents", a.ID, "AGENT.md"))
	if err != nil || !strings.Contains(string(profile), "Finds things out.") || !strings.Contains(string(profile), "Cite sources.") {
		t.Fatalf("profile = %q, err = %v", profile, err)
	}

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	v := store.ThreadView{
		Thread:   store.Thread{ID: "th_test"},
		Triggers: []store.Trigger{{ActorID: "owner", Text: "Find three sources", At: now}},
		Effects:  []store.Effect{{Kind: "message", Body: "I found three sources.", CreatedAt: now.Add(time.Minute)}},
	}
	as := store.Assignment{ID: "as_test", TicketID: "tk_test", StartedAt: now}
	if err := SyncHistory(root, a, v, as, "ready", "Done", "The result", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	conversation, err := os.ReadFile(filepath.Join(root, "agents", a.ID, "conversations", "th_test.md"))
	if err != nil || !strings.Contains(string(conversation), "Find three sources") || !strings.Contains(string(conversation), "three sources") {
		t.Fatalf("conversation = %q, err = %v", conversation, err)
	}
	session, err := os.ReadFile(filepath.Join(root, "agents", a.ID, "sessions", "as_test.md"))
	if err != nil || !strings.Contains(string(session), "ready") || !strings.Contains(string(session), "The result") {
		t.Fatalf("session = %q, err = %v", session, err)
	}
}
