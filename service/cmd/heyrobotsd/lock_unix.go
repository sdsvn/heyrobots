//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockName is held by the running server, so a second one on the same data
// folder refuses to start instead of sharing the database.
const lockName = "heyrobotsd.lock"

// lockData takes the data folder's lock for this process's lifetime.
func lockData(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dataDir, lockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("another heyrobotsd is already running with %s; stop it first", dataDir)
		}
		return err
	}
	// The file stays open, and locked, until the process ends.
	return nil
}

// dataInUse reports whether a running server holds the data folder.
func dataInUse(dataDir string) bool {
	f, err := os.Open(filepath.Join(dataDir, lockName))
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}
