package collector

// Snapshot is one poll result. Pointer fields are nil when the device type
// does not report that metric, so "missing" is never confused with zero.
type Snapshot struct {
	Ts int64 `json:"ts"` // unix seconds (UTC)

	CPUPct *float64 `json:"cpu_pct,omitempty"`
	Load1  *float64 `json:"load1,omitempty"`
	Load5  *float64 `json:"load5,omitempty"`
	Load15 *float64 `json:"load15,omitempty"`

	MemTotal  *uint64  `json:"mem_total,omitempty"` // bytes
	MemUsed   *uint64  `json:"mem_used,omitempty"`
	MemPct    *float64 `json:"mem_pct,omitempty"`
	SwapTotal *uint64  `json:"swap_total,omitempty"`
	SwapUsed  *uint64  `json:"swap_used,omitempty"`
	SwapPct   *float64 `json:"swap_pct,omitempty"`

	UptimeSecs *float64 `json:"uptime_secs,omitempty"`
	BatteryPct *float64 `json:"battery_pct,omitempty"` // smart devices (later phases)
	LatencyMs  *float64 `json:"latency_ms,omitempty"`  // ping checks (phase 4)

	Disks      []DiskStat      `json:"disks,omitempty"`
	Net        []NetStat       `json:"net,omitempty"`
	Temps      []TempStat      `json:"temps,omitempty"`
	Processes  []ProcStat      `json:"processes,omitempty"`
	Services   []ServiceStat   `json:"services,omitempty"`
	Containers []ContainerStat `json:"containers,omitempty"`
}

// DiskStat is one mounted filesystem.
type DiskStat struct {
	Mount      string  `json:"mount"`
	Filesystem string  `json:"filesystem"`
	TotalBytes uint64  `json:"total_bytes"`
	UsedBytes  uint64  `json:"used_bytes"`
	Pct        float64 `json:"pct"`
}

// NetStat is one network interface. Bps fields are computed between polls.
type NetStat struct {
	Iface   string  `json:"iface"`
	RxBytes uint64  `json:"rx_bytes"`
	TxBytes uint64  `json:"tx_bytes"`
	RxBps   float64 `json:"rx_bps"`
	TxBps   float64 `json:"tx_bps"`
}

// TempStat is one temperature sensor reading in Celsius.
type TempStat struct {
	Label string  `json:"label"`
	C     float64 `json:"c"`
}

// ProcStat is one process from the top list.
type ProcStat struct {
	PID     int     `json:"pid"`
	User    string  `json:"user"`
	CPUPct  float64 `json:"cpu_pct"`
	MemPct  float64 `json:"mem_pct"`
	RSSKB   uint64  `json:"rss_kb"`
	Command string  `json:"command"`
}

// ServiceStat is a systemd (or Windows) service.
type ServiceStat struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	Description string `json:"description"`
}

// ContainerStat is a Docker container.
type ContainerStat struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`
	Status string `json:"status"`
}

// MaxDiskPct returns the highest disk usage percent, or nil.
func (s *Snapshot) MaxDiskPct() *float64 {
	if s == nil || len(s.Disks) == 0 {
		return nil
	}
	m := s.Disks[0].Pct
	for _, d := range s.Disks[1:] {
		if d.Pct > m {
			m = d.Pct
		}
	}
	return &m
}

// MaxTempC returns the hottest sensor reading, or nil.
func (s *Snapshot) MaxTempC() *float64 {
	if s == nil || len(s.Temps) == 0 {
		return nil
	}
	m := s.Temps[0].C
	for _, t := range s.Temps[1:] {
		if t.C > m {
			m = t.C
		}
	}
	return &m
}

// NetTotals returns summed rx/tx bytes-per-second across interfaces.
func (s *Snapshot) NetTotals() (rx, tx float64, ok bool) {
	if s == nil || len(s.Net) == 0 {
		return 0, 0, false
	}
	for _, n := range s.Net {
		rx += n.RxBps
		tx += n.TxBps
	}
	return rx, tx, true
}

// Scalars flattens the snapshot into named numeric metrics. Used by the
// alert engine and the 1-minute aggregator.
func (s *Snapshot) Scalars() map[string]float64 {
	out := map[string]float64{}
	if s == nil {
		return out
	}
	put := func(k string, v *float64) {
		if v != nil {
			out[k] = *v
		}
	}
	put("cpu_pct", s.CPUPct)
	put("load1", s.Load1)
	put("mem_pct", s.MemPct)
	put("swap_pct", s.SwapPct)
	put("battery_pct", s.BatteryPct)
	put("latency_ms", s.LatencyMs)
	put("disk_pct", s.MaxDiskPct())
	put("temp_c", s.MaxTempC())
	if rx, tx, ok := s.NetTotals(); ok {
		out["net_rx_bps"], out["net_tx_bps"] = rx, tx
	}
	return out
}

func f64(v float64) *float64 { return &v }
func u64(v uint64) *uint64   { return &v }
