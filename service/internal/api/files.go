package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/store"
)

// fileLinkLife is how long a download link works.
const fileLinkLife = 10 * time.Minute

// fileLinks are the download links handed out, by key. They live in memory:
// a restart only means asking for a new one.
type fileLinks struct {
	mu sync.Mutex
	m  map[string]fileLink
}

type fileLink struct {
	effect uint
	until  time.Time
}

func (l *fileLinks) add(effect uint, now time.Time) (string, time.Time) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	key := hex.EncodeToString(b)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]fileLink{}
	}
	for k, v := range l.m {
		if now.After(v.until) {
			delete(l.m, k)
		}
	}
	until := now.Add(fileLinkLife)
	l.m[key] = fileLink{effect, until}
	return key, until
}

func (l *fileLinks) get(key string, now time.Time) (uint, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.m[key]
	if !ok || now.After(v.until) {
		return 0, false
	}
	return v.effect, true
}

// sharedFile finds a shared file and its chat.
func (s *Server) sharedFile(id uint) (store.Effect, store.SharedFile, store.Thread, error) {
	var e store.Effect
	var th store.Thread
	err := s.opts.Store.Tx(func(tx *store.Tx) error {
		var err error
		if e, err = tx.Effect(id); err != nil {
			return err
		}
		th, err = tx.Thread(e.ThreadID)
		return err
	})
	if err != nil {
		return e, store.SharedFile{}, th, err
	}
	f, ok := e.File()
	if !ok {
		return e, f, th, store.ErrNotFound
	}
	return e, f, th, nil
}

// CreateFileLink makes a short-lived download link for a file shared in a
// chat the device can see.
func (s *Server) CreateFileLink(w http.ResponseWriter, r *http.Request, fileID string) {
	id, ok := effectID(fileID)
	if !ok {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	_, _, th, err := s.sharedFile(id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !canSee(deviceFrom(r), th)) {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	if err != nil {
		s.internal(w, "find shared file", err)
		return
	}
	key, until := s.links.add(id, s.opts.Now())
	writeJSON(w, http.StatusOK, gen.FileLink{Url: "/files/" + key, ExpiresAt: until})
}

// DownloadFile serves a shared file to whoever holds a live link. It is
// outside the device check, so a browser can download it directly.
func (s *Server) DownloadFile(w http.ResponseWriter, r *http.Request) {
	id, ok := s.links.get(r.PathValue("key"), s.opts.Now())
	if !ok {
		http.Error(w, "This download link has ended. Download the file again from the chat.", http.StatusNotFound)
		return
	}
	_, f, _, err := s.sharedFile(id)
	if err != nil {
		http.Error(w, "The file is gone.", http.StatusNotFound)
		return
	}
	path := filepath.Join(s.opts.Files, filepath.Clean("/" + f.Path))
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, "The file is gone.", http.StatusNotFound)
		return
	}
	defer file.Close()
	if f.Type != "" {
		w.Header().Set("Content-Type", f.Type)
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.NewReplacer(`"`, "", "\n", "", "\r", "").Replace(f.Name)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", time.Time{}, file)
}

// fileView is a shared file as a chat item shows it.
func fileView(e store.Effect) *gen.SharedFile {
	f, ok := e.File()
	if !ok {
		return nil
	}
	v := gen.SharedFile{Id: apiEffectID(e.ID), Name: f.Name, Size: int(f.Size)}
	if f.Type != "" {
		v.Type = &f.Type
	}
	return &v
}
