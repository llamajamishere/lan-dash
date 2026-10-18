// Package db owns the SQLite database: opening (WAL mode), backups,
// schema migrations and typed query helpers for every table.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

// DB wraps *sql.DB with the application's query helpers.
type DB struct {
	*sql.DB
	Path string
}

// ErrNotFound is returned by getters when no row matches.
var ErrNotFound = errors.New("not found")

// Open opens (creating if needed) the SQLite database at path in WAL mode.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows a single writer; a small pool plus busy_timeout keeps
	// concurrent readers fast without "database is locked" errors.
	sqldb.SetMaxOpenConns(4)
	sqldb.SetConnMaxIdleTime(5 * time.Minute)
	if err := sqldb.Ping(); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return &DB{DB: sqldb, Path: path}, nil
}

// Backup writes a consistent copy of the database into dir using
// VACUUM INTO and keeps only the newest `keep` backups. It is a no-op for a
// brand-new (empty) database.
func (d *DB) Backup(ctx context.Context, dir string, keep int) (string, error) {
	var n int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table'`).Scan(&n); err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil // fresh install, nothing to back up
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, fmt.Sprintf("landash-%s.db", time.Now().UTC().Format("20060102-150405")))
	if _, err := d.ExecContext(ctx, `VACUUM INTO ?`, dest); err != nil {
		return "", fmt.Errorf("vacuum into: %w", err)
	}
	_ = os.Chmod(dest, 0o600)

	// Rotate old backups.
	matches, _ := filepath.Glob(filepath.Join(dir, "landash-*.db"))
	sort.Strings(matches)
	for len(matches) > keep {
		_ = os.Remove(matches[0])
		matches = matches[1:]
	}
	return dest, nil
}

// Migrate applies all pending migrations in order inside transactions.
func (d *DB) Migrate(ctx context.Context) error {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	var current int
	if err := d.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := m.apply(ctx, tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?,?)`, m.version, time.Now().Unix()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		slog.Info("applied migration", "version", m.version, "name", m.name)
	}
	return nil
}

// SchemaVersion returns the latest applied migration version.
func (d *DB) SchemaVersion(ctx context.Context) int {
	var v int
	_ = d.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&v)
	return v
}

// SizeBytes returns the on-disk size of the database including WAL.
func (d *DB) SizeBytes() int64 {
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if fi, err := os.Stat(d.Path + suffix); err == nil {
			total += fi.Size()
		}
	}
	return total
}

// ---------- small helpers ----------

func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullStr(p *string) any {
	if p == nil || strings.TrimSpace(*p) == "" {
		return nil
	}
	return *p
}

func ptrInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func ptrFloat(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func ptrStr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
