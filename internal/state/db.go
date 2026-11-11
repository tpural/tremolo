// Package state keeps tremolo's own state in a SQLite database: accounts,
// alerts, silences, maintenance, alert history and the audit log.
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // the database/sql driver "sqlite"
)

// File is the database's name in the data directory.
const File = "tremolo.db"

// migrations bring the schema up to date, in order; never edit one that has shipped.
var migrations = []string{
	`CREATE TABLE state (name TEXT PRIMARY KEY, value TEXT NOT NULL, updated INTEGER NOT NULL);
	CREATE TABLE audit (id INTEGER PRIMARY KEY, time INTEGER NOT NULL, actor TEXT NOT NULL,
		action TEXT NOT NULL, target TEXT NOT NULL, detail TEXT NOT NULL);
	CREATE INDEX audit_time ON audit (time);
	CREATE TABLE alert_history (id INTEGER PRIMARY KEY, alert TEXT NOT NULL, rule TEXT NOT NULL,
		severity TEXT NOT NULL, summary TEXT NOT NULL, fired INTEGER NOT NULL, resolved INTEGER NOT NULL,
		acked INTEGER, acked_by TEXT NOT NULL, assignee TEXT NOT NULL, data TEXT NOT NULL);
	CREATE INDEX alert_history_resolved ON alert_history (resolved);
	CREATE INDEX alert_history_rule ON alert_history (rule, resolved);`,

	// Accounts. Times are Unix milliseconds. A user without a password_hash
	// signs in only through an external provider, linked in identities.
	`CREATE TABLE users (
		name TEXT PRIMARY KEY,
		display_name TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL CHECK (role IN ('viewer', 'editor', 'admin')),
		password_hash TEXT,
		created INTEGER NOT NULL
	);
	CREATE TABLE contacts (
		user TEXT NOT NULL REFERENCES users (name) ON DELETE CASCADE ON UPDATE CASCADE,
		position INTEGER NOT NULL,
		channel TEXT NOT NULL,
		address TEXT NOT NULL DEFAULT '',
		after_ms INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (user, position)
	);
	CREATE TABLE teams (
		name TEXT PRIMARY KEY,
		notify TEXT NOT NULL DEFAULT '[]',
		created INTEGER NOT NULL
	);
	CREATE TABLE team_members (
		team TEXT NOT NULL REFERENCES teams (name) ON DELETE CASCADE ON UPDATE CASCADE,
		user TEXT NOT NULL REFERENCES users (name) ON DELETE CASCADE ON UPDATE CASCADE,
		PRIMARY KEY (team, user)
	);
	CREATE INDEX team_members_user ON team_members (user);
	CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		hash TEXT NOT NULL UNIQUE,
		user TEXT NOT NULL REFERENCES users (name) ON DELETE CASCADE ON UPDATE CASCADE,
		provider TEXT NOT NULL DEFAULT 'local',
		created INTEGER NOT NULL,
		expires INTEGER NOT NULL,
		last_seen INTEGER NOT NULL,
		address TEXT NOT NULL DEFAULT '',
		agent TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX sessions_user ON sessions (user);
	CREATE TABLE tokens (
		id TEXT PRIMARY KEY,
		hash TEXT NOT NULL UNIQUE,
		user TEXT NOT NULL REFERENCES users (name) ON DELETE CASCADE ON UPDATE CASCADE,
		name TEXT NOT NULL,
		created INTEGER NOT NULL,
		last_used INTEGER
	);
	CREATE INDEX tokens_user ON tokens (user);
	CREATE TABLE identities (
		provider TEXT NOT NULL,
		subject TEXT NOT NULL,
		user TEXT NOT NULL REFERENCES users (name) ON DELETE CASCADE ON UPDATE CASCADE,
		email TEXT NOT NULL DEFAULT '',
		created INTEGER NOT NULL,
		last_login INTEGER,
		PRIMARY KEY (provider, subject)
	);
	CREATE INDEX identities_user ON identities (user);`,

	// Rules written in the editor. Every save is a new version; the newest is
	// the one that runs.
	`CREATE TABLE rules (
		name TEXT PRIMARY KEY,
		created INTEGER NOT NULL,
		created_by TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE rule_versions (
		rule TEXT NOT NULL REFERENCES rules (name) ON DELETE CASCADE ON UPDATE CASCADE,
		version INTEGER NOT NULL,
		source TEXT NOT NULL,
		author TEXT NOT NULL DEFAULT '',
		note TEXT NOT NULL DEFAULT '',
		created INTEGER NOT NULL,
		PRIMARY KEY (rule, version)
	);`,

	// Maintenance periods, which hold alerts while they're on.
	`CREATE TABLE maintenance (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		start INTEGER NOT NULL,
		end INTEGER,
		every TEXT NOT NULL DEFAULT '' CHECK (every IN ('', 'day', 'week')),
		repeat_until INTEGER,
		rules TEXT NOT NULL DEFAULT '[]',
		hosts TEXT NOT NULL DEFAULT '[]',
		note TEXT NOT NULL DEFAULT '',
		created INTEGER NOT NULL,
		created_by TEXT NOT NULL DEFAULT ''
	);`,

	// On-call schedules were tried in a release candidate and left out; drop
	// the empty tables it may have made.
	`DROP TABLE IF EXISTS schedule_overrides;
	DROP TABLE IF EXISTS schedule_members;
	DROP TABLE IF EXISTS schedules;`,

	// Runbooks: how to handle a rule's alerts, and how they were fixed before.
	`CREATE TABLE runbooks (id TEXT PRIMARY KEY, title TEXT NOT NULL, body TEXT NOT NULL,
		rules TEXT NOT NULL, version INTEGER NOT NULL, updated INTEGER NOT NULL, updated_by TEXT NOT NULL);
	CREATE TABLE runbook_versions (runbook TEXT NOT NULL, version INTEGER NOT NULL, title TEXT NOT NULL,
		body TEXT NOT NULL, rules TEXT NOT NULL, updated INTEGER NOT NULL, updated_by TEXT NOT NULL,
		PRIMARY KEY (runbook, version));
	ALTER TABLE alert_history ADD COLUMN fix TEXT NOT NULL DEFAULT '';`,

	// Assistant tokens: what each may do, and when it expires; and which token
	// did what.
	`ALTER TABLE tokens ADD COLUMN scope TEXT;
	ALTER TABLE tokens ADD COLUMN expires INTEGER;
	ALTER TABLE audit ADD COLUMN via TEXT NOT NULL DEFAULT '';`,

	// Investigations: agents' work on alerts, gaps and runbooks, with what they
	// found and did.
	`CREATE TABLE investigations (id INTEGER PRIMARY KEY, kind TEXT NOT NULL, alert TEXT NOT NULL DEFAULT '',
		rule TEXT NOT NULL DEFAULT '', key TEXT NOT NULL DEFAULT '', title TEXT NOT NULL, severity TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL, claimed_by TEXT NOT NULL DEFAULT '', claim_expires INTEGER NOT NULL DEFAULT 0,
		opened INTEGER NOT NULL, closed INTEGER NOT NULL DEFAULT 0, alert_resolved INTEGER NOT NULL DEFAULT 0,
		summary TEXT NOT NULL DEFAULT '', fix TEXT NOT NULL DEFAULT '', runbook TEXT NOT NULL DEFAULT '',
		outcome TEXT NOT NULL DEFAULT '', outcome_at INTEGER NOT NULL DEFAULT 0, previous INTEGER NOT NULL DEFAULT 0);
	CREATE INDEX investigations_status ON investigations (status, id);
	CREATE INDEX investigations_alert ON investigations (alert, id);
	CREATE TABLE investigation_events (id INTEGER PRIMARY KEY,
		investigation INTEGER NOT NULL REFERENCES investigations (id) ON DELETE CASCADE,
		time INTEGER NOT NULL, actor TEXT NOT NULL, kind TEXT NOT NULL, text TEXT NOT NULL,
		action TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '', namespace TEXT NOT NULL DEFAULT '',
		target TEXT NOT NULL DEFAULT '', disruptive INTEGER NOT NULL DEFAULT 0, result TEXT NOT NULL DEFAULT '');
	CREATE INDEX investigation_events_of ON investigation_events (investigation, id);
	CREATE INDEX investigation_events_kind ON investigation_events (kind, time);`,
}

// DB is tremolo's database.
type DB struct {
	sql *sql.DB
	dir string
}

// Open opens dir/tremolo.db, creating it and bringing its schema up to date.
// An empty dir keeps everything in memory.
//
// The data directory may be on NFS, so the database uses a rollback journal
// rather than WAL (which needs shared memory), and only one tremolo may use it
// at a time.
func Open(dir string) (*DB, error) {
	// Foreign keys are off by default in SQLite; cascades rely on them.
	dsn := "file::memory:?_pragma=foreign_keys(1)"
	if dir != "" {
		dsn = "file:" + filepath.Join(dir, File) +
			"?_pragma=foreign_keys(1)&_pragma=journal_mode(DELETE)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: writes are serialised, and an in-memory database lives
	// as long as its connection.
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	d := &DB{sql: db, dir: dir}
	if err := d.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%s: %w", d.path(), err)
	}
	return d, nil
}

func (d *DB) path() string {
	if d.dir == "" {
		return "in-memory database"
	}
	return filepath.Join(d.dir, File)
}

func (d *DB) migrate() error {
	var version int
	if err := d.sql.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("schema version %d is newer than this tremolo knows (%d); upgrade tremolo", version, len(migrations))
	}
	// Before changing an existing database's schema, keep a copy as it was, so a
	// bad upgrade is one file copy away.
	if d.dir != "" && version > 0 && version < len(migrations) {
		before := filepath.Join(d.dir, fmt.Sprintf("tremolo-schema-%d.db", version))
		if _, err := os.Stat(before); errors.Is(err, os.ErrNotExist) {
			if _, err := d.sql.Exec(`VACUUM INTO ?`, before); err != nil {
				return fmt.Errorf("copying the database before migrating: %w", err)
			}
		}
	}
	for i := version; i < len(migrations); i++ {
		tx, err := d.sql.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Check reads the database, to show it answers.
func (d *DB) Check(ctx context.Context) error {
	var v int
	return d.sql.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v)
}

// Close closes the database.
func (d *DB) Close() error { return d.sql.Close() }

// SQL is the database, for packages that keep their own tables in it.
func (d *DB) SQL() *sql.DB { return d.sql }

// Prune deletes alert history and audit entries older than keep.
func (d *DB) Prune(ctx context.Context, keep time.Duration) (int64, error) {
	cut := time.Now().Add(-keep).UnixMilli()
	var n int64
	for _, q := range []string{
		`DELETE FROM audit WHERE time < ?`, `DELETE FROM alert_history WHERE resolved < ?`,
		`DELETE FROM investigations WHERE closed > 0 AND closed < ?`,
	} {
		res, err := d.sql.ExecContext(ctx, q, cut)
		if err != nil {
			return n, err
		}
		c, _ := res.RowsAffected()
		n += c
	}
	return n, nil
}

// legacy reads a JSON file an earlier tremolo kept in the data directory.
func (d *DB) legacy(name string) ([]byte, string, bool) {
	if d.dir == "" {
		return nil, "", false
	}
	p := filepath.Join(d.dir, name+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", false
	}
	return b, p, true
}
