// Package store owns the server's database and data folder. Only this
// package talks to the database.
package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Store is the server's database.
type Store struct {
	db      *gorm.DB
	dataDir string
	dsn     string
}

// DSN is the database's connection string, for ADK's session store, which
// keeps conversation history in the same file.
func (s *Store) DSN() string { return s.dsn }

// DBFile is the database's file name inside the data folder.
const DBFile = "heyrobots.db"

// Open prepares the data folder, opens the database and applies pending
// migrations. Before migrating an existing database it writes a backup to
// the backups folder.
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data folder: %w", err)
	}
	// The folder holds credentials and personal data, so only the server's
	// user may read it.
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("restrict data folder: %w", err)
	}
	// Create the database file private before SQLite opens it. SQLite gives
	// its WAL and shared-memory files the same permissions.
	dbPath := filepath.Join(dataDir, DBFile)
	f, err := os.OpenFile(dbPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create database file: %w", err)
	}
	f.Close()
	if err := os.Chmod(dbPath, 0o600); err != nil {
		return nil, fmt.Errorf("restrict database file: %w", err)
	}
	// _txlock=immediate takes the write lock when a transaction begins. A
	// transaction that read first and then tried to write could otherwise
	// fail at once with "database is locked", because SQLite can't wait in
	// that case. With the lock taken up front, busy_timeout lets it wait.
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard, NowFunc: func() time.Time { return time.Now().UTC() }})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	s := &Store{db: db, dataDir: dataDir, dsn: dsn}
	if err := s.migrate(time.Now().UTC()); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// DataDir is the data folder this store uses.
func (s *Store) DataDir() string { return s.dataDir }

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Raw runs a read-only query, for tests.
func (s *Store) Raw(sql string, args ...any) *gorm.DB { return s.db.Raw(sql, args...) }
