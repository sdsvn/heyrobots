package vault

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSecretsSurviveReopenAndAreEncrypted(t *testing.T) {
	dir := t.TempDir()
	ks := FileKey{Path: filepath.Join(dir, "vault.key")}
	v, err := Open(dir, ks)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Set(ModelAPIKey, "secret-api-key-123"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, FileName))
	if bytes.Contains(raw, []byte("secret-api-key-123")) {
		t.Fatal("vault file holds the secret in plain text")
	}
	info, _ := os.Stat(filepath.Join(dir, FileName))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("vault mode = %o, want 600", info.Mode().Perm())
	}
	v2, err := Open(dir, ks)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := v2.Get(ModelAPIKey); !ok || got != "secret-api-key-123" {
		t.Fatalf("got %q, %v", got, ok)
	}
}

func TestWrongKeyIsRefused(t *testing.T) {
	dir := t.TempDir()
	v, _ := Open(dir, FileKey{Path: filepath.Join(dir, "a.key")})
	v.Set("x", "y")
	if _, err := Open(dir, FileKey{Path: filepath.Join(dir, "b.key")}); err == nil {
		t.Fatal("opening with another key must fail")
	}
}

func TestKeyFileReadableByOthersIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.key")
	if _, err := (FileKey{Path: path}).Key(); err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0o644)
	if _, err := (FileKey{Path: path}).Key(); err == nil {
		t.Fatal("a key file readable by others must be refused")
	}
}
