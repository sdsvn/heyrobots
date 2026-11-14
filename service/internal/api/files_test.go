package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/store"
)

// A file an agent shared shows in the chat, and a device that can see the
// chat gets a short-lived link that downloads it without signing in.
func TestASharedFileDownloadsFromItsChat(t *testing.T) {
	files := t.TempDir()
	h := newHarnessWith(t, nil, func(_ *harness, o *Options) { o.Files = files })
	owner := h.completeSetup()
	delegate := h.pair(h.inviteDelegate(owner, "partner"), "partner phone")
	_, msg := h.send(owner, "m-1", "", "make me a pdf")

	if err := os.MkdirAll(filepath.Join(files, "fl_1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "fl_1", "what I can do.pdf"), []byte("%PDF-1.7 hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(store.SharedFile{Name: "what I can do.pdf", Size: 14, Type: "application/pdf", Path: "fl_1/what I can do.pdf"})
	if err := h.store.Tx(func(tx *store.Tx) error {
		return tx.CreateEffect(store.Effect{ThreadID: msg.ThreadId, Source: "app", Kind: store.KindFile, Reach: "owner", Body: "Your PDF",
			Payload: string(payload), Status: store.EffectSent, CreatedAt: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}

	var th gen.ThreadDetail
	json.Unmarshal(h.do("GET", "/api/v1/threads/"+msg.ThreadId, owner, nil, "").Body.Bytes(), &th)
	var file *gen.SharedFile
	for _, it := range th.Items {
		if it.File != nil {
			file = it.File
		}
	}
	if file == nil || file.Name != "what I can do.pdf" || file.Size != 14 || *file.Type != "application/pdf" {
		t.Fatalf("thread items = %+v", th.Items)
	}

	if rec := h.do("POST", "/api/v1/files/"+file.Id+"/link", delegate, nil, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("a delegate who can't see the chat got a link: %d", rec.Code)
	}
	rec := h.do("POST", "/api/v1/files/"+file.Id+"/link", owner, nil, "")
	var link gen.FileLink
	json.Unmarshal(rec.Body.Bytes(), &link)
	if rec.Code != http.StatusOK || !strings.HasPrefix(link.Url, "/files/") {
		t.Fatalf("link = %d %s", rec.Code, rec.Body)
	}
	got := h.do("GET", link.Url, "", nil, "")
	if got.Code != http.StatusOK || got.Body.String() != "%PDF-1.7 hello" || !strings.Contains(got.Header().Get("Content-Disposition"), `attachment; filename="what I can do.pdf"`) {
		t.Fatalf("download = %d %v %q", got.Code, got.Header(), got.Body)
	}
	if bad := h.do("GET", "/files/not-a-link", "", nil, ""); bad.Code != http.StatusNotFound {
		t.Fatalf("a made-up link = %d", bad.Code)
	}
}
