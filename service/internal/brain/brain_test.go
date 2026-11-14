package brain

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"heyrobots/service/internal/core"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/store"
)

func TestOldNotesBecomeNoticedFactsForTheRightPerson(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	if err := st.Tx(func(tx *store.Tx) error {
		p, err := tx.Policy()
		if err != nil {
			return err
		}
		p.Delegates = []policy.Delegate{{Name: "Sam_Lee", Identities: []core.Identity{policy.AppDelegate("Sam_Lee")}}}
		_, err = tx.SavePolicy(p, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "ag_mail"), 0o700)
	os.WriteFile(filepath.Join(dir, "ag_mail", "owner.md"), []byte("# Notes\n- Prefers aisle seats\n- Lena signs with DocuSign\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "ag_mail", "delegate-sam_lee.md"), []byte("# Notes\n- Sam likes tea\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "ag_mail", "delegate-gone.md"), []byte("# Notes\n- old\n"), 0o600)
	n, err := ImportNotes(st, dir, now)
	if err != nil || n != 3 {
		t.Fatalf("imported %d, %v", n, err)
	}
	_ = st.Tx(func(tx *store.Tx) error {
		owner, _ := tx.Facts("ag_mail", "owner", false, 10)
		sam, _ := tx.Facts("ag_mail", core.Mandate{Role: core.RoleDelegate, Delegate: "Sam_Lee"}.Person(), false, 10)
		pinned, _ := tx.PinnedFacts("ag_mail", "owner", 10)
		if len(owner) != 2 || len(sam) != 1 || len(pinned) != 0 || owner[0].Source != store.FromSeen {
			t.Errorf("owner %+v, sam %+v, pinned %+v", owner, sam, pinned)
		}
		return nil
	})
	if _, err := os.Stat(filepath.Join(dir, "ag_mail", "owner.md.imported")); err != nil {
		t.Error("the file wasn't kept as a backup")
	}
	if _, err := os.Stat(filepath.Join(dir, "ag_mail", "delegate-gone.md")); err != nil {
		t.Error("a removed delegate's notes were touched")
	}
	// A second start imports nothing again.
	if n, _ := ImportNotes(st, dir, now); n != 0 {
		t.Errorf("imported %d again", n)
	}
}
