package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("entry not found")

// Entry is the list/summary view of a document. Content is deliberately not
// loaded here: the list must stay cheap regardless of document size.
type Entry struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt int64  `json:"updatedAt"` // unix milliseconds
}

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS entries (
	id         TEXT PRIMARY KEY,
	title      TEXT NOT NULL DEFAULT 'Untitled',
	content    TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS entries_updated_at ON entries (updated_at DESC);
`

// OpenStore opens (creating if needed) the SQLite database at path.
// ":memory:" gives a private in-memory database, used by tests.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// One connection: an in-memory database exists per connection, and a single
	// writer is all SQLite allows anyway.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// newID returns a 10-character random id (50 bits), short enough for a URL.
func newID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return idEncoding.EncodeToString(b[:])[:10], nil
}

func (s *Store) CreateEntry(ctx context.Context) (Entry, error) {
	id, err := newID()
	if err != nil {
		return Entry{}, err
	}
	now := time.Now().UnixMilli()
	e := Entry{ID: id, Title: untitled, UpdatedAt: now}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO entries (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		e.ID, e.Title, now, now)
	return e, err
}

func (s *Store) GetEntry(ctx context.Context, id string) (Entry, error) {
	var e Entry
	err := s.db.QueryRowContext(ctx,
		`SELECT id, title, updated_at FROM entries WHERE id = ?`, id,
	).Scan(&e.ID, &e.Title, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	return e, err
}

// ListEntries returns all entries, most recently updated first.
func (s *Store) ListEntries(ctx context.Context) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, updated_at FROM entries ORDER BY updated_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []Entry{} // non-nil so JSON encodes [] rather than null
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Title, &e.UpdatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *Store) DeleteEntry(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM entries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetContent returns the stored markdown of an entry.
func (s *Store) GetContent(ctx context.Context, id string) (string, error) {
	var content string
	err := s.db.QueryRowContext(ctx,
		`SELECT content FROM entries WHERE id = ?`, id,
	).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return content, err
}

// SaveContent replaces an entry's content and derived title and bumps its
// updated time.
func (s *Store) SaveContent(ctx context.Context, id, content, title string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE entries SET content = ?, title = ?, updated_at = ? WHERE id = ?`,
		content, title, time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}
