// Package runbooks keeps runbooks: Markdown notes on how to handle a rule's
// alerts, which grow as people record how each one was fixed.
package runbooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tpural/tremolo/internal/state"
)

// Runbook is one note, attached to the rules it covers.
type Runbook struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Rules     []string  `json:"rules"`
	Version   int       `json:"version"`
	Updated   time.Time `json:"updated"`
	UpdatedBy string    `json:"updatedBy"`
}

var (
	// ErrNotFound is a runbook that doesn't exist.
	ErrNotFound = errors.New("no such runbook")
	// ErrExists is a new runbook whose id is taken.
	ErrExists = errors.New("a runbook with that id exists")
	// ErrChanged is a save based on an older version than the one stored.
	ErrChanged = errors.New("the runbook changed since you opened it")
	// ErrInvalid is a runbook with a bad id, title, body or rules.
	ErrInvalid = errors.New("invalid runbook")
)

const (
	maxTitle = 200
	maxBody  = 256 << 10
	maxRules = 50
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Store keeps runbooks in tremolo's database.
type Store struct {
	db *sql.DB
	mu sync.RWMutex
	// byRule is each rule's runbook ids, for notifications, which can't wait on the database.
	byRule map[string][]string
}

// Open returns the runbooks in db.
func Open(db *state.DB) (*Store, error) {
	s := &Store{db: db.SQL()}
	return s, s.refresh(context.Background())
}

// ForRule returns the ids of the runbooks covering rule.
func (s *Store) ForRule(rule string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.byRule[rule])
}

// ByRule returns every rule's runbook ids.
func (s *Store) ByRule() map[string][]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string][]string, len(s.byRule))
	for k, v := range s.byRule {
		out[k] = slices.Clone(v)
	}
	return out
}

func (s *Store) refresh(ctx context.Context) error {
	all, err := s.List(ctx, "")
	if err != nil {
		return err
	}
	by := map[string][]string{}
	for _, rb := range all {
		for _, r := range rb.Rules {
			by[r] = append(by[r], rb.ID)
		}
	}
	s.mu.Lock()
	s.byRule = by
	s.mu.Unlock()
	return nil
}

// List returns runbooks by title, all of them or those covering rule.
func (s *Store) List(ctx context.Context, rule string) ([]Runbook, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, body, rules, version, updated, updated_by FROM runbooks ORDER BY title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Runbook{}
	for rows.Next() {
		rb, err := scan(rows)
		if err != nil {
			return nil, err
		}
		if rule == "" || slices.Contains(rb.Rules, rule) {
			out = append(out, rb)
		}
	}
	return out, rows.Err()
}

// Get returns one runbook.
func (s *Store) Get(ctx context.Context, id string) (Runbook, error) {
	rb, err := scan(s.db.QueryRowContext(ctx, `SELECT id, title, body, rules, version, updated, updated_by FROM runbooks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return rb, ErrNotFound
	}
	return rb, err
}

// Create adds a runbook. An empty id is made from the title.
func (s *Store) Create(ctx context.Context, rb Runbook, by string) (Runbook, error) {
	if rb.ID == "" {
		rb.ID = Slug(rb.Title)
	}
	rb.Version = 0
	return s.save(ctx, rb, by, true)
}

// Update replaces a runbook's title, body and rules. rb.Version is the version
// it was based on; 0 skips the check.
func (s *Store) Update(ctx context.Context, rb Runbook, by string) (Runbook, error) {
	return s.save(ctx, rb, by, false)
}

func (s *Store) save(ctx context.Context, rb Runbook, by string, create bool) (Runbook, error) {
	if err := clean(&rb); err != nil {
		return rb, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return rb, err
	}
	defer func() { _ = tx.Rollback() }()
	var current int
	err = tx.QueryRowContext(ctx, `SELECT version FROM runbooks WHERE id = ?`, rb.ID).Scan(&current)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if !create {
			return rb, ErrNotFound
		}
	case err != nil:
		return rb, err
	case create:
		return rb, ErrExists
	case rb.Version != 0 && rb.Version != current:
		return rb, ErrChanged
	}
	rb.Version, rb.Updated, rb.UpdatedBy = current+1, time.Now(), by
	rules, _ := json.Marshal(rb.Rules)
	if _, err := tx.ExecContext(ctx, `INSERT INTO runbooks (id, title, body, rules, version, updated, updated_by) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET title = excluded.title, body = excluded.body, rules = excluded.rules,
		version = excluded.version, updated = excluded.updated, updated_by = excluded.updated_by`,
		rb.ID, rb.Title, rb.Body, string(rules), rb.Version, rb.Updated.UnixMilli(), by); err != nil {
		return rb, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO runbook_versions (runbook, version, title, body, rules, updated, updated_by) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rb.ID, rb.Version, rb.Title, rb.Body, string(rules), rb.Updated.UnixMilli(), by); err != nil {
		return rb, err
	}
	if err := tx.Commit(); err != nil {
		return rb, err
	}
	return rb, s.refresh(ctx)
}

// Append adds a section to the end of a runbook.
func (s *Store) Append(ctx context.Context, id, section, by string) (Runbook, error) {
	rb, err := s.Get(ctx, id)
	if err != nil {
		return rb, err
	}
	rb.Body = strings.TrimRight(rb.Body, "\n") + "\n\n" + strings.TrimSpace(section) + "\n"
	return s.Update(ctx, rb, by)
}

// Delete removes a runbook and its versions.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM runbooks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM runbook_versions WHERE runbook = ?`, id); err != nil {
		return err
	}
	return s.refresh(ctx)
}

// Versions returns a runbook's versions, newest first.
func (s *Store) Versions(ctx context.Context, id string) ([]Runbook, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT runbook, title, body, rules, version, updated, updated_by FROM runbook_versions WHERE runbook = ? ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Runbook{}
	for rows.Next() {
		rb, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rb)
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, rows.Err()
}

// Import adds the Markdown files in dir that aren't runbooks yet, each named
// by its file (crash-loop.md is crash-loop). A file may start with a front
// matter line "rules: a, b"; its first "# " heading is the title. Runbooks
// that exist are left alone, so edits in tremolo stick.
func (s *Store) Import(ctx context.Context, dir string) (int, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		rb := parse(strings.TrimSuffix(filepath.Base(f), ".md"), string(b))
		if _, err := s.Create(ctx, rb, "tremolo"); errors.Is(err, ErrExists) {
			continue
		} else if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(f), err))
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

func parse(id, text string) Runbook {
	rb := Runbook{ID: id, Title: id}
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if front, body, ok := strings.Cut(rest, "\n---\n"); ok {
			for line := range strings.SplitSeq(front, "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(line), "rules:"); ok {
					for r := range strings.SplitSeq(strings.Trim(strings.TrimSpace(v), "[]"), ",") {
						if r = strings.TrimSpace(r); r != "" {
							rb.Rules = append(rb.Rules, r)
						}
					}
				}
			}
			text = body
		}
	}
	for line := range strings.SplitSeq(text, "\n") {
		if t, ok := strings.CutPrefix(line, "# "); ok {
			rb.Title = strings.TrimSpace(t)
			break
		}
	}
	rb.Body = strings.TrimSpace(text) + "\n"
	return rb
}

// Slug makes an id from a title, e.g. "Disk full on mm02" is disk-full-on-mm02.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}

func clean(rb *Runbook) error {
	rb.Title, rb.Body = strings.TrimSpace(rb.Title), strings.TrimRight(rb.Body, " \n")+"\n"
	switch {
	case !idPattern.MatchString(rb.ID) || rb.ID == "proposals":
		return fmt.Errorf("%w: id %q must be lowercase letters, digits and dashes", ErrInvalid, rb.ID)
	case rb.Title == "" || len(rb.Title) > maxTitle:
		return fmt.Errorf("%w: title must be 1 to %d characters", ErrInvalid, maxTitle)
	case len(rb.Body) > maxBody:
		return fmt.Errorf("%w: body must be under %d KiB", ErrInvalid, maxBody>>10)
	case len(rb.Rules) > maxRules:
		return fmt.Errorf("%w: at most %d rules", ErrInvalid, maxRules)
	}
	var rules []string
	for _, r := range rb.Rules {
		if r = strings.TrimSpace(r); r != "" && !slices.Contains(rules, r) {
			rules = append(rules, r)
		}
	}
	rb.Rules = rules
	if rb.Rules == nil {
		rb.Rules = []string{}
	}
	return nil
}

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Runbook, error) {
	var rb Runbook
	var rules string
	var updated int64
	if err := row.Scan(&rb.ID, &rb.Title, &rb.Body, &rules, &rb.Version, &updated, &rb.UpdatedBy); err != nil {
		return rb, err
	}
	rb.Updated = time.UnixMilli(updated)
	if err := json.Unmarshal([]byte(rules), &rb.Rules); err != nil || rb.Rules == nil {
		rb.Rules = []string{}
	}
	return rb, nil
}
