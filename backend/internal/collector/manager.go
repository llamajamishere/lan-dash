package collector

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/user/lan-dash/internal/db"
	"github.com/user/lan-dash/internal/sse"
)

// Device status values.
const (
	StatusOnline  = "online"
	StatusOffline = "offline"
	StatusUnknown = "unknown"
)

// ConfigMockEnabled is the config key toggling mock collectors.
const ConfigMockEnabled = "mock_enabled"

// DeviceState is the manager's live view of one device.
type DeviceState struct {
	DeviceID            int64     `json:"device_id"`
	Status              string    `json:"status"`
	LastSeen            int64     `json:"last_seen"`    // last successful poll
	LastAttempt         int64     `json:"last_attempt"` // last poll attempt
	LastError           string    `json:"last_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	PollDurationMs      int64     `json:"poll_duration_ms"`
	IntervalS           int       `json:"interval_s"`
	StartedAt           int64     `json:"started_at"` // when polling began
	HostKeyMismatch     bool      `json:"host_key_mismatch"`
	Unsupported         bool      `json:"unsupported"` // collector not implemented yet (stub)
	Collector           string    `json:"collector"`
	Latest              *Snapshot `json:"latest,omitempty"`
}

// Stale reports whether no successful poll happened within 2× interval.
func (s DeviceState) Stale(now time.Time) bool {
	return now.Unix()-s.LastSeen > int64(2*s.IntervalS)
}

type worker struct {
	dev    db.Device
	mock   bool
	cancel context.CancelFunc
	done   chan struct{}
	wait   <-chan struct{} // previous worker's release; closed when safe to start
}

// Manager schedules one polling goroutine per enabled device.
type Manager struct {
	db       *db.DB
	reg      *Registry
	mockColl *MockCollector
	pub      sse.Publisher

	mu      sync.RWMutex
	ctx     context.Context
	mock    bool
	workers map[int64]*worker
	states  map[int64]*DeviceState
	rings   map[int64]*RingBuffer
	aggs    map[int64]*minuteAgg
	// releasing holds channels closed once a stopped worker has exited and
	// released its resources, so a replacement worker never races it.
	releasing map[int64]chan struct{}
}

// NewManager creates a manager. Call Start to begin polling.
func NewManager(d *db.DB, reg *Registry, pub sse.Publisher) *Manager {
	return &Manager{db: d, reg: reg, mockColl: NewMockCollector(), pub: pub,
		workers: map[int64]*worker{}, states: map[int64]*DeviceState{},
		rings: map[int64]*RingBuffer{}, aggs: map[int64]*minuteAgg{}, releasing: map[int64]chan struct{}{}}
}

// Start loads config/devices, starts workers and the retention pruner.
func (m *Manager) Start(ctx context.Context) error {
	v, _, err := m.db.GetConfig(ctx, ConfigMockEnabled)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.ctx = ctx
	m.mock = v == "true"
	m.mu.Unlock()
	go m.runPruner(ctx)
	return m.Reload(ctx)
}

// MockEnabled reports whether mock collectors are active.
func (m *Manager) MockEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mock
}

// SetMock persists the mock toggle and restarts all workers.
func (m *Manager) SetMock(ctx context.Context, on bool) error {
	val := "false"
	if on {
		val = "true"
	}
	if err := m.db.SetConfig(ctx, ConfigMockEnabled, val); err != nil {
		return err
	}
	m.mu.Lock()
	m.mock = on
	m.mu.Unlock()
	return m.Reload(ctx)
}

// Reload reconciles running workers with the devices table. Call after any
// device create/update/delete.
func (m *Manager) Reload(ctx context.Context) error {
	devs, err := m.db.ListDevices(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[int64]*db.Device{}
	for _, d := range devs {
		if d.Enabled {
			want[d.ID] = d
		}
	}
	all := map[int64]bool{}
	for _, d := range devs {
		all[d.ID] = true
	}
	// Stop workers that are gone, disabled, changed, or on the wrong mode.
	for id, w := range m.workers {
		d, ok := want[id]
		if ok && d.UpdatedAt == w.dev.UpdatedAt && w.mock == m.mock {
			continue
		}
		m.stopWorkerLocked(id, w)
	}
	// Forget state of deleted devices; mark disabled ones unknown.
	for id := range m.states {
		if !all[id] {
			delete(m.states, id)
			delete(m.rings, id)
			delete(m.aggs, id)
		} else if _, ok := want[id]; !ok {
			m.states[id].Status = StatusUnknown
			m.states[id].LastError = "polling disabled"
		}
	}
	for id, d := range want {
		if _, running := m.workers[id]; !running {
			m.startWorkerLocked(*d)
		}
	}
	return nil
}

func (m *Manager) stopWorkerLocked(id int64, w *worker) {
	w.cancel()
	delete(m.workers, id)
	released := make(chan struct{})
	m.releasing[id] = released
	// Release persistent resources (SSH connections) asynchronously.
	go func() {
		defer close(released)
		<-w.done
		for _, c := range m.reg.All() {
			if r, ok := c.(Releaser); ok {
				r.Release(id)
			}
		}
	}()
}

func (m *Manager) startWorkerLocked(dev db.Device) {
	if dev.PollIntervalS < 5 {
		dev.PollIntervalS = 15
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	w := &worker{dev: dev, mock: m.mock, cancel: cancel, done: make(chan struct{}), wait: m.releasing[dev.ID]}
	delete(m.releasing, dev.ID)
	m.workers[dev.ID] = w

	capacity := int(math.Ceil(3600/float64(dev.PollIntervalS))) + 2
	if r, ok := m.rings[dev.ID]; !ok || r == nil {
		m.rings[dev.ID] = NewRingBuffer(capacity)
	}
	if _, ok := m.aggs[dev.ID]; !ok {
		m.aggs[dev.ID] = newMinuteAgg()
	}
	st, ok := m.states[dev.ID]
	if !ok {
		st = &DeviceState{DeviceID: dev.ID, Status: StatusUnknown}
		m.states[dev.ID] = st
	}
	st.IntervalS = dev.PollIntervalS
	st.StartedAt = time.Now().Unix()
	st.LastError = ""
	go m.run(ctx, w)
}

func (m *Manager) collectorFor(dev *db.Device, mock bool) Collector {
	if mock {
		return m.mockColl
	}
	if c, ok := m.reg.Get(dev.Type); ok {
		return c
	}
	return StubCollector{T: dev.Type}
}

func (m *Manager) run(ctx context.Context, w *worker) {
	defer close(w.done)
	if w.wait != nil {
		select {
		case <-w.wait:
		case <-ctx.Done():
			return
		}
	}
	dev := w.dev
	interval := time.Duration(dev.PollIntervalS) * time.Second
	coll := m.collectorFor(&dev, w.mock)
	slog.Info("collector started", "device_id", dev.ID, "name", dev.Name, "collector", coll.Type(), "interval_s", dev.PollIntervalS)

	// Small stagger so many devices don't poll in lock-step.
	select {
	case <-time.After(time.Duration(dev.ID%5) * 200 * time.Millisecond):
	case <-ctx.Done():
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		m.pollOnce(ctx, &dev, coll, interval)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Manager) pollOnce(ctx context.Context, dev *db.Device, coll Collector, interval time.Duration) {
	timeout := interval
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	pctx, cancel := context.WithTimeout(ctx, timeout)
	start := time.Now()
	snap, err := coll.Poll(pctx, dev)
	cancel()
	if ctx.Err() != nil {
		return // worker stopped mid-poll; discard
	}
	now := time.Now()

	m.mu.Lock()
	st := m.states[dev.ID]
	if st == nil { // device removed concurrently
		m.mu.Unlock()
		return
	}
	prevStatus, prevErr := st.Status, st.LastError
	st.LastAttempt = now.Unix()
	st.PollDurationMs = now.Sub(start).Milliseconds()
	st.Collector = coll.Type()
	var flush []db.MetricPoint
	if err == nil && snap != nil {
		st.Status, st.LastSeen, st.LastError, st.ConsecutiveFailures = StatusOnline, now.Unix(), "", 0
		st.HostKeyMismatch, st.Unsupported = false, false
		st.Latest = snap
		m.rings[dev.ID].Push(snap)
		flush = m.aggs[dev.ID].add(snap)
	} else {
		if err == nil {
			err = errors.New("collector returned no data")
		}
		st.ConsecutiveFailures++
		st.LastError = err.Error()
		if errors.Is(err, ErrUnreachable) {
			st.Status = StatusOffline
		} else {
			st.Status = StatusUnknown
		}
		st.Unsupported = errors.Is(err, ErrNotImplemented)
		var hk *HostKeyMismatchError
		if errors.As(err, &hk) {
			st.HostKeyMismatch = true
		}
	}
	stCopy := *st
	m.mu.Unlock()

	if len(flush) > 0 {
		if ferr := m.db.InsertMetrics1m(context.Background(), dev.ID, flush); ferr != nil {
			slog.Error("persist 1m metrics", "device_id", dev.ID, "err", ferr)
		}
	}
	if err == nil {
		m.pub.Publish(sse.EventMetric, map[string]any{"device_id": dev.ID, "ts": snap.Ts, "snapshot": snap, "last_seen": stCopy.LastSeen})
	} else if stCopy.LastError != prevErr {
		// Publish/log only when the error changes to avoid flooding.
		kind := "poll_failed"
		if errors.Is(err, ErrNotImplemented) {
			kind = "not_implemented"
		} else if stCopy.HostKeyMismatch {
			kind = "host_key_changed"
		}
		slog.Warn("poll failed", "device_id", dev.ID, "name", dev.Name, "status", stCopy.Status, "err", err)
		m.pub.Publish(sse.EventCollectorError, map[string]any{"device_id": dev.ID, "device_name": dev.Name, "error": stCopy.LastError, "kind": kind})
	}
	if stCopy.Status != prevStatus {
		m.pub.Publish(sse.EventDeviceStatus, map[string]any{"device_id": dev.ID, "status": stCopy.Status, "last_seen": stCopy.LastSeen, "error": stCopy.LastError})
	}
}

// State returns a copy of a device's live state with staleness applied.
func (m *Manager) State(id int64) (DeviceState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.states[id]
	if !ok {
		return DeviceState{}, false
	}
	c := *st
	if c.Status == StatusOnline && c.Stale(time.Now()) {
		c.Status = StatusUnknown
	}
	return c, true
}

// States returns copies of all device states.
func (m *Manager) States() []DeviceState {
	m.mu.RLock()
	ids := make([]int64, 0, len(m.states))
	for id := range m.states {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	out := make([]DeviceState, 0, len(ids))
	for _, id := range ids {
		if s, ok := m.State(id); ok {
			out = append(out, s)
		}
	}
	return out
}

// Snapshots returns ring-buffer snapshots for a device since `since`.
func (m *Manager) Snapshots(id, since int64) []*Snapshot {
	m.mu.RLock()
	r := m.rings[id]
	m.mu.RUnlock()
	if r == nil {
		return []*Snapshot{}
	}
	return r.Since(since)
}

// Control runs a control action and records it in the audit log
// regardless of outcome.
func (m *Manager) Control(ctx context.Context, dev *db.Device, action Action, sourceIP string) error {
	coll := m.collectorFor(dev, m.MockEnabled())
	err := coll.Control(ctx, dev, action)
	payload := map[string]any{"action": action.Name, "params": action.Params, "collector": coll.Type(), "ok": err == nil}
	if err != nil {
		payload["error"] = err.Error()
	}
	b, _ := json.Marshal(payload)
	id := dev.ID
	if aerr := m.db.InsertAudit(ctx, &id, "control:"+action.Name, string(b), sourceIP); aerr != nil {
		slog.Error("audit insert", "err", aerr)
	}
	slog.Info("control action", "device_id", dev.ID, "action", action.Name, "source_ip", sourceIP, "ok", err == nil)
	return err
}

// ResetHostKey clears a stored TOFU fingerprint and restarts polling.
func (m *Manager) ResetHostKey(ctx context.Context, dev *db.Device) error {
	if u, ok := m.reg.Get("ubuntu"); ok {
		if uc, ok := u.(*UbuntuCollector); ok {
			if err := uc.ResetHostKey(ctx, dev); err != nil {
				return err
			}
		}
	}
	m.mu.Lock()
	if w, ok := m.workers[dev.ID]; ok {
		m.stopWorkerLocked(dev.ID, w)
	}
	if st, ok := m.states[dev.ID]; ok {
		st.HostKeyMismatch = false
	}
	m.mu.Unlock()
	return m.Reload(ctx)
}

// runPruner enforces data retention every 5 minutes.
func (m *Manager) runPruner(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		if err := m.db.Prune(ctx, time.Now()); err != nil && ctx.Err() == nil {
			slog.Error("prune", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---------- 1-minute aggregation ----------

type aggBucket struct{ sum, min, max float64; n int }

type minuteAgg struct {
	minute  int64
	buckets map[string]*aggBucket
}

func newMinuteAgg() *minuteAgg { return &minuteAgg{buckets: map[string]*aggBucket{}} }

// add folds a snapshot into the current minute and returns the completed
// previous minute's points when the minute rolls over.
func (a *minuteAgg) add(s *Snapshot) []db.MetricPoint {
	minute := s.Ts - s.Ts%60
	var out []db.MetricPoint
	if a.minute != 0 && minute != a.minute {
		for k, b := range a.buckets {
			out = append(out, db.MetricPoint{Ts: a.minute, Metric: k, Avg: b.sum / float64(b.n), Min: b.min, Max: b.max})
		}
		a.buckets = map[string]*aggBucket{}
	}
	a.minute = minute
	for k, v := range s.Scalars() {
		b, ok := a.buckets[k]
		if !ok {
			b = &aggBucket{min: v, max: v}
			a.buckets[k] = b
		}
		b.sum += v
		b.n++
		b.min = math.Min(b.min, v)
		b.max = math.Max(b.max, v)
	}
	return out
}
