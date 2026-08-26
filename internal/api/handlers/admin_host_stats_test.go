package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/hoststats"
)

type fakeHostStatsSource struct {
	snapshot hoststats.Snapshot
}

func (f fakeHostStatsSource) Get() hoststats.Snapshot { return f.snapshot }

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
	h := &AdminHandler{HostStatsSource: fakeHostStatsSource{snapshot: hoststats.Snapshot{Supported: false}}}

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
	h := &AdminHandler{HostStatsSource: fakeHostStatsSource{snapshot: hoststats.Snapshot{
		Supported:            true,
		CPUPercent:           42.5,
		MemoryUsedBytes:      1000,
		MemoryTotalBytes:     4000,
		NetworkRxBytesPerSec: 123.4,
		NetworkTxBytesPerSec: 56.7,
		SampledAt:            sampledAt,
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
	if resp.Stats.CPUPercent != 42.5 {
		t.Errorf("CPUPercent = %v, want 42.5", resp.Stats.CPUPercent)
	}
	if resp.Stats.MemoryUsedBytes != 1000 || resp.Stats.MemoryTotalBytes != 4000 {
		t.Errorf("memory = %d/%d, want 1000/4000", resp.Stats.MemoryUsedBytes, resp.Stats.MemoryTotalBytes)
	}
	if !resp.Stats.SampledAt.Equal(sampledAt) {
		t.Errorf("SampledAt = %v, want %v", resp.Stats.SampledAt, sampledAt)
	}
}
