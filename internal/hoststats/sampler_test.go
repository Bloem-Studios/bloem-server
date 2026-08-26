package hoststats

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeProcFixture(t *testing.T, root string, statCPULine, netDevBody, meminfoBody string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "stat"), []byte(statCPULine+"\nctxt 12345\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "net", "dev"), []byte(netDevBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "meminfo"), []byte(meminfoBody), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

const netDevHeader = "Inter-|   Receive                                                |  Transmit\n" +
	" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n"

func TestReadProcStatCPU(t *testing.T) {
	root := newFixtureRoot(t)
	writeProcFixture(t, root,
		"cpu  1000 0 500 8000 200 0 0 0 0 0",
		netDevHeader+"    lo:    100       1    0    0    0     0          0         0      100       1    0    0    0     0       0          0\n"+
			"  eth0:  50000     100    0    0    0     0          0         0    20000      80    0    0    0     0       0          0\n",
		"MemTotal:       16000000 kB\nMemAvailable:   10000000 kB\n",
	)

	total, idle, err := readProcStatCPU(root + "/stat")
	if err != nil {
		t.Fatalf("readProcStatCPU: %v", err)
	}
	wantTotal := uint64(1000 + 0 + 500 + 8000 + 200)
	wantIdle := uint64(8000 + 200) // idle + iowait
	if total != wantTotal {
		t.Errorf("total = %d, want %d", total, wantTotal)
	}
	if idle != wantIdle {
		t.Errorf("idle = %d, want %d", idle, wantIdle)
	}
}

func TestReadProcNetDevExcludesLoopback(t *testing.T) {
	root := newFixtureRoot(t)
	writeProcFixture(t, root,
		"cpu  0 0 0 0 0 0 0 0 0 0",
		netDevHeader+"    lo:    999999       1    0    0    0     0          0         0      999999       1    0    0    0     0       0          0\n"+
			"  eth0:  50000     100    0    0    0     0          0         0    20000      80    0    0    0     0       0          0\n",
		"MemTotal:       16000000 kB\nMemAvailable:   10000000 kB\n",
	)

	rx, tx, err := readProcNetDev(root + "/net/dev")
	if err != nil {
		t.Fatalf("readProcNetDev: %v", err)
	}
	if rx != 50000 {
		t.Errorf("rx = %d, want 50000 (loopback must be excluded)", rx)
	}
	if tx != 20000 {
		t.Errorf("tx = %d, want 20000 (loopback must be excluded)", tx)
	}
}

func TestReadMeminfoUsesAvailableNotFree(t *testing.T) {
	root := newFixtureRoot(t)
	// MemFree is deliberately far lower than MemAvailable, the way a
	// well-cached real host looks -- used must be computed against
	// MemAvailable (6M used) not MemFree (14M used), or a healthy host
	// would misreport as memory-pressured.
	writeProcFixture(t, root,
		"cpu  0 0 0 0 0 0 0 0 0 0",
		netDevHeader,
		"MemTotal:       16000000 kB\nMemFree:         2000000 kB\nMemAvailable:   10000000 kB\n",
	)

	mem, err := readMeminfo(root)
	if err != nil {
		t.Fatalf("readMeminfo: %v", err)
	}
	if mem.totalBytes != 16000000*1024 {
		t.Errorf("totalBytes = %d, want %d", mem.totalBytes, 16000000*1024)
	}
	wantUsed := int64(16000000-10000000) * 1024
	if mem.usedBytes != wantUsed {
		t.Errorf("usedBytes = %d, want %d", mem.usedBytes, wantUsed)
	}
}

func TestSamplerFirstSampleReportsMemoryWithZeroedRates(t *testing.T) {
	root := newFixtureRoot(t)
	writeProcFixture(t, root,
		"cpu  1000 0 500 8000 200 0 0 0 0 0",
		netDevHeader+"  eth0:  50000     100    0    0    0     0          0         0    20000      80    0    0    0     0       0          0\n",
		"MemTotal:       16000000 kB\nMemAvailable:   10000000 kB\n",
	)

	s := &Sampler{Interval: time.Second, procRoot: root}
	s.sampleOnce()
	snap := s.Get()

	if !snap.Supported {
		t.Fatal("expected Supported=true on a readable first sample")
	}
	if snap.CPUPercent != 0 || snap.NetworkRxBytesPerSec != 0 || snap.NetworkTxBytesPerSec != 0 {
		t.Errorf("first sample should have zeroed rate fields, got %+v", snap)
	}
	if snap.MemoryTotalBytes != 16000000*1024 {
		t.Errorf("MemoryTotalBytes = %d, want %d", snap.MemoryTotalBytes, 16000000*1024)
	}
}

func TestSamplerSecondSampleComputesRatesFromDelta(t *testing.T) {
	root := newFixtureRoot(t)
	writeProcFixture(t, root,
		"cpu  1000 0 500 8000 200 0 0 0 0 0",
		netDevHeader+"  eth0:  50000     100    0    0    0     0          0         0    20000      80    0    0    0     0       0          0\n",
		"MemTotal:       16000000 kB\nMemAvailable:   10000000 kB\n",
	)
	s := &Sampler{Interval: time.Second, procRoot: root}
	s.sampleOnce() // first sample: establishes the baseline counters

	// Second sample: CPU busier (idle barely moved vs. total), network
	// advanced by exactly 10000 rx / 5000 tx bytes.
	writeProcFixture(t, root,
		"cpu  2000 0 1000 8100 200 0 0 0 0 0",
		netDevHeader+"  eth0:  60000     110    0    0    0     0          0         0    25000      85    0    0    0     0       0          0\n",
		"MemTotal:       16000000 kB\nMemAvailable:    9000000 kB\n",
	)
	s.sampleOnce()
	snap := s.Get()

	if !snap.Supported {
		t.Fatal("expected Supported=true")
	}
	// total delta = (2000+1000+8100+200) - (1000+500+8000+200) = 11300-9700 = 1600
	// idle delta = (8100+200) - (8000+200) = 100
	// cpu% = 100 * (1 - 100/1600) = 93.75
	if got := snap.CPUPercent; got < 93 || got > 94 {
		t.Errorf("CPUPercent = %v, want ~93.75", got)
	}
	if snap.NetworkRxBytesPerSec == 0 || snap.NetworkTxBytesPerSec == 0 {
		t.Errorf("expected nonzero network rates, got rx=%v tx=%v", snap.NetworkRxBytesPerSec, snap.NetworkTxBytesPerSec)
	}
	wantUsed := int64(16000000-9000000) * 1024
	if snap.MemoryUsedBytes != wantUsed {
		t.Errorf("MemoryUsedBytes = %d, want %d", snap.MemoryUsedBytes, wantUsed)
	}
}

func TestSamplerUnsupportedWhenProcUnreadable(t *testing.T) {
	s := &Sampler{Interval: time.Second, procRoot: "/nonexistent-proc-root-for-test"}
	s.sampleOnce()
	snap := s.Get()
	if snap.Supported {
		t.Fatal("expected Supported=false when /proc can't be read")
	}
}

func TestDiffUint64GuardsAgainstCounterGoingBackwards(t *testing.T) {
	if got := diffUint64(5, 10); got != 0 {
		t.Errorf("diffUint64(5, 10) = %d, want 0 (counter appeared to reset)", got)
	}
	if got := diffUint64(15, 10); got != 5 {
		t.Errorf("diffUint64(15, 10) = %d, want 5", got)
	}
}

func TestGetOnNilSamplerReturnsZeroValue(t *testing.T) {
	var s *Sampler
	snap := s.Get()
	if snap.Supported {
		t.Error("nil sampler must report Supported=false, not panic or claim support")
	}
}
