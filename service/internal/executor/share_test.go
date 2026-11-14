package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/genai"

	"heyrobots/service/internal/store"
)

// An agent shares a file it made; the chat gets a download, and the server
// keeps its own copy. A file outside the task folder, even through a link,
// can't be shared.
func TestAnAgentSharesAFileItMade(t *testing.T) {
	e := newEnv(t)
	e.exec.Files = t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.model.steps = [][]*genai.Part{
		call("write_file", map[string]any{"path": "report.md", "content": "# Report\n"}),
		call("share_file", map[string]any{"path": "report.md", "caption": "Your report"}),
		call("share_file", map[string]any{"path": "../../secret.txt"}),
		reportCall("ready", "Shared it.", "Here is your report.", ""),
	}
	res := e.say(owner, "c1", "write me a report")
	e.evalAll()
	// Make a link inside the task folder that points outside it.
	tk := e.ticket(res.ThreadIDs[0])
	ws := filepath.Join(e.exec.Workspaces, tk.ID)
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(ws, "out")); err != nil {
		t.Fatal(err)
	}
	e.model.steps = append(e.model.steps[:3], call("share_file", map[string]any{"path": "out/secret.txt"}), e.model.steps[3])
	if !e.work() {
		t.Fatal("no assignment ran")
	}
	var files []store.SharedFile
	for _, ef := range e.view(res.ThreadIDs[0]).Effects {
		if f, ok := ef.File(); ok {
			files = append(files, f)
			if ef.Body != "Your report" {
				t.Errorf("caption = %q", ef.Body)
			}
		}
	}
	if len(files) != 1 || files[0].Name != "report.md" || files[0].Size != 9 || !strings.HasPrefix(files[0].Type, "text/") {
		t.Fatalf("shared = %+v", files)
	}
	if b, err := os.ReadFile(filepath.Join(e.exec.Files, files[0].Path)); err != nil || string(b) != "# Report\n" {
		t.Fatalf("the server's copy = %q %v", b, err)
	}
	for _, r := range e.model.results[2:4] {
		if r["error"] == nil {
			t.Fatalf("a file outside the task folder was shared: %v", e.model.results)
		}
	}
}
