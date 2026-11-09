package state

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Store holds a value of type T, kept in memory and saved to the database as
// JSON on every update. With a nil DB it lives in memory only.
type Store[T any] struct {
	db   *DB
	name string
	mu   sync.Mutex
	v    T
}

// Load returns the store called name, or one holding the zero value when
// there's none yet. The first time, it imports name.json from the data
// directory of an earlier tremolo, and renames that file to name.json.imported.
func Load[T any](db *DB, name string) (*Store[T], error) {
	s := &Store[T]{db: db, name: name}
	if db == nil {
		return s, nil
	}
	var raw string
	err := db.sql.QueryRow(`SELECT value FROM state WHERE name = ?`, name).Scan(&raw)
	if err == nil {
		if err := json.Unmarshal([]byte(raw), &s.v); err != nil {
			return nil, fmt.Errorf("state %s: %w", name, err)
		}
		return s, nil
	}
	b, path, ok := db.legacy(name)
	if !ok {
		return s, nil
	}
	if err := json.Unmarshal(b, &s.v); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := s.save(s.v); err != nil {
		return nil, err
	}
	if err := os.Rename(path, path+".imported"); err != nil {
		return nil, err
	}
	return s, nil
}

// Read calls fn with the value. fn must not keep or change it.
func (s *Store[T]) Read(fn func(*T)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.v)
}

// Update changes the value with fn and saves it. When fn or saving fails, the
// value is left as it was.
func (s *Store[T]) Update(fn func(*T) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Work on a copy, so a failure leaves nothing half changed.
	b, err := json.Marshal(s.v)
	if err != nil {
		return err
	}
	var next T
	if err := json.Unmarshal(b, &next); err != nil {
		return err
	}
	if err := fn(&next); err != nil {
		return err
	}
	if err := s.save(next); err != nil {
		return err
	}
	s.v = next
	return nil
}

func (s *Store[T]) save(v T) error {
	if s.db == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.sql.Exec(`INSERT INTO state (name, value, updated) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET value = excluded.value, updated = excluded.updated`,
		s.name, string(b), time.Now().UnixMilli())
	return err
}
