package collector

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/user/lan-dash/internal/db"
)

// MockCollector generates realistic, smoothly varying fake data for any
// device type. Enabled via config key mock_enabled=true (or -mock flag).
//
// Conventions for testing alerts/status in the UI:
//   - host starting with "down" or "offline" → always unreachable
//   - host starting with "flaky"             → unreachable ~2 minutes of every 6
//   - host starting with "hot"               → CPU/temperature run high
//   - host starting with "full"              → root disk ~92%
type MockCollector struct {
	mu      sync.Mutex
	netBase map[int64]uint64
}

// NewMockCollector builds the mock.
func NewMockCollector() *MockCollector { return &MockCollector{netBase: map[int64]uint64{}} }

// Type implements Collector ("mock" is never a device type; the manager
// uses this collector for every device when mock mode is on).
func (m *MockCollector) Type() string { return "mock" }

// Discover implements Collector.
func (m *MockCollector) Discover(context.Context, *db.Device) error { return nil }

// Control accepts any action (lets the UI/audit flow be tested).
func (m *MockCollector) Control(_ context.Context, dev *db.Device, a Action) error {
	if a.Name == "" {
		return errors.New("action name required")
	}
	return nil
}

func seed(id int64) float64 {
	h := fnv.New32a()
	fmt.Fprintf(h, "dev-%d", id)
	return float64(h.Sum32()%1000) / 1000 * 2 * math.Pi
}

// Poll implements Collector.
func (m *MockCollector) Poll(ctx context.Context, dev *db.Device) (*Snapshot, error) {
	host := strings.ToLower(dev.Host)
	now := time.Now()
	t := float64(now.Unix())
	if strings.HasPrefix(host, "down") || strings.HasPrefix(host, "offline") {
		return nil, Unreachable(errors.New("mock: connection refused"))
	}
	if strings.HasPrefix(host, "flaky") && int(t/60)%6 < 2 {
		return nil, Unreachable(errors.New("mock: i/o timeout"))
	}
	ph := seed(dev.ID)
	jitter := func(a float64) float64 { return (rand.Float64()*2 - 1) * a }
	wave := func(period, amp float64) float64 { return amp * math.Sin(2*math.Pi*t/period+ph) }

	cpu := 25 + wave(300, 15) + wave(47, 6) + jitter(4)
	tempBase := 48.0
	if strings.HasPrefix(host, "hot") {
		cpu, tempBase = 92+wave(120, 6)+jitter(2), 78
	}
	cpu = clampPct(cpu)
	memTotal := uint64(16) << 30
	memPct := clampPct(55 + wave(900, 12) + jitter(1))
	memUsed := uint64(float64(memTotal) * memPct / 100)
	swapTotal := uint64(4) << 30
	swapUsed := uint64(float64(swapTotal) * (0.05 + 0.03*math.Sin(t/1200+ph)))
	load1 := math.Max(0, cpu/25+jitter(0.2))

	rootPct := 40 + 10*math.Sin(ph)
	if strings.HasPrefix(host, "full") {
		rootPct = 92 + wave(600, 1)
	}
	disks := []DiskStat{
		mkDisk("/", "/dev/nvme0n1p2", 512<<30, rootPct),
		mkDisk("/boot/efi", "/dev/nvme0n1p1", 512<<20, 6.2),
		mkDisk("/srv/data", "/dev/sda1", 4<<40, 63+5*math.Sin(ph*2)),
	}

	m.mu.Lock()
	rxRate := math.Max(0, 2e6+1.5e6*math.Sin(2*math.Pi*t/180+ph)+jitter(3e5))
	txRate := math.Max(0, 6e5+4e5*math.Sin(2*math.Pi*t/240+ph)+jitter(1e5))
	m.netBase[dev.ID] += uint64((rxRate + txRate) * 15)
	base := m.netBase[dev.ID]
	m.mu.Unlock()

	snap := &Snapshot{
		Ts:     now.Unix(),
		CPUPct: f64(cpu), Load1: f64(round2(load1)), Load5: f64(round2(load1 * 0.9)), Load15: f64(round2(load1 * 0.8)),
		MemTotal: u64(memTotal), MemUsed: u64(memUsed), MemPct: f64(memPct),
		SwapTotal: u64(swapTotal), SwapUsed: u64(swapUsed), SwapPct: f64(clampPct(100 * float64(swapUsed) / float64(swapTotal))),
		UptimeSecs: f64(float64(int64(t)%(86400*30)) + 3600),
		Disks:      disks,
		Net: []NetStat{
			{Iface: "enp3s0", RxBytes: base * 3 / 4, TxBytes: base / 4, RxBps: rxRate, TxBps: txRate},
			{Iface: "wlp2s0", RxBytes: base / 50, TxBytes: base / 80, RxBps: rxRate / 40, TxBps: txRate / 50},
		},
		Temps: []TempStat{
			{Label: "coretemp Package id 0", C: round1(tempBase + cpu*0.25 + jitter(1))},
			{Label: "nvme Composite", C: round1(38 + wave(600, 3))},
		},
		Processes: []ProcStat{
			{PID: 1342, User: "root", CPUPct: round1(cpu * 0.35), MemPct: 4.1, RSSKB: 690_000, Command: "dockerd"},
			{PID: 2210, User: "postgres", CPUPct: round1(cpu * 0.2), MemPct: 8.4, RSSKB: 1_400_000, Command: "postgres"},
			{PID: 3021, User: "www-data", CPUPct: round1(cpu * 0.12), MemPct: 2.2, RSSKB: 360_000, Command: "nginx"},
			{PID: 4410, User: "jellyfin", CPUPct: round1(cpu * 0.08), MemPct: 6.0, RSSKB: 980_000, Command: "jellyfin"},
			{PID: 912, User: "root", CPUPct: round1(cpu * 0.03), MemPct: 0.3, RSSKB: 41_000, Command: "systemd-journald"},
			{PID: 1, User: "root", CPUPct: 0.0, MemPct: 0.1, RSSKB: 12_000, Command: "systemd"},
		},
		Services: []ServiceStat{
			{Name: "ssh", State: "running", Description: "OpenBSD Secure Shell server"},
			{Name: "docker", State: "running", Description: "Docker Application Container Engine"},
			{Name: "nginx", State: "running", Description: "A high performance web server"},
			{Name: "postgresql@16-main", State: "running", Description: "PostgreSQL Cluster 16-main"},
			{Name: "cron", State: "running", Description: "Regular background program processing daemon"},
			{Name: "systemd-journald", State: "running", Description: "Journal Service"},
		},
		Containers: []ContainerStat{
			{ID: "3f2a9c1d7b44", Name: "jellyfin", Image: "jellyfin/jellyfin:latest", State: "running", Status: "Up 3 days"},
			{ID: "9b8c7d6e5f40", Name: "pihole", Image: "pihole/pihole:latest", State: "running", Status: "Up 3 days (healthy)"},
			{ID: "1a2b3c4d5e6f", Name: "backup-job", Image: "restic/restic", State: "exited", Status: "Exited (0) 5 hours ago"},
		},
	}
	switch dev.Type {
	case "hue", "blink", "plug":
		// Smart devices: no host metrics, just battery/latency.
		snap = &Snapshot{Ts: now.Unix(), BatteryPct: f64(clampPct(70 + wave(3600, 25))), LatencyMs: f64(round1(8 + jitter(3)))}
	case "ping":
		snap = &Snapshot{Ts: now.Unix(), LatencyMs: f64(round1(math.Max(0.2, 2+wave(120, 1)+jitter(0.5))))}
	}
	return snap, nil
}

func mkDisk(mount, fs string, total uint64, pct float64) DiskStat {
	pct = clampPct(pct)
	return DiskStat{Mount: mount, Filesystem: fs, TotalBytes: total, UsedBytes: uint64(float64(total) * pct / 100), Pct: pct}
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// StubCollector is a placeholder for device types delivered in later
// phases. Poll always returns ErrNotImplemented → status "unknown".
type StubCollector struct{ T string }

func (s StubCollector) Type() string                                 { return s.T }
func (s StubCollector) Discover(context.Context, *db.Device) error   { return ErrNotImplemented }
func (s StubCollector) Poll(context.Context, *db.Device) (*Snapshot, error) { return nil, ErrNotImplemented }
func (s StubCollector) Control(context.Context, *db.Device, Action) error   { return ErrNotImplemented }
