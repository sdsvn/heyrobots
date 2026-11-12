package acp

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestRoutineProtocolMessagesStayOutOfTheLog(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(quiet{slog.NewTextHandler(&buf, nil)})
	log.Error("failed to handle notification", "method", "auth/status_update", "err", errors.New(`{"code":-32601,"message":"Method not found"}`))
	log.Info("connection closed", "cause", "read |0: file already closed")
	log.Error("failed to handle notification", "err", errors.New("boom"))
	out := buf.String()
	if strings.Contains(out, "auth/status_update") || strings.Contains(out, "connection closed") {
		t.Fatalf("routine messages logged:\n%s", out)
	}
	if !strings.Contains(out, "boom") {
		t.Fatalf("a real error was dropped:\n%s", out)
	}
}

func TestTheAgentModelIsTheOnlyAnthropicSettingPassedOn(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "stray")
	t.Setenv("ANTHROPIC_MODEL", "from-the-shell")
	env := (&Worker{Model: "claude-sonnet-5-5"}).env()
	var got []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_") {
			got = append(got, kv)
		}
	}
	if len(got) != 1 || got[0] != "ANTHROPIC_MODEL=claude-sonnet-5-5" {
		t.Fatalf("passed on %v", got)
	}
}
