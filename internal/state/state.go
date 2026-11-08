// Package state keeps small pieces of tremolo's own state in JSON files.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Store holds a value of type T, saved to a file on every update. With no
// file it lives in memory only.
type Store[T any] struct {
	path string
	mu   sync.Mutex
	v    T
}

// Open loads dir/name, or starts from the zero value when it doesn't exist.
// An empty dir keeps the store in memory.
func Open[T any](dir, name string) (*Store[T], error) {
	s := &Store[T]{}
	if dir == "" {
		return s, nil
	}
	s.path = filepath.Join(dir, name)
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.v); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
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
	if s.path != "" {
		if err := write(s.path, next); err != nil {
			return err
		}
	}
	s.v = next
	return nil
}

// write replaces path atomically, so a crash never leaves half a file.
func write(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
