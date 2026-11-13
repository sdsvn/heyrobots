package store

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesPrivateFolderAndMigrates(t *testing.T) {
	s := openTemp(t)
	info, err := os.Stat(s.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("data folder mode = %o, want 700", perm)
	}
	files, _ := filepath.Glob(filepath.Join(s.DataDir(), DBFile+"*"))
	if len(files) == 0 {
		t.Fatal("no database files")
	}
	for _, f := range files {
		if info, _ := os.Stat(f); info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", filepath.Base(f), info.Mode().Perm())
		}
	}
	v, err := s.SchemaVersion()
	if err != nil || v != latestVersion() {
		t.Fatalf("version = %d, %v; want %d", v, err, latestVersion())
	}
	if _, err := os.Stat(filepath.Join(s.DataDir(), "backups")); !os.IsNotExist(err) {
		t.Fatal("a fresh database must not take a backup")
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := os.Stat(filepath.Join(dir, "backups")); !os.IsNotExist(err) {
		t.Fatal("reopening an up-to-date database must not take a backup")
	}
}

func TestMigratingAnExistingDatabaseTakesABackupFirst(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Pretend this database predates the latest migration.
	saved := migrations
	t.Cleanup(func() { migrations = saved })
	migrations = append(append([]migration{}, saved...), migration{
		version: latestVersion() + 1, name: "test column",
		up: func(tx *gorm.DB) error { return tx.Exec("CREATE TABLE test_upgrade (id INTEGER)").Error },
	})
	if err := s.migrate(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	backups, _ := filepath.Glob(filepath.Join(dir, "backups", "*.db"))
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want exactly one", backups)
	}
	info, _ := os.Stat(backups[0])
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %o, want 600", info.Mode().Perm())
	}
}

func TestNewerDatabaseIsRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Create(&schemaMigration{Version: latestVersion() + 5, Name: "from the future"})
	s.Close()
	if _, err := Open(dir); err == nil {
		t.Fatal("a database from a newer server must be refused")
	}
}

func TestPairingCodeWorksOnce(t *testing.T) {
	s := openTemp(t)
	now := time.Now().UTC()
	code := []byte("hash-1")
	if err := s.CreatePairingCode(PairingCode{CodeHash: code, Role: "owner", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	d, err := s.Pair(code, now, Device{ID: "d1", Name: "phone", TokenHash: []byte("t1")})
	if err != nil {
		t.Fatal(err)
	}
	if d.Role != "owner" {
		t.Fatalf("role = %q, want the code's role", d.Role)
	}
	if _, err := s.Pair(code, now, Device{ID: "d2", Name: "other", TokenHash: []byte("t2")}); !errors.Is(err, ErrCodeInvalid) {
		t.Fatalf("second use err = %v, want ErrCodeInvalid", err)
	}
}

func TestPairingCodeRaceCreatesOneDevice(t *testing.T) {
	s := openTemp(t)
	now := time.Now().UTC()
	code := []byte("race")
	s.CreatePairingCode(PairingCode{CodeHash: code, Role: "owner", CreatedAt: now, ExpiresAt: now.Add(time.Minute)})
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Pair(code, now, Device{ID: string(rune('a' + i)), Name: "x", TokenHash: []byte{byte(i)}})
			if err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("%d devices paired with one code, want 1", ok)
	}
}

func TestExpiredPairingCodeIsRefused(t *testing.T) {
	s := openTemp(t)
	now := time.Now().UTC()
	s.CreatePairingCode(PairingCode{CodeHash: []byte("old"), Role: "owner", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute)})
	if _, err := s.Pair([]byte("old"), now, Device{ID: "d", Name: "x", TokenHash: []byte("t")}); !errors.Is(err, ErrCodeInvalid) {
		t.Fatalf("err = %v, want ErrCodeInvalid", err)
	}
}

func TestRevokedDeviceTokenStopsWorking(t *testing.T) {
	s := openTemp(t)
	now := time.Now().UTC()
	s.CreatePairingCode(PairingCode{CodeHash: []byte("c"), Role: "owner", CreatedAt: now, ExpiresAt: now.Add(time.Minute)})
	s.Pair([]byte("c"), now, Device{ID: "d1", Name: "phone", TokenHash: []byte("tok")})
	if n, _ := s.ActiveOwnerDevices(); n != 1 {
		t.Fatalf("active owner devices = %d, want 1", n)
	}
	if err := s.RevokeDevice("d1", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeviceByTokenHash([]byte("tok")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if n, _ := s.ActiveOwnerDevices(); n != 0 {
		t.Fatalf("active owner devices = %d, want 0", n)
	}
	if err := s.RevokeDevice("d1", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoking twice err = %v, want ErrNotFound", err)
	}
}

// Transactions that read and then write must wait for each other rather
// than fail with "database is locked". This happened with several Eval
// workers and intake running at once.
func TestConcurrentReadThenWriteTransactionsWait(t *testing.T) {
	s := openTemp(t)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.Tx(func(tx *Tx) error {
				if _, err := tx.Policy(); err != nil {
					return err
				}
				time.Sleep(time.Millisecond)
				return tx.CreateDecision(Decision{ID: "de_" + string(rune('A'+i)), Kind: DecisionIgnore, CreatedAt: time.Now()})
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("transaction failed: %v", err)
		}
	}
}
