package webapp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServesTheBuildAndFallsBackToIndex(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o600)
	os.WriteFile(filepath.Join(dir, "main.dart.js"), []byte("console.log(1)"), 0o600)
	f, ok := FS(dir)
	if !ok {
		t.Fatal("no build found")
	}
	h := Handler(f)
	for path, want := range map[string]string{"/": "app", "/main.dart.js": "console", "/thread/th_1": "app"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body)
		}
	}
	if _, ok := FS(t.TempDir()); ok {
		t.Fatal("an empty folder counts as a build")
	}
}
