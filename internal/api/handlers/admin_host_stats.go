package handlers

import (
	"net/http"
	"time"

	"github.com/Silo-Server/silo-server/internal/nodemetrics"
)

// HostStatsSource returns the most recently sampled host resource usage.
// Implementations must never block — nodemetrics.Sampler.Snapshot reads a
// background-refreshed snapshot, so this handler never waits on /proc I/O.
type HostStatsSource interface {
	Snapshot() nodemetrics.Snapshot
}

type hostStatsResponse struct {
	// Supported is the capability flag every caller must check first: false
	// on a non-Linux host, or when even the first /proc sample has not
	// succeeded (temporarily unreadable, or this build has no sampler
	// wired in at all). Stats is only ever present alongside true.
	Supported bool                   `json:"supported"`
	Stats     *hostStatsSnapshotJSON `json:"stats,omitempty"`
}

type hostStatsSnapshotJSON struct {
	CPUPercent           float64   `json:"cpu_percent"`
	MemoryUsedBytes      int64     `json:"memory_used_bytes"`
	MemoryTotalBytes     int64     `json:"memory_total_bytes"`
	NetworkRxBytesPerSec float64   `json:"network_rx_bytes_per_sec"`
	NetworkTxBytesPerSec float64   `json:"network_tx_bytes_per_sec"`
	SampledAt            time.Time `json:"sampled_at"`
}

// HandleGetHostStats handles GET /admin/host-stats. Always 200: an
// unsupported platform or a not-yet-configured sampler is a real, expected
// state reported via the `supported` flag (this codebase's established
// capability-detection pattern — see TenantsAvailability), not an error.
func (h *AdminHandler) HandleGetHostStats(w http.ResponseWriter, r *http.Request) {
	if h.HostStatsSource == nil {
		writeJSON(w, http.StatusOK, hostStatsResponse{Supported: false})
		return
	}
	snap := h.HostStatsSource.Snapshot()
	if !snap.Available || snap.System == nil {
		writeJSON(w, http.StatusOK, hostStatsResponse{Supported: false})
		return
	}
	writeJSON(w, http.StatusOK, hostStatsResponse{
		Supported: true,
		Stats: &hostStatsSnapshotJSON{
			CPUPercent:           float64(snap.System.CPUPct),
			MemoryUsedBytes:      snap.System.MemUsedMB * bytesPerMB,
			MemoryTotalBytes:     snap.System.MemTotalMB * bytesPerMB,
			NetworkRxBytesPerSec: float64(snap.System.NetRxBps) / bitsPerByte,
			NetworkTxBytesPerSec: float64(snap.System.NetTxBps) / bitsPerByte,
			SampledAt:            snap.SampledAt,
		},
	})
}

// bytesPerMB and bitsPerByte convert nodemetrics' MB/bits-per-second units
// back to the bytes this endpoint has always reported, so the wire format is
// unchanged even though the underlying sampler's units differ from Bloem's
// former hoststats package.
const (
	bytesPerMB  = int64(1024 * 1024)
	bitsPerByte = 8
)
