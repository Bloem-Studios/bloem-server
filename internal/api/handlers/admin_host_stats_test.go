package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/nodemetrics"
)

type fakeHostStatsSource struct {
	snapshot nodemetrics.Snapshot
}

func (f fakeHostStatsSource) Snapshot() nodemetrics.Snapshot { return f.snapshot }

func TestHandleGetHostStats_NoSourceConfigured(t *testing.T) {
	h := &AdminHandler{}

	req := httptest.NewRequest(http.MethodGet, "/admin/host-stats", nil)
	rr := httptest.NewRecorder()
	h.HandleGetHostStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp hostStatsResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Supported {
		t.Fatal("expected supported=false when no source is configured")
	}
	if resp.Stats != nil {
		t.Fatal("expected stats to be absent when unsupported")
	}
}

func TestHandleGetHostStats_UnsupportedSnapshot(t *testing.T) {
	h := &AdminHandler{HostStatsSource: fakeHostStatsSource{snapshot: nodemetrics.Snapshot{Available: false}}}

	req := httptest.NewRequest(http.MethodGet, "/admin/host-stats", nil)
	rr := httptest.NewRecorder()
	h.HandleGetHostStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when unsupported", rr.Code)
	}
	var resp hostStatsResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Supported {
		t.Fatal("expected supported=false")
	}
	if resp.Stats != nil {
		t.Fatal("expected stats to be absent when unsupported")
	}
}

func TestHandleGetHostStats_SupportedSnapshot(t *testing.T) {
	sampledAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := &AdminHandler{HostStatsSource: fakeHostStatsSource{snapshot: nodemetrics.Snapshot{
		Available: true,
		SampledAt: sampledAt,
		System: &nodemetrics.SystemStats{
			CPUPct:     42,
			MemUsedMB:  1,
			MemTotalMB: 4,
			NetRxBps:   1000,
			NetTxBps:   500,
		},
	}}}

	req := httptest.NewRequest(http.MethodGet, "/admin/host-stats", nil)
	rr := httptest.NewRecorder()
	h.HandleGetHostStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp hostStatsResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Supported {
		t.Fatal("expected supported=true")
	}
	if resp.Stats == nil {
		t.Fatal("expected stats to be present when supported")
	}
	if resp.Stats.CPUPercent != 42 {
		t.Errorf("CPUPercent = %v, want 42", resp.Stats.CPUPercent)
	}
	if resp.Stats.MemoryUsedBytes != 1024*1024 || resp.Stats.MemoryTotalBytes != 4*1024*1024 {
		t.Errorf("memory = %d/%d, want %d/%d", resp.Stats.MemoryUsedBytes, resp.Stats.MemoryTotalBytes, 1024*1024, 4*1024*1024)
	}
	if resp.Stats.NetworkRxBytesPerSec != 125 || resp.Stats.NetworkTxBytesPerSec != 62.5 {
		t.Errorf("network = %v/%v, want 125/62.5", resp.Stats.NetworkRxBytesPerSec, resp.Stats.NetworkTxBytesPerSec)
	}
	if !resp.Stats.SampledAt.Equal(sampledAt) {
		t.Errorf("SampledAt = %v, want %v", resp.Stats.SampledAt, sampledAt)
	}
}
