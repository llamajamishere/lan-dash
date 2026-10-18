package db

import (
	"context"
	"database/sql"
	"time"
)

// migration is a single, append-only schema change. Never edit a released
// migration; add a new one instead so upgrades stay deterministic.
type migration struct {
	version int
	name    string
	apply   func(ctx context.Context, tx *sql.Tx) error
}

func execAll(stmts ...string) func(ctx context.Context, tx *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		for _, s := range stmts {
			if _, err := tx.ExecContext(ctx, s); err != nil {
				return err
			}
		}
		return nil
	}
}

var migrations = []migration{
	{1, "initial schema", execAll(
		`CREATE TABLE config (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE devices (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			name            TEXT NOT NULL,
			host            TEXT NOT NULL,
			port            INTEGER NOT NULL DEFAULT 0,
			type            TEXT NOT NULL CHECK (type IN ('ubuntu','windows','hue','blink','plug','ping')),
			enabled         INTEGER NOT NULL DEFAULT 1,
			parent_id       INTEGER REFERENCES devices(id) ON DELETE SET NULL,
			grp             TEXT,
			notes           TEXT NOT NULL DEFAULT '',
			poll_interval_s INTEGER NOT NULL DEFAULT 15,
			created_at      INTEGER NOT NULL,
			updated_at      INTEGER NOT NULL
		)`,
		// One credential blob per device. username is not secret; the secret
		// part (password / private key / passphrase) is AES-256-GCM encrypted.
		`CREATE TABLE credentials (
			device_id  INTEGER PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
			kind       TEXT NOT NULL DEFAULT 'ssh',
			username   TEXT NOT NULL DEFAULT '',
			secret_enc BLOB,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE sessions (
			token_hash TEXT PRIMARY KEY,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			ip         TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE alert_rules (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			name              TEXT NOT NULL,
			device_id         INTEGER REFERENCES devices(id) ON DELETE CASCADE,
			grp               TEXT,
			device_type       TEXT,
			metric            TEXT NOT NULL,
			operator          TEXT NOT NULL CHECK (operator IN ('gt','lt','eq')),
			threshold         REAL NOT NULL,
			duration_s        INTEGER NOT NULL DEFAULT 0,
			resolve_threshold REAL,
			severity          TEXT NOT NULL CHECK (severity IN ('info','warning','critical')),
			enabled           INTEGER NOT NULL DEFAULT 1,
			cooldown_s        INTEGER NOT NULL DEFAULT 300,
			created_at        INTEGER NOT NULL,
			updated_at        INTEGER NOT NULL
		)`,
		`CREATE TABLE alert_state (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			rule_id          INTEGER NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
			device_id        INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
			state            TEXT NOT NULL,
			value            REAL,
			pending_since    INTEGER,
			fired_at         INTEGER,
			resolved_at      INTEGER,
			last_eval_at     INTEGER NOT NULL,
			acked_at         INTEGER,
			muted_until      INTEGER,
			suppressed       INTEGER NOT NULL DEFAULT 0,
			last_notified_at INTEGER,
			UNIQUE(rule_id, device_id)
		)`,
		`CREATE TABLE alert_history (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			rule_id     INTEGER,
			device_id   INTEGER,
			rule_name   TEXT NOT NULL,
			device_name TEXT NOT NULL,
			metric      TEXT NOT NULL,
			severity    TEXT NOT NULL,
			peak_value  REAL,
			fired_at    INTEGER NOT NULL,
			resolved_at INTEGER NOT NULL,
			acked       INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX idx_alert_history_resolved ON alert_history(resolved_at DESC)`,
		`CREATE TABLE audit_log (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			ts        INTEGER NOT NULL,
			device_id INTEGER,
			action    TEXT NOT NULL,
			payload   TEXT NOT NULL DEFAULT '{}',
			source_ip TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX idx_audit_ts ON audit_log(ts DESC)`,
		// 1-minute aggregates, narrow layout so new metrics need no migration.
		`CREATE TABLE metrics_1m (
			device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
			ts        INTEGER NOT NULL,
			metric    TEXT NOT NULL,
			avg       REAL NOT NULL,
			min       REAL NOT NULL,
			max       REAL NOT NULL,
			PRIMARY KEY (device_id, metric, ts)
		)`,
		`CREATE INDEX idx_metrics_1m_ts ON metrics_1m(ts)`,
		// Reserved for optional raw-sample persistence; live raw data is kept
		// in the in-memory ring buffer. The pruner keeps this to 1 hour.
		`CREATE TABLE metrics_raw (
			device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
			ts        INTEGER NOT NULL,
			metric    TEXT NOT NULL,
			value     REAL NOT NULL,
			PRIMARY KEY (device_id, metric, ts)
		)`,
		`CREATE INDEX idx_metrics_raw_ts ON metrics_raw(ts)`,
	)},
	{2, "default alert rules", installDefaultRules},
}

// installDefaultRules seeds the rule set only when the table is empty so a
// user who deleted defaults never gets them back on upgrade.
func installDefaultRules(ctx context.Context, tx *sql.Tx) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM alert_rules`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	now := time.Now().Unix()
	f := func(v float64) *float64 { return &v }
	s := func(v string) *string { return &v }
	defaults := []AlertRule{
		{Name: "Device offline", Metric: "device_offline", Operator: "eq", Threshold: 1, DurationS: 60, Severity: "critical", Enabled: true, CooldownS: 300},
		{Name: "Disk usage above 85%", Metric: "disk_pct", Operator: "gt", Threshold: 85, ResolveThreshold: f(80), DurationS: 60, Severity: "warning", Enabled: true, CooldownS: 900},
		{Name: "RAM above 90%", Metric: "mem_pct", Operator: "gt", Threshold: 90, ResolveThreshold: f(85), DurationS: 60, Severity: "warning", Enabled: true, CooldownS: 600},
		{Name: "CPU above 95% for 5 min", Metric: "cpu_pct", Operator: "gt", Threshold: 95, ResolveThreshold: f(85), DurationS: 300, Severity: "warning", Enabled: true, CooldownS: 600},
		{Name: "High temperature", Metric: "temp_c", Operator: "gt", Threshold: 80, ResolveThreshold: f(75), DurationS: 60, Severity: "critical", Enabled: true, CooldownS: 600},
		{Name: "SSH host key changed", Metric: "ssh_key_changed", Operator: "eq", Threshold: 1, DurationS: 0, Severity: "critical", Enabled: true, CooldownS: 0},
		// Stubs for collectors arriving in later phases (Hue = phase 5,
		// Blink = phase 7). Disabled until those collectors exist.
		{Name: "Hue bridge offline", DeviceType: s("hue"), Metric: "device_offline", Operator: "eq", Threshold: 1, DurationS: 120, Severity: "warning", Enabled: false, CooldownS: 600},
		{Name: "Camera offline", DeviceType: s("blink"), Metric: "device_offline", Operator: "eq", Threshold: 1, DurationS: 300, Severity: "warning", Enabled: false, CooldownS: 600},
		{Name: "Low battery", Metric: "battery_pct", Operator: "lt", Threshold: 20, ResolveThreshold: f(30), DurationS: 0, Severity: "warning", Enabled: false, CooldownS: 3600},
	}
	for _, r := range defaults {
		if _, err := tx.ExecContext(ctx, `INSERT INTO alert_rules
			(name, device_id, grp, device_type, metric, operator, threshold, duration_s, resolve_threshold, severity, enabled, cooldown_s, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.Name, nil, nil, nullStr(r.DeviceType), r.Metric, r.Operator, r.Threshold, r.DurationS,
			nullFloat(r.ResolveThreshold), r.Severity, boolInt(r.Enabled), r.CooldownS, now, now); err != nil {
			return err
		}
	}
	return nil
}
