package acp

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

// TestLiveCheck runs the real adapter against the owner's Claude Code.
// It only runs with HEYROBOTS_LIVE_ACP=1.
func TestLiveCheck(t *testing.T) {
	if os.Getenv("HEYROBOTS_LIVE_ACP") != "1" {
		t.Skip("set HEYROBOTS_LIVE_ACP=1 to run against the real Claude Code")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	w := &Worker{Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	if err := w.Check(ctx); err != nil {
		t.Fatal(err)
	}
}
