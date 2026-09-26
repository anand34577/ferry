// Package db wraps database/sql with one portable SQL dialect for SQLite and PostgreSQL.
// Queries are written with '?' placeholders and rebound to $n for Postgres.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type DB struct {
	sql     *sql.DB
	Dialect string // "sqlite" | "postgres"
}

// Q is implemented by both *DB and *Tx.
type Q interface {
	Exec(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryRow(ctx context.Context, q string, args ...any) *sql.Row
	Query(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

var ErrNoRows = sql.ErrNoRows

func Open(driver, dsn string) (*DB, error) {
	d := &DB{Dialect: driver}
	var err error
	switch driver {
	case "sqlite":
		if dir := filepath.Dir(dsn); dir != "" {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, fmt.Errorf("create database directory: %w", err)
			}
		}
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + "_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
		d.sql, err = sql.Open("sqlite", dsn)
		if err != nil {
			return nil, err
		}
		// SQLite allows one writer; a single connection removes SQLITE_BUSY entirely.
		// Ceiling is a few thousand small queries/s — switch to Postgres beyond a small team.
		d.sql.SetMaxOpenConns(1)
	case "postgres":
		d.sql, err = sql.Open("pgx", dsn)
		if err != nil {
			return nil, err
		}
		d.sql.SetMaxOpenConns(20)
		d.sql.SetConnMaxIdleTime(5 * time.Minute)
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := d.sql.PingContext(ctx); err != nil {
		d.sql.Close()
		return nil, fmt.Errorf("connect to %s database: %w", driver, err)
	}
	return d, nil
}

func (d *DB) Close() error                   { return d.sql.Close() }
func (d *DB) Ping(ctx context.Context) error { return d.sql.PingContext(ctx) }

func (d *DB) rebind(q string) string {
	if d.Dialect != "postgres" || !strings.Contains(q, "?") {
		return q
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		} else {
			b.WriteByte(q[i])
		}
	}
	return b.String()
}

func (d *DB) Exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return d.sql.ExecContext(ctx, d.rebind(q), args...)
}
func (d *DB) QueryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return d.sql.QueryRowContext(ctx, d.rebind(q), args...)
}
func (d *DB) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return d.sql.QueryContext(ctx, d.rebind(q), args...)
}

type Tx struct {
	tx *sql.Tx
	d  *DB
}

func (t *Tx) Exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, t.d.rebind(q), args...)
}
func (t *Tx) QueryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, t.d.rebind(q), args...)
}
func (t *Tx) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, t.d.rebind(q), args...)
}

// InTx runs fn in a transaction, committing on nil error.
func (d *DB) InTx(ctx context.Context, fn func(tx *Tx) error) error {
	t, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&Tx{tx: t, d: d}); err != nil {
		t.Rollback()
		return err
	}
	return t.Commit()
}

// IsUnique reports whether err is a unique-constraint violation in either dialect.
func IsUnique(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "UNIQUE constraint failed") || strings.Contains(s, "SQLSTATE 23505") || strings.Contains(s, "duplicate key")
}

func IsNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// Migrate applies pending migrations in order.
func (d *DB) Migrate(ctx context.Context) error {
	if _, err := d.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at BIGINT NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	var cur int
	if err := d.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&cur); err != nil {
		return err
	}
	for i, m := range migrations {
		v := i + 1
		if v <= cur {
			continue
		}
		err := d.InTx(ctx, func(tx *Tx) error {
			for _, stmt := range strings.Split(m, ";") {
				if strings.TrimSpace(stmt) == "" {
					continue
				}
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return fmt.Errorf("%w\nstatement: %s", err, strings.TrimSpace(stmt))
				}
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, v, time.Now().UnixMilli())
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %d: %w", v, err)
		}
	}
	return nil
}

// Portable DDL only: TEXT, BIGINT, INTEGER. Booleans are INTEGER 0/1, times are unix ms.
// "Root" parent/folder is the empty string rather than NULL so UNIQUE constraints work in both DBs.
var migrations = []string{`
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL,
  disabled INTEGER NOT NULL DEFAULT 0,
  quota_bytes BIGINT NOT NULL DEFAULT -1,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL
);

CREATE TABLE devices (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  platform TEXT NOT NULL,
  app_version TEXT NOT NULL DEFAULT '',
  fingerprint TEXT NOT NULL DEFAULT '',
  lan_addrs TEXT NOT NULL DEFAULT '',
  lan_port INTEGER NOT NULL DEFAULT 0,
  lan_protocol TEXT NOT NULL DEFAULT '',
  presence_at BIGINT NOT NULL DEFAULT 0,
  last_seen BIGINT NOT NULL,
  created_at BIGINT NOT NULL
);
CREATE INDEX devices_user ON devices(user_id);

CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id TEXT NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL,
  expires_at BIGINT NOT NULL,
  last_seen BIGINT NOT NULL,
  ip TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE folders (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  parent_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  UNIQUE (user_id, parent_id, name)
);

CREATE TABLE files (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  folder_id TEXT NOT NULL DEFAULT '',
  transfer_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  size BIGINT NOT NULL,
  mime TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  blob TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  UNIQUE (user_id, folder_id, transfer_id, name)
);
CREATE INDEX files_folder ON files(user_id, folder_id);

CREATE TABLE uploads (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  share_id TEXT NOT NULL DEFAULT '',
  folder_id TEXT NOT NULL DEFAULT '',
  transfer_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  size BIGINT NOT NULL,
  received BIGINT NOT NULL DEFAULT 0,
  conflict TEXT NOT NULL DEFAULT 'keep_both',
  hash_state TEXT NOT NULL DEFAULT '',
  client_sha256 TEXT NOT NULL DEFAULT '',
  uploader TEXT NOT NULL DEFAULT '',
  uploader_key TEXT NOT NULL DEFAULT '',
  file_id TEXT NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL
);
CREATE INDEX uploads_user ON uploads(user_id);
CREATE INDEX uploads_share ON uploads(share_id);

CREATE TABLE shares (
  id TEXT PRIMARY KEY,
  token TEXT NOT NULL UNIQUE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  message TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  expires_at BIGINT NOT NULL DEFAULT 0,
  max_downloads INTEGER NOT NULL DEFAULT 0,
  download_count INTEGER NOT NULL DEFAULT 0,
  allow_download INTEGER NOT NULL DEFAULT 1,
  allow_preview INTEGER NOT NULL DEFAULT 1,
  require_auth INTEGER NOT NULL DEFAULT 0,
  allow_list INTEGER NOT NULL DEFAULT 0,
  allow_delete INTEGER NOT NULL DEFAULT 0,
  max_file_bytes BIGINT NOT NULL DEFAULT 0,
  max_files INTEGER NOT NULL DEFAULT 0,
  allowed_types TEXT NOT NULL DEFAULT '',
  folder_id TEXT NOT NULL DEFAULT '',
  upload_count INTEGER NOT NULL DEFAULT 0,
  uploaded_bytes BIGINT NOT NULL DEFAULT 0,
  notify INTEGER NOT NULL DEFAULT 0,
  revoked INTEGER NOT NULL DEFAULT 0,
  last_access BIGINT NOT NULL DEFAULT 0,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL
);
CREATE INDEX shares_user ON shares(user_id);

CREATE TABLE share_items (
  share_id TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
  item_type TEXT NOT NULL,
  item_id TEXT NOT NULL,
  PRIMARY KEY (share_id, item_type, item_id)
);
CREATE INDEX share_items_item ON share_items(item_id);

CREATE TABLE download_sessions (
  id TEXT PRIMARY KEY,
  share_id TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
  created_at BIGINT NOT NULL,
  expires_at BIGINT NOT NULL,
  ip TEXT NOT NULL DEFAULT ''
);

CREATE TABLE transfers (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  direction TEXT NOT NULL,
  method TEXT NOT NULL,
  status TEXT NOT NULL,
  peer TEXT NOT NULL DEFAULT '',
  source_device_id TEXT NOT NULL DEFAULT '',
  target_device_id TEXT NOT NULL DEFAULT '',
  share_id TEXT NOT NULL DEFAULT '',
  file_count INTEGER NOT NULL DEFAULT 0,
  total_bytes BIGINT NOT NULL DEFAULT 0,
  bytes_done BIGINT NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  hidden INTEGER NOT NULL DEFAULT 0,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL
);
CREATE INDEX transfers_user ON transfers(user_id, created_at);
CREATE INDEX transfers_target ON transfers(target_device_id, status);

CREATE TABLE audit_log (
  id TEXT PRIMARY KEY,
  at BIGINT NOT NULL,
  user_id TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  ip TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_at ON audit_log(at)
`, `
CREATE INDEX files_blob ON files(blob);
CREATE INDEX uploads_transfer ON uploads(transfer_id)
`, `
ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN totp_last_step BIGINT NOT NULL DEFAULT 0;
CREATE TABLE password_resets (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at BIGINT NOT NULL,
  expires_at BIGINT NOT NULL
);
CREATE INDEX password_resets_user ON password_resets(user_id)
`, `
CREATE TABLE share_events (
  id TEXT PRIMARY KEY,
  share_id TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
  at BIGINT NOT NULL,
  kind TEXT NOT NULL,
  ip TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT '',
  referrer TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX share_events_share ON share_events(share_id, at);
CREATE INDEX share_events_at ON share_events(at);
CREATE INDEX audit_user ON audit_log(user_id, at);
ALTER TABLE shares ADD COLUMN short_url TEXT NOT NULL DEFAULT '';
ALTER TABLE shares ADD COLUMN short_id TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN gotify_url TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN gotify_token TEXT NOT NULL DEFAULT ''
`, `
CREATE TABLE user_identities (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  issuer TEXT NOT NULL,
  subject TEXT NOT NULL,
  email TEXT NOT NULL DEFAULT '',
  created_at BIGINT NOT NULL,
  last_login BIGINT NOT NULL DEFAULT 0,
  UNIQUE (issuer, subject)
);
CREATE INDEX user_identities_user ON user_identities(user_id)
`, `
ALTER TABLE users ADD COLUMN prefs TEXT NOT NULL DEFAULT '{}'
`, `
ALTER TABLE uploads ADD COLUMN concat TEXT NOT NULL DEFAULT '';
ALTER TABLE uploads ADD COLUMN crc32 BIGINT NOT NULL DEFAULT 0;
ALTER TABLE files ADD COLUMN crc32 BIGINT NOT NULL DEFAULT -1
`, `
ALTER TABLE download_sessions ADD COLUMN delivered TEXT NOT NULL DEFAULT ''
`}
