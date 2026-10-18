package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ======================= config =======================

// GetConfig returns the value for key, or "" and false if unset.
func (d *DB) GetConfig(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := d.QueryRowContext(ctx, `SELECT value FROM config WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetConfig upserts a config key.
func (d *DB) SetConfig(ctx context.Context, key, value string) error {
	_, err := d.ExecContext(ctx, `INSERT INTO config(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, time.Now().Unix())
	return err
}

// DeleteConfig removes a config key.
func (d *DB) DeleteConfig(ctx context.Context, key string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM config WHERE key=?`, key)
	return err
}

// ======================= devices =======================

const deviceCols = `id,name,host,port,type,enabled,parent_id,grp,notes,poll_interval_s,created_at,updated_at`

func scanDevice(s interface{ Scan(...any) error }) (*Device, error) {
	var dv Device
	var parent sql.NullInt64
	var grp sql.NullString
	var enabled int
	if err := s.Scan(&dv.ID, &dv.Name, &dv.Host, &dv.Port, &dv.Type, &enabled, &parent, &grp,
		&dv.Notes, &dv.PollIntervalS, &dv.CreatedAt, &dv.UpdatedAt); err != nil {
		return nil, err
	}
	dv.Enabled = enabled == 1
	dv.ParentID = ptrInt(parent)
	dv.Group = ptrStr(grp)
	return &dv, nil
}

// ListDevices returns all devices ordered by name.
func (d *DB) ListDevices(ctx context.Context) ([]*Device, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+deviceCols+` FROM devices ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Device{}
	for rows.Next() {
		dv, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dv)
	}
	return out, rows.Err()
}

// GetDevice fetches one device by id.
func (d *DB) GetDevice(ctx context.Context, id int64) (*Device, error) {
	dv, err := scanDevice(d.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return dv, err
}

// CreateDevice inserts dv and fills ID/timestamps.
func (d *DB) CreateDevice(ctx context.Context, dv *Device) error {
	now := time.Now().Unix()
	dv.CreatedAt, dv.UpdatedAt = now, now
	res, err := d.ExecContext(ctx, `INSERT INTO devices(name,host,port,type,enabled,parent_id,grp,notes,poll_interval_s,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		dv.Name, dv.Host, dv.Port, dv.Type, boolInt(dv.Enabled), nullInt(dv.ParentID), nullStr(dv.Group),
		dv.Notes, dv.PollIntervalS, now, now)
	if err != nil {
		return err
	}
	dv.ID, err = res.LastInsertId()
	return err
}

// UpdateDevice saves all mutable fields of dv.
func (d *DB) UpdateDevice(ctx context.Context, dv *Device) error {
	dv.UpdatedAt = time.Now().Unix()
	res, err := d.ExecContext(ctx, `UPDATE devices SET name=?,host=?,port=?,type=?,enabled=?,parent_id=?,grp=?,notes=?,poll_interval_s=?,updated_at=? WHERE id=?`,
		dv.Name, dv.Host, dv.Port, dv.Type, boolInt(dv.Enabled), nullInt(dv.ParentID), nullStr(dv.Group),
		dv.Notes, dv.PollIntervalS, dv.UpdatedAt, dv.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteDevice removes a device (cascades to credentials, alert state, metrics).
func (d *DB) DeleteDevice(ctx context.Context, id int64) error {
	res, err := d.ExecContext(ctx, `DELETE FROM devices WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ======================= credentials =======================

// GetCredentialRow returns the raw (encrypted) credential row.
func (d *DB) GetCredentialRow(ctx context.Context, deviceID int64) (kind, username string, secretEnc []byte, updatedAt int64, err error) {
	err = d.QueryRowContext(ctx, `SELECT kind, username, secret_enc, updated_at FROM credentials WHERE device_id=?`, deviceID).
		Scan(&kind, &username, &secretEnc, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

// PutCredentialRow upserts an encrypted credential row.
func (d *DB) PutCredentialRow(ctx context.Context, deviceID int64, kind, username string, secretEnc []byte) error {
	_, err := d.ExecContext(ctx, `INSERT INTO credentials(device_id,kind,username,secret_enc,updated_at) VALUES(?,?,?,?,?)
		ON CONFLICT(device_id) DO UPDATE SET kind=excluded.kind, username=excluded.username, secret_enc=excluded.secret_enc, updated_at=excluded.updated_at`,
		deviceID, kind, username, secretEnc, time.Now().Unix())
	return err
}

// DeleteCredential removes a device's credential.
func (d *DB) DeleteCredential(ctx context.Context, deviceID int64) error {
	_, err := d.ExecContext(ctx, `DELETE FROM credentials WHERE device_id=?`, deviceID)
	return err
}

// ======================= sessions =======================

// CreateSession stores a session keyed by the SHA-256 hash of its token.
func (d *DB) CreateSession(ctx context.Context, tokenHash, ip string, ttl time.Duration) error {
	now := time.Now()
	_, err := d.ExecContext(ctx, `INSERT INTO sessions(token_hash,created_at,expires_at,ip) VALUES(?,?,?,?)`,
		tokenHash, now.Unix(), now.Add(ttl).Unix(), ip)
	return err
}

// SessionValid reports whether an unexpired session exists.
func (d *DB) SessionValid(ctx context.Context, tokenHash string) (bool, error) {
	var exp int64
	err := d.QueryRowContext(ctx, `SELECT expires_at FROM sessions WHERE token_hash=?`, tokenHash).Scan(&exp)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return exp > time.Now().Unix(), nil
}

// DeleteSession removes one session.
func (d *DB) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash)
	return err
}

// DeleteAllSessions logs everyone out (used after a password change).
func (d *DB) DeleteAllSessions(ctx context.Context) error {
	_, err := d.ExecContext(ctx, `DELETE FROM sessions`)
	return err
}

// ======================= alert rules =======================

const ruleCols = `id,name,device_id,grp,device_type,metric,operator,threshold,duration_s,resolve_threshold,severity,enabled,cooldown_s,created_at,updated_at`

func scanRule(s interface{ Scan(...any) error }) (*AlertRule, error) {
	var r AlertRule
	var dev sql.NullInt64
	var grp, dtype sql.NullString
	var res sql.NullFloat64
	var enabled int
	if err := s.Scan(&r.ID, &r.Name, &dev, &grp, &dtype, &r.Metric, &r.Operator, &r.Threshold, &r.DurationS,
		&res, &r.Severity, &enabled, &r.CooldownS, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.DeviceID, r.Group, r.DeviceType, r.ResolveThreshold = ptrInt(dev), ptrStr(grp), ptrStr(dtype), ptrFloat(res)
	r.Enabled = enabled == 1
	return &r, nil
}

// ListAlertRules returns all rules.
func (d *DB) ListAlertRules(ctx context.Context) ([]*AlertRule, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+ruleCols+` FROM alert_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AlertRule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetAlertRule fetches one rule.
func (d *DB) GetAlertRule(ctx context.Context, id int64) (*AlertRule, error) {
	r, err := scanRule(d.QueryRowContext(ctx, `SELECT `+ruleCols+` FROM alert_rules WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// CreateAlertRule inserts a rule.
func (d *DB) CreateAlertRule(ctx context.Context, r *AlertRule) error {
	now := time.Now().Unix()
	r.CreatedAt, r.UpdatedAt = now, now
	res, err := d.ExecContext(ctx, `INSERT INTO alert_rules(name,device_id,grp,device_type,metric,operator,threshold,duration_s,resolve_threshold,severity,enabled,cooldown_s,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.Name, nullInt(r.DeviceID), nullStr(r.Group), nullStr(r.DeviceType), r.Metric, r.Operator, r.Threshold, r.DurationS,
		nullFloat(r.ResolveThreshold), r.Severity, boolInt(r.Enabled), r.CooldownS, now, now)
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

// UpdateAlertRule saves a rule.
func (d *DB) UpdateAlertRule(ctx context.Context, r *AlertRule) error {
	r.UpdatedAt = time.Now().Unix()
	res, err := d.ExecContext(ctx, `UPDATE alert_rules SET name=?,device_id=?,grp=?,device_type=?,metric=?,operator=?,threshold=?,duration_s=?,resolve_threshold=?,severity=?,enabled=?,cooldown_s=?,updated_at=? WHERE id=?`,
		r.Name, nullInt(r.DeviceID), nullStr(r.Group), nullStr(r.DeviceType), r.Metric, r.Operator, r.Threshold, r.DurationS,
		nullFloat(r.ResolveThreshold), r.Severity, boolInt(r.Enabled), r.CooldownS, r.UpdatedAt, r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAlertRule removes a rule and its state rows.
func (d *DB) DeleteAlertRule(ctx context.Context, id int64) error {
	res, err := d.ExecContext(ctx, `DELETE FROM alert_rules WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ======================= alert state =======================

const stateCols = `s.id,s.rule_id,s.device_id,s.state,s.value,s.pending_since,s.fired_at,s.resolved_at,s.last_eval_at,s.acked_at,s.muted_until,s.suppressed,s.last_notified_at,
	COALESCE(r.name,''),COALESCE(d.name,''),COALESCE(r.metric,''),COALESCE(r.severity,'')`

func scanState(sc interface{ Scan(...any) error }) (*AlertState, error) {
	var s AlertState
	var val sql.NullFloat64
	var ps, fa, ra, aa, mu, ln sql.NullInt64
	var sup int
	if err := sc.Scan(&s.ID, &s.RuleID, &s.DeviceID, &s.State, &val, &ps, &fa, &ra, &s.LastEvalAt, &aa, &mu, &sup, &ln,
		&s.RuleName, &s.DeviceName, &s.Metric, &s.Severity); err != nil {
		return nil, err
	}
	s.Value, s.PendingSince, s.FiredAt, s.ResolvedAt = ptrFloat(val), ptrInt(ps), ptrInt(fa), ptrInt(ra)
	s.AckedAt, s.MutedUntil, s.LastNotifiedAt = ptrInt(aa), ptrInt(mu), ptrInt(ln)
	s.Suppressed = sup == 1
	return &s, nil
}

// ListAlertStates returns state rows, optionally filtered to the given states.
func (d *DB) ListAlertStates(ctx context.Context, states ...string) ([]*AlertState, error) {
	q := `SELECT ` + stateCols + ` FROM alert_state s
		LEFT JOIN alert_rules r ON r.id=s.rule_id LEFT JOIN devices d ON d.id=s.device_id`
	args := []any{}
	if len(states) > 0 {
		q += ` WHERE s.state IN (`
		for i, st := range states {
			if i > 0 {
				q += ","
			}
			q += "?"
			args = append(args, st)
		}
		q += `)`
	}
	q += ` ORDER BY s.fired_at DESC, s.pending_since DESC`
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AlertState{}
	for rows.Next() {
		s, err := scanState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetAlertState fetches one state row by id.
func (d *DB) GetAlertState(ctx context.Context, id int64) (*AlertState, error) {
	s, err := scanState(d.QueryRowContext(ctx, `SELECT `+stateCols+` FROM alert_state s
		LEFT JOIN alert_rules r ON r.id=s.rule_id LEFT JOIN devices d ON d.id=s.device_id WHERE s.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

// UpsertAlertState writes the full state row for (rule_id, device_id) and
// sets s.ID.
func (d *DB) UpsertAlertState(ctx context.Context, s *AlertState) error {
	err := d.QueryRowContext(ctx, `INSERT INTO alert_state
		(rule_id,device_id,state,value,pending_since,fired_at,resolved_at,last_eval_at,acked_at,muted_until,suppressed,last_notified_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(rule_id,device_id) DO UPDATE SET state=excluded.state, value=excluded.value,
			pending_since=excluded.pending_since, fired_at=excluded.fired_at, resolved_at=excluded.resolved_at,
			last_eval_at=excluded.last_eval_at, acked_at=excluded.acked_at, muted_until=excluded.muted_until,
			suppressed=excluded.suppressed, last_notified_at=excluded.last_notified_at
		RETURNING id`,
		s.RuleID, s.DeviceID, s.State, nullFloat(s.Value), nullInt(s.PendingSince), nullInt(s.FiredAt), nullInt(s.ResolvedAt),
		s.LastEvalAt, nullInt(s.AckedAt), nullInt(s.MutedUntil), boolInt(s.Suppressed), nullInt(s.LastNotifiedAt)).Scan(&s.ID)
	return err
}

// DeleteAlertState removes a state row (e.g. its rule no longer applies).
func (d *DB) DeleteAlertState(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `DELETE FROM alert_state WHERE id=?`, id)
	return err
}

// ======================= alert history =======================

// InsertAlertHistory records a resolved alert.
func (d *DB) InsertAlertHistory(ctx context.Context, h *AlertHistory) error {
	_, err := d.ExecContext(ctx, `INSERT INTO alert_history(rule_id,device_id,rule_name,device_name,metric,severity,peak_value,fired_at,resolved_at,acked)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, nullInt(h.RuleID), nullInt(h.DeviceID), h.RuleName, h.DeviceName, h.Metric, h.Severity,
		nullFloat(h.PeakValue), h.FiredAt, h.ResolvedAt, boolInt(h.Acked))
	return err
}

// ListAlertHistory returns a page of resolved alerts plus the total count.
func (d *DB) ListAlertHistory(ctx context.Context, limit, offset int) ([]*AlertHistory, int, error) {
	var total int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM alert_history`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := d.QueryContext(ctx, `SELECT id,rule_id,device_id,rule_name,device_name,metric,severity,peak_value,fired_at,resolved_at,acked
		FROM alert_history ORDER BY resolved_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*AlertHistory{}
	for rows.Next() {
		var h AlertHistory
		var rid, did sql.NullInt64
		var peak sql.NullFloat64
		var acked int
		if err := rows.Scan(&h.ID, &rid, &did, &h.RuleName, &h.DeviceName, &h.Metric, &h.Severity, &peak, &h.FiredAt, &h.ResolvedAt, &acked); err != nil {
			return nil, 0, err
		}
		h.RuleID, h.DeviceID, h.PeakValue, h.Acked = ptrInt(rid), ptrInt(did), ptrFloat(peak), acked == 1
		out = append(out, &h)
	}
	return out, total, rows.Err()
}

// ======================= audit log =======================

// InsertAudit appends to the audit log.
func (d *DB) InsertAudit(ctx context.Context, deviceID *int64, action, payload, sourceIP string) error {
	if payload == "" {
		payload = "{}"
	}
	_, err := d.ExecContext(ctx, `INSERT INTO audit_log(ts,device_id,action,payload,source_ip) VALUES(?,?,?,?,?)`,
		time.Now().Unix(), nullInt(deviceID), action, payload, sourceIP)
	return err
}

// ListAudit returns a page of audit entries (newest first) and the total.
func (d *DB) ListAudit(ctx context.Context, limit, offset int) ([]*AuditEntry, int, error) {
	var total int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM audit_log`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := d.QueryContext(ctx, `SELECT id,ts,device_id,action,payload,source_ip FROM audit_log ORDER BY ts DESC, id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*AuditEntry{}
	for rows.Next() {
		var a AuditEntry
		var did sql.NullInt64
		if err := rows.Scan(&a.ID, &a.Ts, &did, &a.Action, &a.Payload, &a.SourceIP); err != nil {
			return nil, 0, err
		}
		a.DeviceID = ptrInt(did)
		out = append(out, &a)
	}
	return out, total, rows.Err()
}

// ======================= metrics =======================

// InsertMetrics1m writes a batch of 1-minute aggregates for one device.
func (d *DB) InsertMetrics1m(ctx context.Context, deviceID int64, points []MetricPoint) error {
	if len(points) == 0 {
		return nil
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO metrics_1m(device_id,ts,metric,avg,min,max) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, p := range points {
		if _, err := stmt.ExecContext(ctx, deviceID, p.Ts, p.Metric, p.Avg, p.Min, p.Max); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ListMetrics1m returns aggregates for a device since `since` (unix secs).
func (d *DB) ListMetrics1m(ctx context.Context, deviceID, since int64) ([]MetricPoint, error) {
	rows, err := d.QueryContext(ctx, `SELECT ts,metric,avg,min,max FROM metrics_1m WHERE device_id=? AND ts>=? ORDER BY ts`, deviceID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MetricPoint{}
	for rows.Next() {
		var p MetricPoint
		if err := rows.Scan(&p.Ts, &p.Metric, &p.Avg, &p.Min, &p.Max); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Prune applies the retention policy: raw 1h, 1-min averages 24h, resolved
// alert history 90 days, expired sessions.
func (d *DB) Prune(ctx context.Context, now time.Time) error {
	stmts := []struct {
		q   string
		arg int64
	}{
		{`DELETE FROM metrics_raw WHERE ts < ?`, now.Add(-1 * time.Hour).Unix()},
		{`DELETE FROM metrics_1m WHERE ts < ?`, now.Add(-24 * time.Hour).Unix()},
		{`DELETE FROM alert_history WHERE resolved_at < ?`, now.Add(-90 * 24 * time.Hour).Unix()},
		{`DELETE FROM sessions WHERE expires_at < ?`, now.Unix()},
	}
	for _, s := range stmts {
		if _, err := d.ExecContext(ctx, s.q, s.arg); err != nil {
			return err
		}
	}
	return nil
}
