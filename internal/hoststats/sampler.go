// Package hoststats periodically samples the host machine's own CPU,
// memory, and network usage from /proc, independent of the Go process's own
// resource footprint (which is what /metrics' default Prometheus collectors
// already expose). It exists to back a small admin-facing "how is the box
// this is running on doing" view — not a general observability pipeline.
package hoststats

import (
	"bufio"
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Snapshot is the most recently sampled host state. Supported is false on a
// platform this package doesn't know how to sample (anything but Linux) or
// when /proc itself couldn't be read even once — callers must check it
// before trusting any other field, mirroring this codebase's other
// capability-flag responses (see TenantsAvailability's 404-vs-error split).
type Snapshot struct {
	Supported            bool
	CPUPercent           float64
	MemoryUsedBytes      int64
	MemoryTotalBytes     int64
	NetworkRxBytesPerSec float64
	NetworkTxBytesPerSec float64
	SampledAt            time.Time
}

// Sampler owns a background ticker that refreshes Snapshot at Interval.
// Reads of the current snapshot (Get) never block on or wait for sampling —
// they return whatever was last computed, so a slow or blocked /proc read
// can never make an HTTP handler hang.
type Sampler struct {
	Interval time.Duration
	// procRoot is "/proc" in production; overridable in tests, and
	// resolved once at construction to "/host/proc" when that exists
	// (a bind-mounted host /proc, the same convention this codebase's
	// database.detectTotalMemoryBytes already uses for containerized
	// deployments) and "/proc" doesn't.
	procRoot string

	mu       sync.RWMutex
	snapshot Snapshot

	// prev carries the raw, un-rated counters from the previous sample so
	// CPU% and network bytes/sec can be computed as a delta over Interval.
	// nil before the first sample, and permanently nil on an unsupported
	// platform since no second sample is ever attempted.
	prev *rawCounters
}

type rawCounters struct {
	at            time.Time
	cpuTotalJifs  uint64
	cpuIdleJifs   uint64
	networkRxByte uint64
	networkTxByte uint64
}

// NewSampler resolves procRoot once (cheap: at most two stat calls) and
// returns a Sampler ready for Run.
func NewSampler(interval time.Duration) *Sampler {
	root := "/proc"
	if _, err := os.Stat("/host/proc/stat"); err == nil {
		root = "/host/proc"
	}
	return &Sampler{Interval: interval, procRoot: root}
}

// Get returns the most recently computed snapshot. Safe for concurrent use;
// returns the zero Snapshot (Supported: false) before the first sample.
func (s *Sampler) Get() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

// Run samples once immediately, then every Interval, until ctx is done.
// Meant to be launched with `go sampler.Run(ctx)` at startup; it never
// returns an error since a permanently-unsupported platform is a valid,
// reported state (Snapshot.Supported == false), not a failure to log.
func (s *Sampler) Run(ctx context.Context) {
	if s == nil {
		return
	}
	if runtime.GOOS != "linux" {
		s.mu.Lock()
		s.snapshot = Snapshot{Supported: false}
		s.mu.Unlock()
		return
	}

	s.sampleOnce()
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sampleOnce()
		}
	}
}

func (s *Sampler) sampleOnce() {
	now := time.Now()
	cur, err := readCounters(s.procRoot)
	if err != nil {
		// A single failed read (e.g. /proc momentarily unavailable) marks
		// this platform unsupported for the CURRENT snapshot only — the
		// next tick tries again and can recover. Genuinely never-Linux
		// platforms are already short-circuited in Run above.
		s.mu.Lock()
		s.snapshot = Snapshot{Supported: false}
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	prev := s.prev
	s.prev = cur
	s.mu.Unlock()

	if prev == nil {
		// First sample: totals exist but nothing to diff against yet for
		// the rate-based fields. Reported as supported with zeroed
		// rates rather than withheld, since memory usage is already
		// meaningful on this very first sample.
		mem, memErr := readMeminfo(s.procRoot)
		snap := Snapshot{Supported: memErr == nil, SampledAt: now}
		if memErr == nil {
			snap.MemoryUsedBytes = mem.usedBytes
			snap.MemoryTotalBytes = mem.totalBytes
		}
		s.mu.Lock()
		s.snapshot = snap
		s.mu.Unlock()
		return
	}

	elapsed := cur.at.Sub(prev.at).Seconds()
	snap := Snapshot{Supported: true, SampledAt: now}
	if totalDelta := diffUint64(cur.cpuTotalJifs, prev.cpuTotalJifs); totalDelta > 0 {
		idleDelta := diffUint64(cur.cpuIdleJifs, prev.cpuIdleJifs)
		snap.CPUPercent = clampPercent(100 * (1 - float64(idleDelta)/float64(totalDelta)))
	}
	if elapsed > 0 {
		snap.NetworkRxBytesPerSec = float64(diffUint64(cur.networkRxByte, prev.networkRxByte)) / elapsed
		snap.NetworkTxBytesPerSec = float64(diffUint64(cur.networkTxByte, prev.networkTxByte)) / elapsed
	}
	if mem, err := readMeminfo(s.procRoot); err == nil {
		snap.MemoryUsedBytes = mem.usedBytes
		snap.MemoryTotalBytes = mem.totalBytes
	}

	s.mu.Lock()
	s.snapshot = snap
	s.mu.Unlock()
}

// diffUint64 guards against a counter that appears to go backwards (a
// /proc/stat or /proc/net/dev field wrapping, or the host clock/counters
// resetting under a container restart mid-interval) by reporting no
// movement rather than an enormous underflowed delta.
func diffUint64(cur, prev uint64) uint64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func readCounters(procRoot string) (*rawCounters, error) {
	cpuTotal, cpuIdle, err := readProcStatCPU(procRoot + "/stat")
	if err != nil {
		return nil, err
	}
	rx, tx, err := readProcNetDev(procRoot + "/net/dev")
	if err != nil {
		return nil, err
	}
	return &rawCounters{
		at:            time.Now(),
		cpuTotalJifs:  cpuTotal,
		cpuIdleJifs:   cpuIdle,
		networkRxByte: rx,
		networkTxByte: tx,
	}, nil
}

// readProcStatCPU parses the aggregate "cpu " line of /proc/stat: user nice
// system idle iowait irq softirq [steal [guest [guest_nice]]]. idle time is
// idle+iowait (iowait is still idle CPU, just blocked on I/O); total is the
// sum of every field present, so this degrades gracefully on older kernels
// that report fewer columns.
func readProcStatCPU(path string) (total, idle uint64, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		values := make([]uint64, 0, len(fields))
		for _, f := range fields {
			v, parseErr := strconv.ParseUint(f, 10, 64)
			if parseErr != nil {
				return 0, 0, parseErr
			}
			values = append(values, v)
			total += v
		}
		if len(values) >= 4 {
			idle = values[3]
			if len(values) >= 5 {
				idle += values[4] // + iowait
			}
		}
		return total, idle, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	return 0, 0, os.ErrNotExist
}

// readProcNetDev sums received/transmitted bytes across every interface
// except loopback. A container's veth/eth0 pair and a bare-metal host's
// real NICs are indistinguishable from inside /proc/net/dev, so this
// reports whatever traffic this network namespace sees — the same scope
// the rest of this process already runs in.
func readProcNetDev(path string) (rx, tx uint64, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= 2 {
			continue // two header lines
		}
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		iface := strings.TrimSpace(parts[0])
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}
		// Columns: bytes packets errs drop fifo frame compressed multicast
		// (receive) | bytes packets errs drop fifo colls carrier compressed
		// (transmit) -- receive bytes is field 0, transmit bytes is field 8.
		rxBytes, parseErr := strconv.ParseUint(fields[0], 10, 64)
		if parseErr != nil {
			continue
		}
		txBytes, parseErr := strconv.ParseUint(fields[8], 10, 64)
		if parseErr != nil {
			continue
		}
		rx += rxBytes
		tx += txBytes
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	return rx, tx, nil
}

type meminfo struct {
	usedBytes  int64
	totalBytes int64
}

// readMeminfo computes "used" the way `free`/`top` do: total - available,
// not total - free (MemFree alone excludes reclaimable page/buffer cache
// and would make a healthy, well-cached host look far more memory-pressured
// than it is).
func readMeminfo(procRoot string) (meminfo, error) {
	file, err := os.Open(procRoot + "/meminfo")
	if err != nil {
		return meminfo{}, err
	}
	defer file.Close()

	var totalKB, availableKB int64
	haveTotal, haveAvailable := false, false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			totalKB, err = parseMeminfoKB(line)
			if err != nil {
				return meminfo{}, err
			}
			haveTotal = true
		case strings.HasPrefix(line, "MemAvailable:"):
			availableKB, err = parseMeminfoKB(line)
			if err != nil {
				return meminfo{}, err
			}
			haveAvailable = true
		}
		if haveTotal && haveAvailable {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return meminfo{}, err
	}
	if !haveTotal || !haveAvailable {
		return meminfo{}, os.ErrNotExist
	}
	used := totalKB - availableKB
	if used < 0 {
		used = 0
	}
	return meminfo{usedBytes: used * 1024, totalBytes: totalKB * 1024}, nil
}

func parseMeminfoKB(line string) (int64, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, os.ErrInvalid
	}
	return strconv.ParseInt(fields[1], 10, 64)
}
