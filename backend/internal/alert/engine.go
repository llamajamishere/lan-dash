// Package alert implements the rule evaluation engine and the per
// (rule, device) state machine:
//
//	OK ──breach──▶ PENDING ──duration elapsed──▶ FIRING ──clears resolve threshold──▶ RESOLVED ──▶ OK
//	                  │ (breach ends) ▲
//	                  └──────▶ OK     └─ parent down / cooldown: held in PENDING
//
// A missing value (collector failing, stale data) evaluates to UNKNOWN:
// OK/PENDING rows move to UNKNOWN; FIRING rows stay FIRING so outages in
// data never masquerade as recovery. State is persisted in alert_state and
// reloaded on start, so firing alerts are never re-fired after a restart.
package alert

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/user/lan-dash/internal/collector"
	"github.com/user/lan-dash/internal/db"
	"github.com/user/lan-dash/internal/sse"
)

// EvalInterval is how often all rules are evaluated.
const EvalInterval = 10 * time.Second

// Metrics understood by the engine (exposed to the rules editor).
var Metrics = []string{"cpu_pct", "mem_pct", "swap_pct", "disk_pct", "temp_c", "load1",
	"net_rx_bps", "net_tx_bps", "latency_ms", "battery_pct", "device_offline", "ssh_key_changed"}

// StateSource provides live device state (implemented by collector.Manager).
type StateSource interface {
	State(id int64) (collector.DeviceState, bool)
}

type key struct{ rule, device int64 }

// Engine evaluates alert rules.
type Engine struct {
	db       *db.DB
	src      StateSource
	pub      sse.Publisher
	notifier Notifier

	mu     sync.Mutex
	states map[key]*db.AlertState
	peaks  map[key]float64
}

// NewEngine creates an engine.
func NewEngine(d *db.DB, src StateSource, pub sse.Publisher, n Notifier) *Engine {
	if n == nil {
		n = NoopNotifier{}
	}
	return &Engine{db: d, src: src, pub: pub, notifier: n, states: map[key]*db.AlertState{}, peaks: map[key]float64{}}
}

// Start reloads persisted state and runs the evaluation loop.
func (e *Engine) Start(ctx context.Context) error {
	rows, err := e.db.ListAlertStates(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	for _, s := range rows {
		e.states[key{s.RuleID, s.DeviceID}] = s
		if s.Value != nil {
			e.peaks[key{s.RuleID, s.DeviceID}] = *s.Value
		}
	}
	e.mu.Unlock()
	slog.Info("alert engine started", "restored_states", len(rows))
	go func() {
		t := time.NewTicker(EvalInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := e.Evaluate(ctx, time.Now()); err != nil && ctx.Err() == nil {
					slog.Error("alert evaluation", "err", err)
				}
			}
		}
	}()
	return nil
}

// ruleMatches reports whether a rule targets a device.
func ruleMatches(r *db.AlertRule, d *db.Device) bool {
	if r.DeviceID != nil && *r.DeviceID != d.ID {
		return false
	}
	if r.Group != nil && (d.Group == nil || *d.Group != *r.Group) {
		return false
	}
	if r.DeviceType != nil && *r.DeviceType != d.Type {
		return false
	}
	return true
}

// metricValue extracts the current value; nil means UNKNOWN.
func (e *Engine) metricValue(metric string, d *db.Device, now time.Time) *float64 {
	ds, ok := e.src.State(d.ID)
	if !ok || ds.Unsupported {
		return nil
	}
	one, zero := 1.0, 0.0
	grace := now.Unix()-ds.StartedAt < int64(2*ds.IntervalS)
	switch metric {
	case "device_offline":
		if ds.Status == collector.StatusOnline && !ds.Stale(now) {
			return &zero
		}
		if grace && ds.Status != collector.StatusOffline {
			return nil // just started; no verdict yet
		}
		return &one
	case "ssh_key_changed":
		if ds.HostKeyMismatch {
			return &one
		}
		return &zero
	}
	if ds.Latest == nil || ds.Stale(now) {
		return nil
	}
	if v, ok := ds.Latest.Scalars()[metric]; ok {
		return &v
	}
	return nil
}

func breach(r *db.AlertRule, v float64) bool {
	switch r.Operator {
	case "gt":
		return v > r.Threshold
	case "lt":
		return v < r.Threshold
	case "eq":
		return v == r.Threshold
	}
	return false
}

// cleared applies hysteresis: a firing alert resolves only once the value
// passes resolve_threshold (if set).
func cleared(r *db.AlertRule, v float64) bool {
	if r.ResolveThreshold == nil {
		return !breach(r, v)
	}
	rt := *r.ResolveThreshold
	switch r.Operator {
	case "gt":
		return v < rt
	case "lt":
		return v > rt
	default:
		return v != r.Threshold
	}
}

func i64(v int64) *int64 { return &v }

// parentDown reports whether d's parent is offline/unknown.
func (e *Engine) parentDown(d *db.Device, now time.Time) bool {
	if d.ParentID == nil {
		return false
	}
	ps, ok := e.src.State(*d.ParentID)
	if !ok {
		return false // parent not polled (disabled) → don't suppress
	}
	return ps.Status != collector.StatusOnline || ps.Stale(now)
}

// Evaluate runs one pass over all enabled rules and devices.
func (e *Engine) Evaluate(ctx context.Context, now time.Time) error {
	rules, err := e.db.ListAlertRules(ctx)
	if err != nil {
		return err
	}
	devs, err := e.db.ListDevices(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	seen := map[key]bool{}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		for _, d := range devs {
			if !d.Enabled || !ruleMatches(r, d) {
				continue
			}
			k := key{r.ID, d.ID}
			val := e.metricValue(r.Metric, d, now)
			cur, exists := e.states[k]
			if !exists && val == nil {
				continue // never had data for this pair; nothing to track
			}
			seen[k] = true
			if !exists {
				cur = &db.AlertState{RuleID: r.ID, DeviceID: d.ID, State: db.StateOK}
			}
			cur.RuleName, cur.DeviceName, cur.Metric, cur.Severity = r.Name, d.Name, r.Metric, r.Severity
			e.step(ctx, r, d, k, cur, val, now, !exists)
		}
	}
	// Drop states whose rule/device no longer applies.
	for k, s := range e.states {
		if seen[k] {
			continue
		}
		if s.State == db.StateFiring {
			e.resolve(ctx, k, s, now, "rule or device no longer applies")
			e.pub.Publish(sse.EventAlertResolved, s)
		}
		_ = e.db.DeleteAlertState(ctx, s.ID)
		delete(e.states, k)
		delete(e.peaks, k)
	}
	return nil
}

// step advances the state machine for one (rule, device).
func (e *Engine) step(ctx context.Context, r *db.AlertRule, d *db.Device, k key, s *db.AlertState, val *float64, now time.Time, isNew bool) {
	prevState := s.State
	prevVal := s.Value
	prevSup := s.Suppressed
	s.Value = val
	s.LastEvalAt = now.Unix()

	if val == nil { // UNKNOWN
		if s.State != db.StateFiring {
			s.State, s.PendingSince, s.Suppressed = db.StateUnknown, nil, false
		}
	} else {
		v := *val
		if s.State == db.StateFiring {
			if v > e.peaks[k] && r.Operator != "lt" || v < e.peaks[k] && r.Operator == "lt" {
				e.peaks[k] = v
			}
			if cleared(r, v) {
				e.resolve(ctx, k, s, now, "")
			}
		} else {
			if s.State != db.StatePending {
				if breach(r, v) {
					s.State, s.PendingSince = db.StatePending, i64(now.Unix())
				} else {
					s.State, s.PendingSince, s.Suppressed = db.StateOK, nil, false
				}
			} else if !breach(r, v) {
				s.State, s.PendingSince, s.Suppressed = db.StateOK, nil, false
			}
			if s.State == db.StatePending && now.Unix()-*s.PendingSince >= int64(r.DurationS) {
				switch {
				case e.parentDown(d, now):
					s.Suppressed = true // stay pending while upstream is down
				case s.ResolvedAt != nil && now.Unix()-*s.ResolvedAt < int64(r.CooldownS):
					// cooldown after last resolution; hold in pending
				default:
					e.fire(ctx, k, s, v, now)
				}
			}
		}
	}

	changed := isNew || s.State != prevState || s.Suppressed != prevSup
	valueChanged := (prevVal == nil) != (val == nil) || (prevVal != nil && val != nil && *prevVal != *val)
	if changed || (valueChanged && (s.State == db.StatePending || s.State == db.StateFiring)) {
		if err := e.db.UpsertAlertState(ctx, s); err != nil {
			slog.Error("persist alert state", "err", err)
			return
		}
		e.states[k] = s
		switch {
		case s.State == db.StateFiring && prevState != db.StateFiring:
			e.pub.Publish(sse.EventAlertFired, s)
		case s.State == db.StateResolved && prevState == db.StateFiring:
			e.pub.Publish(sse.EventAlertResolved, s)
		default:
			e.pub.Publish(sse.EventAlertUpdated, s)
		}
	}
}

func (e *Engine) fire(ctx context.Context, k key, s *db.AlertState, v float64, now time.Time) {
	s.State = db.StateFiring
	s.FiredAt = i64(now.Unix())
	s.ResolvedAt, s.AckedAt, s.Suppressed = nil, nil, false
	e.peaks[k] = v
	slog.Warn("alert firing", "rule", s.RuleName, "device", s.DeviceName, "value", v, "severity", s.Severity)
	e.notify(ctx, s, now)
}

func (e *Engine) resolve(ctx context.Context, k key, s *db.AlertState, now time.Time, reason string) {
	s.State = db.StateResolved
	s.ResolvedAt = i64(now.Unix())
	s.PendingSince = nil
	var peak *float64
	if p, ok := e.peaks[k]; ok {
		peak = &p
	}
	if s.FiredAt != nil {
		h := &db.AlertHistory{RuleID: &s.RuleID, DeviceID: &s.DeviceID, RuleName: s.RuleName, DeviceName: s.DeviceName,
			Metric: s.Metric, Severity: s.Severity, PeakValue: peak, FiredAt: *s.FiredAt, ResolvedAt: now.Unix(), Acked: s.AckedAt != nil}
		if err := e.db.InsertAlertHistory(ctx, h); err != nil {
			slog.Error("insert alert history", "err", err)
		}
	}
	delete(e.peaks, k)
	slog.Info("alert resolved", "rule", s.RuleName, "device", s.DeviceName, "reason", reason)
	e.notify(ctx, s, now)
}

func (e *Engine) notify(ctx context.Context, s *db.AlertState, now time.Time) {
	if s.MutedUntil != nil && *s.MutedUntil > now.Unix() {
		return
	}
	if err := e.notifier.Notify(ctx, *s); err != nil {
		slog.Error("notify", "err", err)
		return
	}
	s.LastNotifiedAt = i64(now.Unix())
}

// ---------- operator actions ----------

// ErrNotFound is returned when an alert state id does not exist.
var ErrNotFound = errors.New("alert not found")

func (e *Engine) findByID(id int64) (key, *db.AlertState) {
	for k, s := range e.states {
		if s.ID == id {
			return k, s
		}
	}
	return key{}, nil
}

// MuteForever is the muted_until sentinel used for "until unmuted".
const MuteForever = int64(1) << 62

// Action is an operator action on an alert.
type Action string

const (
	ActionAck    Action = "ack"
	ActionMute   Action = "mute"
	ActionUnmute Action = "unmute"
)

// Apply performs ack/mute/unmute, persists, publishes and audits it.
// durationS applies to mute only (0 = until unmuted).
func (e *Engine) Apply(ctx context.Context, id int64, action Action, durationS int64, sourceIP string) (*db.AlertState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, s := e.findByID(id)
	if s == nil {
		return nil, ErrNotFound
	}
	now := time.Now().Unix()
	switch action {
	case ActionAck:
		s.AckedAt = i64(now)
	case ActionMute:
		if durationS > 0 {
			s.MutedUntil = i64(now + durationS)
		} else {
			s.MutedUntil = i64(MuteForever)
		}
	case ActionUnmute:
		s.MutedUntil = nil
	default:
		return nil, errors.New("unknown action")
	}
	if err := e.db.UpsertAlertState(ctx, s); err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]any{"alert_id": id, "rule": s.RuleName, "duration_s": durationS})
	devID := s.DeviceID
	_ = e.db.InsertAudit(ctx, &devID, "alert:"+string(action), string(payload), sourceIP)
	e.pub.Publish(sse.EventAlertUpdated, s)
	c := *s
	return &c, nil
}
