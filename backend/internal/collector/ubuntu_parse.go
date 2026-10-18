package collector

import (
	"bufio"
	"encoding/json"
	"strconv"
	"strings"
)

// ubuntuScript is executed once per poll over SSH. Every section is
// prefixed with a unique marker so parsing is trivial and robust. Every
// optional tool (sensors, docker, systemctl) fails silently.
const ubuntuScript = `export LC_ALL=C
echo '@@CPU@@'; head -n1 /proc/stat
echo '@@LOAD@@'; cat /proc/loadavg
echo '@@MEM@@'; cat /proc/meminfo
echo '@@DISK@@'; df -P -k -x tmpfs -x devtmpfs -x squashfs -x overlay -x efivarfs 2>/dev/null || df -P -k
echo '@@NET@@'; cat /proc/net/dev
echo '@@THERMAL@@'; for z in /sys/class/thermal/thermal_zone*; do [ -r "$z/temp" ] && echo "$(cat $z/type 2>/dev/null) $(cat $z/temp 2>/dev/null)"; done 2>/dev/null
echo '@@SENSORS@@'; sensors -j 2>/dev/null || true
echo '@@UPTIME@@'; cat /proc/uptime
echo '@@PROCS@@'; ps -eo pid,user,pcpu,pmem,rss,comm --sort=-pcpu --no-headers 2>/dev/null | head -n 15
echo '@@SERVICES@@'; systemctl list-units --type=service --state=running --no-pager --plain --no-legend 2>/dev/null | head -n 100
echo '@@DOCKER@@'; docker ps -a --format '{{json .}}' 2>/dev/null || true
echo '@@END@@'
`

// splitSections splits script output by @@NAME@@ markers.
func splitSections(out string) map[string]string {
	sections := map[string]string{}
	var cur string
	var b strings.Builder
	flush := func() {
		if cur != "" {
			sections[cur] = b.String()
		}
		b.Reset()
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "@@") && strings.HasSuffix(line, "@@") && len(line) > 4 {
			flush()
			cur = strings.Trim(line, "@")
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	flush()
	return sections
}

// cpuSample holds aggregate jiffies from /proc/stat.
type cpuSample struct{ idle, total uint64 }

func parseCPU(s string) (cpuSample, bool) {
	f := strings.Fields(s)
	if len(f) < 5 || f[0] != "cpu" {
		return cpuSample{}, false
	}
	var cs cpuSample
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		// Fields: user nice system idle iowait irq softirq steal guest guest_nice.
		// guest/guest_nice are already included in user/nice.
		if i >= 8 {
			break
		}
		cs.total += n
		if i == 3 || i == 4 {
			cs.idle += n
		}
	}
	return cs, cs.total > 0
}

// cpuPct computes usage between two samples (or since boot if prev is zero).
func cpuPct(prev, cur cpuSample) float64 {
	dt := cur.total - prev.total
	di := cur.idle - prev.idle
	if prev.total == 0 || cur.total <= prev.total {
		dt, di = cur.total, cur.idle
	}
	if dt == 0 {
		return 0
	}
	return clampPct(100 * (1 - float64(di)/float64(dt)))
}

func parseLoad(s string) (l1, l5, l15 float64, ok bool) {
	f := strings.Fields(s)
	if len(f) < 3 {
		return
	}
	var e1, e2, e3 error
	l1, e1 = strconv.ParseFloat(f[0], 64)
	l5, e2 = strconv.ParseFloat(f[1], 64)
	l15, e3 = strconv.ParseFloat(f[2], 64)
	return l1, l5, l15, e1 == nil && e2 == nil && e3 == nil
}

// parseMeminfo returns values in bytes keyed by field name.
func parseMeminfo(s string) map[string]uint64 {
	m := map[string]uint64{}
	for _, line := range strings.Split(s, "\n") {
		k, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		if len(f) > 1 && f[1] == "kB" {
			n *= 1024
		}
		m[k] = n
	}
	return m
}

var ignoredFS = map[string]bool{"tmpfs": true, "devtmpfs": true, "udev": true, "overlay": true, "squashfs": true, "efivarfs": true, "none": true, "shm": true}

func parseDF(s string) []DiskStat {
	out := []DiskStat{}
	for i, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 6 || f[0] == "Filesystem" {
			continue
		}
		if ignoredFS[f[0]] || strings.HasPrefix(f[5], "/snap/") || strings.HasPrefix(f[5], "/run") ||
			strings.HasPrefix(f[5], "/sys") || strings.HasPrefix(f[5], "/proc") || strings.HasPrefix(f[5], "/dev") {
			continue
		}
		total, _ := strconv.ParseUint(f[1], 10, 64)
		used, _ := strconv.ParseUint(f[2], 10, 64)
		avail, _ := strconv.ParseUint(f[3], 10, 64)
		if total == 0 {
			continue
		}
		// Match df's own percentage: used / (used + available).
		pct := 0.0
		if used+avail > 0 {
			pct = 100 * float64(used) / float64(used+avail)
		}
		out = append(out, DiskStat{Mount: strings.Join(f[5:], " "), Filesystem: f[0],
			TotalBytes: total * 1024, UsedBytes: used * 1024, Pct: round1(pct)})
	}
	return out
}

func skipIface(name string) bool {
	return name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "br-") ||
		strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "virbr")
}

func parseNetDev(s string) []NetStat {
	out := []NetStat{}
	for _, line := range strings.Split(s, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if len(f) < 9 || skipIface(name) {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		out = append(out, NetStat{Iface: name, RxBytes: rx, TxBytes: tx})
	}
	return out
}

// parseTemps combines /sys/class/thermal zones and `sensors -j` output.
// lm-sensors readings take precedence when present (they are labelled).
func parseTemps(thermal, sensorsJSON string) []TempStat {
	out := []TempStat{}
	if strings.TrimSpace(sensorsJSON) != "" {
		var chips map[string]map[string]any
		if json.Unmarshal([]byte(sensorsJSON), &chips) == nil {
			for chip, feats := range chips {
				for feat, v := range feats {
					vals, ok := v.(map[string]any)
					if !ok {
						continue
					}
					for k, raw := range vals {
						if strings.HasPrefix(k, "temp") && strings.HasSuffix(k, "_input") {
							if c, ok := raw.(float64); ok && c > -40 && c < 150 {
								out = append(out, TempStat{Label: chip + " " + feat, C: round1(c)})
							}
						}
					}
				}
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, line := range strings.Split(thermal, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		milli, err := strconv.ParseFloat(f[len(f)-1], 64)
		if err != nil {
			continue
		}
		c := milli / 1000
		if c > -40 && c < 150 {
			out = append(out, TempStat{Label: strings.Join(f[:len(f)-1], " "), C: round1(c)})
		}
	}
	return out
}

func parseUptime(s string) (float64, bool) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	return v, err == nil
}

func parseProcs(s string) []ProcStat {
	out := []ProcStat{}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		cpu, _ := strconv.ParseFloat(f[2], 64)
		mem, _ := strconv.ParseFloat(f[3], 64)
		rss, _ := strconv.ParseUint(f[4], 10, 64)
		out = append(out, ProcStat{PID: pid, User: f[1], CPUPct: cpu, MemPct: mem, RSSKB: rss, Command: strings.Join(f[5:], " ")})
	}
	return out
}

func parseServices(s string) []ServiceStat {
	out := []ServiceStat{}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.HasSuffix(f[0], ".service") {
			continue
		}
		out = append(out, ServiceStat{Name: strings.TrimSuffix(f[0], ".service"), State: f[3], Description: strings.Join(f[4:], " ")})
	}
	return out
}

func parseDocker(s string) []ContainerStat {
	out := []ContainerStat{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var c struct{ ID, Names, Image, State, Status string }
		if json.Unmarshal([]byte(line), &c) == nil {
			out = append(out, ContainerStat{ID: c.ID, Name: c.Names, Image: c.Image, State: c.State, Status: c.Status})
		}
	}
	return out
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return round1(v)
}

func round1(v float64) float64 { return float64(int64(v*10+0.5*sign(v))) / 10 }

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}
