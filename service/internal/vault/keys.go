package vault

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/zalando/go-keyring"
)

// FileKey keeps the vault key in a file readable only by the server's user.
// It is the default on Linux, where a headless server has no keychain.
type FileKey struct{ Path string }

// Key reads the key file, creating it on first use.
func (f FileKey) Key() ([]byte, error) {
	raw, err := os.ReadFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		// O_EXCL: never overwrite a key that appeared meanwhile.
		file, err := os.OpenFile(f.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		if _, err := file.Write(key); err != nil {
			return nil, err
		}
		return key, file.Sync()
	}
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(f.Path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("key file %s is readable by other users; run chmod 600 on it", f.Path)
	}
	return raw, nil
}

// KeychainKey keeps the vault key in the operating system's keychain, such
// as the macOS Keychain. It is the default on macOS.
type KeychainKey struct {
	Service, Account string
}

// Key reads the key from the keychain, creating it on first use.
func (k KeychainKey) Key() ([]byte, error) {
	s, err := keyring.Get(k.Service, k.Account)
	if errors.Is(err, keyring.ErrNotFound) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := keyring.Set(k.Service, k.Account, base64.StdEncoding.EncodeToString(key)); err != nil {
			return nil, fmt.Errorf("store vault key in keychain: %w", err)
		}
		return key, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read vault key from keychain: %w", err)
	}
	return base64.StdEncoding.DecodeString(s)
}
