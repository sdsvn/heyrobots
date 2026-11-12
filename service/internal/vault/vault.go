// Package vault keeps secrets, such as the model API key and account
// tokens, encrypted on disk. The model never sees what is in it.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// FileName is the vault's file name inside the data folder.
const FileName = "vault.enc"

// KeySource provides the 32-byte key that encrypts the vault, creating it
// on first use.
type KeySource interface {
	Key() ([]byte, error)
}

// Vault is an encrypted map of named secrets.
type Vault struct {
	mu   sync.Mutex
	path string
	aead cipher.AEAD
	data map[string]string
}

// Open opens or creates the vault in dataDir.
func Open(dataDir string, ks KeySource) (*Vault, error) {
	key, err := ks.Key()
	if err != nil {
		return nil, fmt.Errorf("vault key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("vault key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	v := &Vault{path: filepath.Join(dataDir, FileName), aead: aead, data: map[string]string{}}
	raw, err := os.ReadFile(v.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return v, nil
	case err != nil:
		return nil, err
	}
	ns := aead.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("vault file is damaged")
	}
	plain, err := aead.Open(nil, raw[:ns], raw[ns:], []byte(FileName))
	if err != nil {
		return nil, errors.New("vault can't be decrypted: wrong key or damaged file")
	}
	if err := json.Unmarshal(plain, &v.data); err != nil {
		return nil, fmt.Errorf("vault contents: %w", err)
	}
	return v, nil
}

// Get returns a secret.
func (v *Vault) Get(name string) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, ok := v.data[name]
	return s, ok
}

// Set stores a secret and writes the vault to disk.
func (v *Vault) Set(name, value string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	old, had := v.data[name]
	v.data[name] = value
	if err := v.save(); err != nil {
		if had {
			v.data[name] = old
		} else {
			delete(v.data, name)
		}
		return err
	}
	return nil
}

// Delete removes a secret.
func (v *Vault) Delete(name string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	old, had := v.data[name]
	if !had {
		return nil
	}
	delete(v.data, name)
	if err := v.save(); err != nil {
		v.data[name] = old
		return err
	}
	return nil
}

// save writes the vault atomically: a temporary file, then a rename.
func (v *Vault) save() error {
	plain, err := json.Marshal(v.data)
	if err != nil {
		return err
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	out := v.aead.Seal(nonce, nonce, plain, []byte(FileName))
	tmp, err := os.CreateTemp(filepath.Dir(v.path), ".vault-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), v.path)
}

// Secret names used by the server.
const (
	ModelAPIKey = "model_api_key"
	// NtfyToken is the access token for the ntfy server.
	NtfyToken = "ntfy_token"
	// GoogleClient is the owner's own Google OAuth client, as JSON.
	GoogleClient = "google_client"
	// GoogleToken is the Google account's OAuth token, as JSON.
	GoogleToken = "google_token"
)
