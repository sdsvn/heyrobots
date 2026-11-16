package acp

import (
	"context"
	"log/slog"
	"os"
	"strings"
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

// TestLiveSearch checks a quick search runs Claude Code's web search and
// nothing else. It only runs with HEYROBOTS_LIVE_ACP=1.
func TestLiveSearch(t *testing.T) {
	if os.Getenv("HEYROBOTS_LIVE_ACP") != "1" {
		t.Skip("set HEYROBOTS_LIVE_ACP=1 to run against the real Claude Code")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	w := &Worker{Log: slog.New(slog.NewTextHandler(os.Stderr, nil))}
	text, err := w.Search(ctx, "What is the latest stable release of the Go programming language? Search the web with your web search tool, then answer in one line with the source URL. Use no other tool.")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(text)
	if !strings.Contains(text, "http") {
		t.Fatalf("no source in %q", text)
	}
}
