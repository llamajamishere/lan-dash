package collector

import "testing"

func TestSplitSectionsAndParsers(t *testing.T) {
	out := `@@CPU@@
cpu  100 0 100 700 100 0 0 0 0 0
@@LOAD@@
0.50 0.40 0.30 1/200 1234
@@MEM@@
MemTotal:       1000 kB
MemAvailable:    250 kB
@@DISK@@
Filesystem     1024-blocks    Used Available Capacity Mounted on
/dev/sda1         1000000  850000    150000      85% /
tmpfs              100000       0    100000       0% /run
@@END@@
`
	s := splitSections(out)

	cs, ok := parseCPU(s["CPU"])
	if !ok || cs.total != 1000 || cs.idle != 800 {
		t.Fatalf("parseCPU = %+v ok=%v", cs, ok)
	}
	if got := cpuPct(cpuSample{}, cs); got != 20 {
		t.Errorf("cpuPct since boot = %v, want 20", got)
	}
	if got := cpuPct(cs, cpuSample{idle: 850, total: 1100}); got != 50 {
		t.Errorf("cpuPct delta = %v, want 50", got)
	}

	if l1, _, l15, ok := parseLoad(s["LOAD"]); !ok || l1 != 0.5 || l15 != 0.3 {
		t.Errorf("parseLoad = %v %v %v", l1, l15, ok)
	}

	m := parseMeminfo(s["MEM"])
	if m["MemTotal"] != 1000*1024 || m["MemAvailable"] != 250*1024 {
		t.Errorf("parseMeminfo = %v", m)
	}

	disks := parseDF(s["DISK"])
	if len(disks) != 1 || disks[0].Mount != "/" || disks[0].Pct != 85 {
		t.Fatalf("parseDF = %+v", disks)
	}
}

func TestRingBufferWraps(t *testing.T) {
	r := NewRingBuffer(3)
	if r.Latest() != nil {
		t.Fatal("empty buffer should have no latest")
	}
	for ts := int64(1); ts <= 5; ts++ {
		r.Push(&Snapshot{Ts: ts})
	}
	if r.Len() != 3 || r.Latest().Ts != 5 {
		t.Fatalf("len=%d latest=%d", r.Len(), r.Latest().Ts)
	}
	got := r.Since(4)
	if len(got) != 2 || got[0].Ts != 4 || got[1].Ts != 5 {
		t.Fatalf("Since(4) wrong: %d items", len(got))
	}
}
