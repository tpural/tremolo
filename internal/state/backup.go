package state

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tpural/tremolo/internal/victoria"
)

// BackupAgeMetric is how old the newest backup is, in seconds; since tremolo started when there's none.
const BackupAgeMetric = "tremolo_backup_age_seconds"

const backupPrefix, backupExt = "tremolo-", ".db"

// Backup writes a consistent copy of the database to dir, then deletes all but
// the newest keep copies. It's safe while tremolo runs.
func (d *DB) Backup(ctx context.Context, dir string, keep int, now time.Time) (string, error) {
	if d.dir == "" {
		return "", errors.New("the database is in memory")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	path := filepath.Join(dir, backupPrefix+now.UTC().Format("20060102-150405")+backupExt)
	// VACUUM INTO won't overwrite, and a half-written copy mustn't look like a backup.
	partial := path + ".partial"
	_ = os.Remove(partial)
	if _, err := d.sql.ExecContext(ctx, `VACUUM INTO ?`, partial); err != nil {
		_ = os.Remove(partial)
		return "", err
	}
	if err := os.Rename(partial, path); err != nil {
		return "", err
	}
	old, err := Backups(dir)
	if err != nil {
		return path, err
	}
	for len(old) > max(keep, 1) {
		if err := os.Remove(old[0]); err != nil {
			return path, err
		}
		old = old[1:]
	}
	return path, nil
}

// Backups lists the backups in dir, oldest first.
func Backups(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if n := e.Name(); e.Type().IsRegular() && strings.HasPrefix(n, backupPrefix) && strings.HasSuffix(n, backupExt) {
			out = append(out, filepath.Join(dir, n))
		}
	}
	// The names sort by time.
	slices.Sort(out)
	return out, nil
}

// BackupOptions configures RunBackups.
type BackupOptions struct {
	Dir   string
	Keep  int
	Every time.Duration
	// MetricsURL, when set, gets BackupAgeMetric every few minutes.
	MetricsURL *url.URL
	Client     *http.Client
	Log        *slog.Logger
}

// RunBackups backs the database up every opts.Every, counting from the newest
// backup so restarts don't add more, until ctx ends.
func (d *DB) RunBackups(ctx context.Context, opts BackupOptions) {
	started := time.Now()
	for {
		last, err := newest(opts.Dir)
		if err != nil {
			opts.Log.Warn("listing backups", "err", err)
		}
		if time.Since(last) >= opts.Every {
			path, err := d.Backup(ctx, opts.Dir, opts.Keep, time.Now())
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				opts.Log.Warn("backing up the database", "err", err)
			} else {
				opts.Log.Info("backed up the database", "file", path)
				last = time.Now()
			}
		}
		if opts.MetricsURL != nil {
			now := time.Now()
			age := now.Sub(last)
			if last.IsZero() {
				age = now.Sub(started)
			}
			s := victoria.Sample{Metric: BackupAgeMetric, Value: age.Seconds(), Time: now}
			if err := victoria.Import(ctx, opts.Client, opts.MetricsURL, []victoria.Sample{s}); err != nil && ctx.Err() == nil {
				opts.Log.Warn("writing backup age", "err", err)
			}
		}
		// Wake often enough to keep the metric fresh; a failed backup is retried then too.
		select {
		case <-ctx.Done():
			return
		case <-time.After(min(opts.Every, 5*time.Minute)):
		}
	}
}

// newest is when the newest backup in dir was made; zero when there's none.
func newest(dir string) (time.Time, error) {
	bs, err := Backups(dir)
	if err != nil || len(bs) == 0 {
		return time.Time{}, err
	}
	info, err := os.Stat(bs[len(bs)-1])
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}
