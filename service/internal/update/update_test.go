package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func tgz(t *testing.T, name string, body []byte) []byte {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	tw.Write(body)
	tw.Close()
	gz.Close()
	return b.Bytes()
}

// A release server with v0.6.0, whose server is a script printing its version.
func releaseServer(t *testing.T, say string, badSum bool) *httptest.Server {
	tag := "v0.6.0"
	name := "heyrobotsd-" + tag + "-" + runtime.GOOS + "-" + runtime.GOARCH
	archive := tgz(t, name+"/heyrobotsd", []byte("#!/bin/sh\necho "+say+"\n"))
	sum := sha256.Sum256(archive)
	if badSum {
		sum[0] ^= 1
	}
	files := map[string][]byte{
		name + ".tar.gz": archive,
		"SHA256SUMS.txt": []byte(hex.EncodeToString(sum[:]) + "  " + name + ".tar.gz\n"),
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r/releases/latest" {
			var as []string
			for n := range files {
				as = append(as, fmt.Sprintf(`{"name":%q,"browser_download_url":%q}`, n, srv.URL+"/dl/"+n))
			}
			fmt.Fprintf(w, `{"tag_name":%q,"body":"New things","html_url":"x","assets":[%s]}`, tag, strings.Join(as, ","))
			return
		}
		b, ok := files[strings.TrimPrefix(r.URL.Path, "/dl/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newUpdater(t *testing.T, srv *httptest.Server) (*Updater, string, chan struct{}, *bool) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "heyrobotsd")
	os.WriteFile(exe, []byte("old"), 0o755)
	restarted := make(chan struct{})
	resumed := false
	u := &Updater{Version: "0.5.0", Repo: "o/r", API: srv.URL, HTTP: srv.Client(), Exe: exe, AppDirs: []string{},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Drain:   func(context.Context) func() { return func() { resumed = true } },
		Restart: func() { close(restarted) }}
	return u, exe, restarted, &resumed
}

func wait(t *testing.T, u *Updater, state string) Status {
	t.Helper()
	for i := 0; i < 200; i++ {
		if s := u.Status(); s.State == state {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("state = %+v, want %s", u.Status(), state)
	return Status{}
}

func TestUpdateReplacesTheServerAndRestarts(t *testing.T) {
	u, exe, restarted, _ := newUpdater(t, releaseServer(t, "0.6.0", false))
	s, err := u.Check(context.Background(), true)
	if err != nil || !s.Newer || !s.CanUpdate || s.Latest != "0.6.0" || s.Notes != "New things" {
		t.Fatalf("check = %+v, %v", s, err)
	}
	if err := u.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatalf("no restart: %+v", u.Status())
	}
	if b, _ := os.ReadFile(exe); !strings.Contains(string(b), "echo 0.6.0") {
		t.Errorf("server not replaced: %q", b)
	}
	if b, _ := os.ReadFile(exe + ".previous"); string(b) != "old" {
		t.Errorf("previous server not kept: %q", b)
	}
}

func TestABadDownloadChangesNothing(t *testing.T) {
	for _, c := range []struct {
		name, say string
		bad       bool
	}{{"checksum", "0.6.0", true}, {"wrong version", "0.5.9", false}} {
		t.Run(c.name, func(t *testing.T) {
			u, exe, _, _ := newUpdater(t, releaseServer(t, c.say, c.bad))
			u.Check(context.Background(), true)
			if err := u.Start(); err != nil {
				t.Fatal(err)
			}
			s := wait(t, u, Failed)
			if !strings.Contains(s.Error, "nothing was changed") {
				t.Errorf("error = %q", s.Error)
			}
			if b, _ := os.ReadFile(exe); string(b) != "old" {
				t.Errorf("server changed: %q", b)
			}
		})
	}
}

func TestADevBuildDoesNotOfferUpdates(t *testing.T) {
	u, _, _, _ := newUpdater(t, releaseServer(t, "0.6.0", false))
	u.Version = "dev"
	s, _ := u.Check(context.Background(), true)
	if s.Newer || s.CanUpdate || s.Why == "" || u.Start() == nil {
		t.Errorf("dev build status = %+v", s)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"0.5.1", "0.5.0", true}, {"0.10.0", "0.9.9", true}, {"0.5.0", "0.5.0", false}, {"0.4.9", "0.5.0", false}, {"1.0", "0.99.1", true}} {
		if newer(c.a, c.b) != c.want {
			t.Errorf("newer(%s, %s) != %v", c.a, c.b, c.want)
		}
	}
}
