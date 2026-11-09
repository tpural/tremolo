// Package state keeps tremolo's own state in a SQLite database: accounts,
// alerts, silences, maintenance, alert history and the audit log.
package state

import (
	"context"
	"database/sql"
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
	dsn := ":memory:"
	if dir != "" {
		dsn = "file:" + filepath.Join(dir, File) +
			"?_pragma=journal_mode(DELETE)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)&_txlock=immediate"
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

// Close closes the database.
func (d *DB) Close() error { return d.sql.Close() }

// SQL is the database, for packages that keep their own tables in it.
func (d *DB) SQL() *sql.DB { return d.sql }

// Prune deletes alert history and audit entries older than keep.
func (d *DB) Prune(ctx context.Context, keep time.Duration) (int64, error) {
	cut := time.Now().Add(-keep).UnixMilli()
	var n int64
	for _, q := range []string{`DELETE FROM audit WHERE time < ?`, `DELETE FROM alert_history WHERE resolved < ?`} {
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
