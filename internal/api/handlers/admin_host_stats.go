package handlers

import (
	"net/http"
	"time"

	"github.com/Silo-Server/silo-server/internal/hoststats"
)

// HostStatsSource returns the most recently sampled host resource usage.
// Implementations must never block — hoststats.Sampler.Get reads a
// background-refreshed snapshot, so this handler never waits on /proc I/O.
type HostStatsSource interface {
	Get() hoststats.Snapshot
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
	snap := h.HostStatsSource.Get()
	if !snap.Supported {
		writeJSON(w, http.StatusOK, hostStatsResponse{Supported: false})
		return
	}
	writeJSON(w, http.StatusOK, hostStatsResponse{
		Supported: true,
		Stats: &hostStatsSnapshotJSON{
			CPUPercent:           snap.CPUPercent,
			MemoryUsedBytes:      snap.MemoryUsedBytes,
			MemoryTotalBytes:     snap.MemoryTotalBytes,
			NetworkRxBytesPerSec: snap.NetworkRxBytesPerSec,
			NetworkTxBytesPerSec: snap.NetworkTxBytesPerSec,
			SampledAt:            snap.SampledAt,
		},
	})
}
