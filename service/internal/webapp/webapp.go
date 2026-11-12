// Package webapp serves the companion app's web build from the same address
// as the API, so a browser needs nothing else running. The build is
// embedded at compile time; `make build` at the repository root puts it in
// dist first.
package webapp

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// FS returns the web build: the folder dir when it is set, for development,
// or the embedded one. It reports false when there is no build.
func FS(dir string) (fs.FS, bool) {
	var f fs.FS
	if dir != "" {
		f = os.DirFS(dir)
	} else {
		sub, err := fs.Sub(embedded, "dist")
		if err != nil {
			return nil, false
		}
		f = sub
	}
	if _, err := fs.Stat(f, "index.html"); err != nil {
		return nil, false
	}
	return f, true
}

// Handler serves the build. Unknown paths get index.html, so the app's own
// routes load; /api paths never reach it.
func Handler(f fs.FS) http.Handler {
	files := http.FileServerFS(f)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(f, p); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		// The app's script files change with every build.
		if p == "index.html" || strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".json") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
