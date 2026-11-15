package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"heyrobots/service/internal/executor"
	"heyrobots/service/internal/store"
)

func TestClearingHistoryKeepsABackupAndRemovesTheTasksFiles(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, f := range []string{"workspaces/tk_1/notes.md", "files/fl_1/report.pdf"} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	workers := &executor.Engine{Store: st}
	_, backup, err := clearHistory(context.Background(), st, workers, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(backup); err != nil || fi.Size() == 0 {
		t.Fatalf("backup %q: %v", backup, err)
	}
	for _, d := range []string{"workspaces", "files"} {
		if _, err := os.Stat(filepath.Join(dir, d)); !os.IsNotExist(err) {
			t.Fatalf("%s is still there: %v", d, err)
		}
	}
	if agents, _ := st.Agents(); len(agents) != 4 {
		t.Fatalf("the team stays: %d agents", len(agents))
	}
}
