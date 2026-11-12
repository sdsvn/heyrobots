package toolsets

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/netguard"
)

func TestFilesStayInsideTheWorkspace(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "tk_1")
	if r := writeFile(ws, "drafts/letter.md", "Dear Anna"); !r.OK {
		t.Fatalf("write: %+v", r)
	}
	if r := readFile(ws, "drafts/letter.md"); r.Content != "Dear Anna" {
		t.Fatalf("read: %+v", r)
	}
	secret := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(secret, []byte("the owner's secret"), 0o600)
	os.Symlink(secret, filepath.Join(ws, "link.txt"))
	os.Symlink(filepath.Dir(secret), filepath.Join(ws, "linkdir"))

	for _, p := range []string{"../escape.txt", "/etc/passwd", "drafts/../../x", "link.txt", "linkdir/secret.txt", ".tmp/x", ""} {
		if r := readFile(ws, p); r.OK || strings.Contains(r.Content, "secret") {
			t.Errorf("read %q: %+v", p, r)
		}
		if r := writeFile(ws, p, "x"); r.OK {
			t.Errorf("write %q succeeded", p)
		}
	}
	if b, _ := os.ReadFile(secret); string(b) != "the owner's secret" {
		t.Fatal("a file outside the workspace changed")
	}
	if r := writeFile(ws, "big.txt", strings.Repeat("a", MaxFileBytes+1)); r.OK {
		t.Fatal("an oversized file was written")
	}
}

func TestNotesAccumulate(t *testing.T) {
	ws := t.TempDir()
	appendNote(ws, "first")
	appendNote(ws, "second")
	if r := readFile(ws, "notes.md"); r.Content != "- first\n- second\n" {
		t.Fatalf("notes = %q", r.Content)
	}
	if l := listFiles(ws); len(l.Files) != 1 || l.Files[0] != "notes.md" {
		t.Fatalf("files = %v", l.Files)
	}
}

func TestFetchUsesTheNetworkGuard(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "router admin page") }))
	defer local.Close()
	_, err := fetchText(context.Background(), netguard.Client(netguard.PublicWeb, 5*time.Second), local.URL)
	if !errors.Is(err, netguard.ErrRefused) {
		t.Fatalf("err = %v, want the guard to refuse a local address", err)
	}
	for _, u := range []string{"file:///etc/passwd", "ftp://x.example/a", "not a url"} {
		if _, err := fetchText(context.Background(), netguard.Client(netguard.PublicWeb, time.Second), u); err == nil {
			t.Errorf("%s was fetched", u)
		}
	}
}

func TestHTMLTextDropsScriptsAndKeepsLinks(t *testing.T) {
	got := htmlText(`<html><head><style>.x{}</style><script>steal()</script></head><body><h1>Menu</h1><p>Pasta <a href="https://book.example/now">book</a></p></body></html>`)
	if strings.Contains(got, "steal") || strings.Contains(got, ".x{}") || !strings.Contains(got, "Menu") || !strings.Contains(got, "[link: https://book.example/now]") {
		t.Fatalf("text = %q", got)
	}
}

func TestToolsetsBuild(t *testing.T) {
	env := Env{Workspace: t.TempDir(), Web: netguard.Client(netguard.PublicWeb, time.Second)}
	for ts, want := range map[core.Toolset][]string{
		core.ToolsetResearch: {"web_search", "web_fetch", "save_note", "read_notes"},
		core.ToolsetWriting:  {"write_file", "read_file", "list_files"},
	} {
		tools, err := For(ts, env)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, tl := range tools {
			names = append(names, tl.Name())
		}
		if strings.Join(names, ",") != strings.Join(want, ",") {
			t.Errorf("%s tools = %v", ts, names)
		}
	}
	if _, err := For(core.ToolsetCode, env); err == nil {
		t.Error("code is not available yet and must say so")
	}
}
