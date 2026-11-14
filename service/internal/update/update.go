// Package update checks GitHub for a newer heyrobots release and installs
// it in place: the server binary, and on a Mac the app beside it. Every
// file is checked against the release's SHA256SUMS.txt, as the install
// script does, and the new server is run once before it replaces the old.
package update

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// States of an update.
const (
	Idle        = "idle"
	Downloading = "downloading"
	Waiting     = "waiting"
	Restarting  = "restarting"
	Failed      = "failed"
)

// Status is what the app shows about updates.
type Status struct {
	Version   string
	Latest    string
	Newer     bool
	Notes     string
	URL       string
	APKURL    string
	CanUpdate bool
	// Why says why this install can't update itself, when it can't.
	Why       string
	State     string
	Error     string
	CheckedAt time.Time
	// Working is how many tasks are running, which an update waits for.
	Working int
}

// Updater checks for and installs releases.
type Updater struct {
	// Version is the running server's version, without the "v"; "dev" for
	// a build from source.
	Version string
	Repo    string
	HTTP    *http.Client
	Log     *slog.Logger
	Now     func() time.Time
	// Exe is the server binary to replace (default: this process's).
	Exe string
	// AppDirs are where to look for the Mac app (default: /Applications
	// and ~/Applications, where the install script puts it).
	AppDirs []string
	// API is GitHub's API address, for tests.
	API string
	// Drain stops new work and waits, up to a limit, for running tasks to
	// end; it returns how to take work again if the update then fails.
	// Restart restarts the server as the new binary.
	Drain   func(ctx context.Context) (resume func())
	Working func() int
	Restart func()

	mu      sync.Mutex
	rel     *release
	checked time.Time
	state   string
	err     string
}

type release struct {
	Tag    string `json:"tag_name"`
	Body   string `json:"body"`
	URL    string `json:"html_url"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r *release) asset(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

func (u *Updater) now() time.Time {
	if u.Now == nil {
		return time.Now()
	}
	return u.Now()
}

// Run checks once a day until ctx ends.
func (u *Updater) Run(ctx context.Context) {
	for {
		if _, err := u.Check(ctx, false); err != nil && ctx.Err() == nil {
			u.Log.Info("update check failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}

// Check returns the update status, asking GitHub when fresh is set or the
// last answer is a day old. A failed check keeps the last answer.
func (u *Updater) Check(ctx context.Context, fresh bool) (Status, error) {
	u.mu.Lock()
	stale := fresh || u.now().Sub(u.checked) > 24*time.Hour || (u.rel == nil && u.now().Sub(u.checked) > 10*time.Minute)
	u.mu.Unlock()
	var err error
	if stale {
		// A failed check counts as one too, so an offline server doesn't
		// ask GitHub on every request; it keeps the last answer.
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		r, cerr := u.latest(cctx)
		cancel()
		u.mu.Lock()
		if cerr == nil {
			u.rel = r
		}
		u.checked = u.now()
		u.mu.Unlock()
		err = cerr
	}
	return u.Status(), err
}

// Status is the last known status, without asking GitHub.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := Status{Version: u.Version, State: u.state, Error: u.err, CheckedAt: u.checked}
	if s.State == "" {
		s.State = Idle
	}
	if r := u.rel; r != nil {
		s.Latest = strings.TrimPrefix(r.Tag, "v")
		s.Notes, s.URL = r.Body, r.URL
		s.APKURL = r.asset("heyrobots-app-" + r.Tag + "-android.apk")
		s.Newer = u.Version != "dev" && newer(s.Latest, u.Version)
	}
	if u.Working != nil {
		s.Working = u.Working()
	}
	s.Why = u.cannot()
	s.CanUpdate = s.Why == "" && s.Newer
	return s
}

// cannot says why this install can't replace itself, or "".
func (u *Updater) cannot() string {
	if u.Version == "dev" {
		return "This server was built from source; update it the way you built it."
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return "Updates from the app work on macOS and Linux."
	}
	exe, err := u.exe()
	if err != nil {
		return "Can't find the server's own file."
	}
	f, err := os.CreateTemp(filepath.Dir(exe), ".heyrobotsd-check-*")
	if err != nil {
		return "Can't write to " + filepath.Dir(exe) + "; run the install script again to update."
	}
	f.Close()
	os.Remove(f.Name())
	return ""
}

func (u *Updater) exe() (string, error) {
	if u.Exe != "" {
		return u.Exe, nil
	}
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

func (u *Updater) latest(ctx context.Context) (*release, error) {
	api := u.API
	if api == "" {
		api = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/repos/"+u.Repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := u.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", res.Status)
	}
	var r release
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&r); err != nil {
		return nil, err
	}
	if r.Tag == "" {
		return nil, errors.New("GitHub sent no release")
	}
	return &r, nil
}

// ErrBusy is returned when an update is already under way.
var ErrBusy = errors.New("already updating")

// Start begins installing the latest release in the background.
func (u *Updater) Start() error {
	s := u.Status()
	if !s.CanUpdate {
		if s.Why != "" {
			return errors.New(s.Why)
		}
		return errors.New("already up to date")
	}
	u.mu.Lock()
	if u.state == Downloading || u.state == Waiting || u.state == Restarting {
		u.mu.Unlock()
		return ErrBusy
	}
	u.state, u.err = Downloading, ""
	r := u.rel
	u.mu.Unlock()
	go func() {
		if err := u.install(r); err != nil {
			u.Log.Error("update failed", "to", r.Tag, "err", err)
			u.set(Failed, err.Error())
		}
	}()
	return nil
}

func (u *Updater) set(state, err string) {
	u.mu.Lock()
	u.state, u.err = state, err
	u.mu.Unlock()
}

func (u *Updater) install(r *release) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	exe, err := u.exe()
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp(filepath.Dir(exe), ".heyrobots-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	sums, err := u.sums(ctx, r, work)
	if err != nil {
		return err
	}
	name := "heyrobotsd-" + r.Tag + "-" + runtime.GOOS + "-" + runtime.GOARCH
	tgz, err := u.fetch(ctx, r, name+".tar.gz", work, sums)
	if err != nil {
		return err
	}
	next := filepath.Join(work, "heyrobotsd")
	if err := untar(tgz, name+"/heyrobotsd", next); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		_ = exec.Command("xattr", "-c", next).Run()
	}
	// A build for the wrong machine never replaces a working one.
	out, err := exec.CommandContext(ctx, next, "version").Output()
	if got := strings.TrimSpace(string(out)); err != nil || got != strings.TrimPrefix(r.Tag, "v") {
		return fmt.Errorf("the new server didn't run here (said %q, %v); nothing was changed", got, err)
	}

	// On a Mac, the app the install script put beside it.
	var app, newApp string
	if runtime.GOOS == "darwin" {
		if app = u.findApp(); app != "" {
			zip, err := u.fetch(ctx, r, "heyrobots-app-"+r.Tag+"-macos.zip", work, sums)
			if err != nil {
				return err
			}
			// Unpack beside the installed app, so the swap is a rename.
			dir, err := os.MkdirTemp(filepath.Dir(app), ".heyrobots-app-")
			if err != nil {
				return fmt.Errorf("can't write next to %s: %w", app, err)
			}
			defer os.RemoveAll(dir)
			if out, err := exec.CommandContext(ctx, "ditto", "-x", "-k", zip, dir).CombinedOutput(); err != nil {
				return fmt.Errorf("unpack the app: %v: %s", err, out)
			}
			newApp = filepath.Join(dir, "heyrobots.app")
			if _, err := os.Stat(newApp); err != nil {
				return errors.New("the app download has no heyrobots.app")
			}
			_ = exec.Command("xattr", "-cr", newApp).Run()
		}
	}

	u.set(Waiting, "")
	if u.Drain != nil {
		if resume := u.Drain(ctx); resume != nil {
			defer func() {
				if err != nil {
					resume()
				}
			}()
		}
	}
	u.set(Restarting, "")
	if newApp != "" {
		old := app + ".previous"
		os.RemoveAll(old)
		if err := os.Rename(app, old); err != nil {
			return fmt.Errorf("replace the app: %w", err)
		}
		if err := os.Rename(newApp, app); err != nil {
			_ = os.Rename(old, app)
			return fmt.Errorf("replace the app: %w", err)
		}
		os.RemoveAll(old)
	}
	prev := exe + ".previous"
	os.Remove(prev)
	if err := os.Link(exe, prev); err != nil {
		u.Log.Warn("couldn't keep the previous server", "err", err)
	}
	if err := os.Rename(next, exe); err != nil {
		return fmt.Errorf("replace the server: %w", err)
	}
	u.Log.Info("updated; restarting", "from", u.Version, "to", r.Tag)
	// The restart replaces this process, so deferred clean-ups never run.
	os.RemoveAll(work)
	if newApp != "" {
		os.RemoveAll(filepath.Dir(newApp))
	}
	if u.Restart != nil {
		u.Restart()
	}
	return nil
}

func (u *Updater) findApp() string {
	dirs := u.AppDirs
	if dirs == nil {
		dirs = []string{"/Applications"}
		if h, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, filepath.Join(h, "Applications"))
		}
	}
	for _, d := range dirs {
		p := filepath.Join(d, "heyrobots.app")
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}

// sums reads the release's SHA256SUMS.txt.
func (u *Updater) sums(ctx context.Context, r *release, work string) (map[string]string, error) {
	p, err := u.download(ctx, r, "SHA256SUMS.txt", work)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if sum, name, ok := strings.Cut(sc.Text(), " "); ok {
			m[strings.TrimLeft(name, " *")] = sum
		}
	}
	return m, sc.Err()
}

// fetch downloads a release file and checks it against its checksum.
func (u *Updater) fetch(ctx context.Context, r *release, name, work string, sums map[string]string) (string, error) {
	want := sums[name]
	if want == "" {
		return "", fmt.Errorf("SHA256SUMS.txt doesn't list %s", name)
	}
	p, err := u.download(ctx, r, name, work)
	if err != nil {
		return "", err
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return "", fmt.Errorf("%s doesn't match its checksum; nothing was changed", name)
	}
	return p, nil
}

func (u *Updater) download(ctx context.Context, r *release, name, work string) (string, error) {
	url := r.asset(name)
	if url == "" {
		return "", fmt.Errorf("release %s has no %s", r.Tag, name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	res, err := u.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", name, res.Status)
	}
	p := filepath.Join(work, name)
	f, err := os.Create(p)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, io.LimitReader(res.Body, 1<<30)); err != nil {
		f.Close()
		return "", err
	}
	return p, f.Close()
}

// untar copies one file out of a .tar.gz.
func untar(tgz, name, dest string) error {
	f, err := os.Open(tgz)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("the download has no %s", name)
		}
		if err != nil {
			return err
		}
		if h.Name != name {
			continue
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(tr, 1<<30)); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
}

// newer reports whether version a is later than b, both like "0.5.1".
func newer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}
