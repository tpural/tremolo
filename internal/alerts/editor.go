package alerts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Version is one saved version of an editor rule.
type Version struct {
	Version int       `json:"version"`
	Author  string    `json:"author,omitempty"`
	Note    string    `json:"note,omitempty"`
	Created time.Time `json:"created"`
	Source  string    `json:"source,omitempty"`
}

var (
	// ErrReadOnly means the rule is built in or a file; copy it to edit it.
	ErrReadOnly = errors.New("this rule comes from a file; it can only be changed there")
	// ErrNoDB means rules can't be saved without a database.
	ErrNoDB = errors.New("saving rules needs tremolo's database")

	ruleName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

// maxSource bounds a rule's source.
const maxSource = 64 << 10

// editorRules returns the newest version of every editor rule.
func (e *Engine) editorRules(ctx context.Context) ([]*Rule, error) {
	if e.opts.DB == nil {
		return nil, nil
	}
	rows, err := e.opts.DB.SQL().QueryContext(ctx, `SELECT v.rule, v.version, v.source FROM rule_versions v
		WHERE v.version = (SELECT max(version) FROM rule_versions WHERE rule = v.rule) ORDER BY v.rule`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Rule
	for rows.Next() {
		r := &Rule{Origin: FromEditor}
		if err := rows.Scan(&r.Name, &r.Version, &r.Source); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// fileRule reports whether a rule of that name comes from the rules directory.
func (e *Engine) fileRule(name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.rules[name]
	return ok && st.Origin == FromFile
}

// SaveRule checks source and saves it as the newest version of an editor
// rule, which starts running straight away. A rule named like a built-in
// replaces it; one named like a file rule is refused.
func (e *Engine) SaveRule(ctx context.Context, name, source, note, by string) (Version, error) {
	if e.opts.DB == nil {
		return Version{}, ErrNoDB
	}
	if !ruleName.MatchString(name) {
		return Version{}, fmt.Errorf("%q: use 1 to 64 lowercase letters, digits, dashes or underscores", name)
	}
	if len(source) > maxSource || len(note) > 500 {
		return Version{}, errors.New("the rule is over 64 KiB, or the note over 500 characters")
	}
	if e.fileRule(name) {
		return Version{}, ErrReadOnly
	}
	if _, err := ParseRule(name, source, rulesFS(e.rulesDir())); err != nil {
		return Version{}, err
	}
	v := Version{Author: by, Note: strings.TrimSpace(note), Created: e.now()}
	tx, err := e.opts.DB.SQL().BeginTx(ctx, nil)
	if err != nil {
		return Version{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO rules (name, created, created_by) VALUES (?, ?, ?) ON CONFLICT (name) DO NOTHING`,
		name, v.Created.UnixMilli(), by); err != nil {
		return Version{}, err
	}
	var last string
	err = tx.QueryRow(`SELECT source FROM rule_versions WHERE rule = ? ORDER BY version DESC LIMIT 1`, name).Scan(&last)
	if err == nil && last == source {
		return Version{}, errors.New("nothing changed since the last version")
	}
	if err := tx.QueryRow(`SELECT coalesce(max(version), 0) + 1 FROM rule_versions WHERE rule = ?`, name).Scan(&v.Version); err != nil {
		return Version{}, err
	}
	if _, err := tx.Exec(`INSERT INTO rule_versions (rule, version, source, author, note, created) VALUES (?, ?, ?, ?, ?, ?)`,
		name, v.Version, source, by, v.Note, v.Created.UnixMilli()); err != nil {
		return Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return Version{}, err
	}
	e.reloadSoon()
	return v, nil
}

// DeleteRule deletes an editor rule and its versions; its alerts resolve
// without notifying anyone, as when a rule file is removed.
func (e *Engine) DeleteRule(ctx context.Context, name string) error {
	if e.opts.DB == nil {
		return ErrNoDB
	}
	res, err := e.opts.DB.SQL().ExecContext(ctx, `DELETE FROM rules WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if e.fileRule(name) || e.builtin(name) {
			return ErrReadOnly
		}
		return ErrNoRule
	}
	e.reloadSoon()
	return nil
}

func (e *Engine) builtin(name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.rules[name]
	return ok && st.Origin == FromBuiltin
}

// Versions lists an editor rule's versions, newest first, without their source.
func (e *Engine) Versions(ctx context.Context, name string) ([]Version, error) {
	out := []Version{}
	if e.opts.DB == nil {
		return out, nil
	}
	rows, err := e.opts.DB.SQL().QueryContext(ctx, `SELECT version, author, note, created FROM rule_versions WHERE rule = ? ORDER BY version DESC`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Version
		var created int64
		if err := rows.Scan(&v.Version, &v.Author, &v.Note, &created); err != nil {
			return nil, err
		}
		v.Created = time.UnixMilli(created)
		out = append(out, v)
	}
	return out, rows.Err()
}

// VersionOf returns one version of an editor rule, with its source.
func (e *Engine) VersionOf(ctx context.Context, name string, version int) (Version, error) {
	if e.opts.DB == nil {
		return Version{}, ErrNoRule
	}
	v := Version{Version: version}
	var created int64
	err := e.opts.DB.SQL().QueryRowContext(ctx, `SELECT author, note, created, source FROM rule_versions WHERE rule = ? AND version = ?`,
		name, version).Scan(&v.Author, &v.Note, &created, &v.Source)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, ErrNoRule
	}
	v.Created = time.UnixMilli(created)
	return v, err
}

// Rule returns a running rule by name.
func (e *Engine) Rule(name string) (RuleStatus, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.rules[name]
	if !ok {
		return RuleStatus{}, false
	}
	return *st, true
}

// reloadSoon asks Watch to load the rules again now, rather than at its
// next look at the rules directory.
func (e *Engine) reloadSoon() {
	select {
	case e.reload <- struct{}{}:
	default:
	}
}

func (e *Engine) rulesDir() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dir
}

// load reads every rule and starts them.
func (e *Engine) load(ctx context.Context, dir string) {
	editor, err := e.editorRules(ctx)
	if err != nil {
		e.opts.Log.Error("loading editor rules", "err", err)
	}
	rules, lerr := LoadRules(dir, e.opts.Config.Disable, editor...)
	if lerr != nil {
		e.opts.Log.Error("loading rules", "err", lerr)
	}
	for _, r := range editor {
		if i := slices.IndexFunc(rules, func(x *Rule) bool { return x.Name == r.Name }); i >= 0 && rules[i].Origin == FromFile {
			lerr = errors.Join(lerr, fmt.Errorf("%s: a file in the rules directory replaces the editor's rule of the same name", r.Name))
		}
	}
	e.SetRules(ctx, rules, errors.Join(err, lerr))
	e.opts.Log.Info("rules loaded", "rules", len(rules), "editor", len(editor), "dir", dir)
}
