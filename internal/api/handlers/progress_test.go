package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// syncProgressStatuses decodes a POST /sync/progress response and returns the
// per-item status values in request order.
func syncProgressStatuses(t *testing.T, body []byte) []string {
	t.Helper()

	var resp struct {
		Results []struct {
			MediaItemID string `json:"media_item_id"`
			Status      string `json:"status"`
			Error       string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode sync response: %v (body %s)", err, body)
	}

	statuses := make([]string, 0, len(resp.Results))
	for _, result := range resp.Results {
		statuses = append(statuses, result.Status)
	}
	return statuses
}

// postSyncProgress runs one POST /sync/progress request against the handler and
// returns the recorded response.
func postSyncProgress(t *testing.T, handler *ProgressHandler, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync/progress", strings.NewReader(body))
	req = req.WithContext(newAuthorizedPlaybackContext())
	rec := httptest.NewRecorder()
	handler.HandleSyncProgress(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	return rec
}

// A client cannot act on a batch result that says "ok" for both a row the server
// wrote and a row the min-resume floor discarded: the discarded row looks like a
// landed write, so the client stops resending it and the position is lost. The
// wire vocabulary is `updated` / `ignored` / `error`.
func TestSyncProgressUsesContractStatusVocabulary(t *testing.T) {
	store := newPlaybackTestStore(t)
	handler := &ProgressHandler{storeProvider: testUserStoreProvider{store: store}}

	// 10/1000 = 1%, under the 5% default min-resume floor; 500/1000 = 50% is a
	// real resume point; the empty identifier is the existing per-item error.
	body := `{"items":[
		{"media_item_id":"movie-below-floor","position":10,"duration":1000},
		{"media_item_id":"movie-above-floor","position":500,"duration":1000},
		{"media_item_id":"","position":500,"duration":1000}
	]}`
	rec := postSyncProgress(t, handler, body)

	got := syncProgressStatuses(t, rec.Body.Bytes())
	want := []string{"ignored", "updated", "error"}
	if len(got) != len(want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("statuses = %v, want %v", got, want)
		}
	}

	if raw := rec.Body.String(); strings.Contains(raw, `"status":"ok"`) {
		t.Fatalf("response carries a legacy ok status: %s", raw)
	}

	// The existing error message is part of the contract's `error` field and
	// must not drift with the status vocabulary.
	var resp struct {
		Results []struct {
			Error string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	if resp.Results[2].Error != "media_item_id is required" {
		t.Fatalf("error = %q, want %q", resp.Results[2].Error, "media_item_id is required")
	}

	// The statuses have to describe what the store actually holds: `updated`
	// means a row landed, `ignored` means none did.
	ctx := context.Background()
	if row, err := store.GetProgress(ctx, "profile-1", "movie-above-floor"); err != nil || row == nil {
		t.Fatalf("above-floor progress = (%v, %v), want a stored row", row, err)
	}
	row, err := store.GetProgress(ctx, "profile-1", "movie-below-floor")
	if err != nil {
		t.Fatalf("get below-floor progress: %v", err)
	}
	if row != nil {
		t.Fatalf("below-floor progress = %+v, want no stored row", row)
	}
}

// The offline-queued path (items carrying `updated_at`) reports the same
// vocabulary: it is the path that queues events while disconnected, so a client
// that mistakes a discard for a write there loses positions silently.
func TestSyncProgressOfflineItemsReportContractStatuses(t *testing.T) {
	store := newPlaybackTestStore(t)
	handler := &ProgressHandler{storeProvider: testUserStoreProvider{store: store}}

	eventAt := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	body := `{"items":[
		{"media_item_id":"offline-below-floor","position":10,"duration":1000,"updated_at":"` + eventAt + `"},
		{"media_item_id":"offline-above-floor","position":500,"duration":1000,"updated_at":"` + eventAt + `"},
		{"media_item_id":"offline-bad-time","position":500,"duration":1000,"updated_at":"not-a-time"}
	]}`
	rec := postSyncProgress(t, handler, body)

	got := syncProgressStatuses(t, rec.Body.Bytes())
	want := []string{"ignored", "updated", "error"}
	if len(got) != len(want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("statuses = %v, want %v", got, want)
		}
	}
}

// An offline event that loses last-write-wins against a newer stored event is
// not a write either. Reporting it as `updated` is the same lie as reporting a
// floor discard as a success.
func TestSyncProgressReportsLastWriteWinsLossAsIgnored(t *testing.T) {
	store := newPlaybackTestStore(t)
	handler := &ProgressHandler{storeProvider: testUserStoreProvider{store: store}}

	newer := time.Now().UTC().Add(-time.Minute)
	if err := store.SetProgressAt(context.Background(), "profile-1", "movie-lww", 900, 1000, false, newer); err != nil {
		t.Fatalf("seed newer progress: %v", err)
	}

	older := newer.Add(-time.Hour).Format(time.RFC3339)
	body := `{"items":[{"media_item_id":"movie-lww","position":300,"duration":1000,"updated_at":"` + older + `"}]}`
	rec := postSyncProgress(t, handler, body)

	got := syncProgressStatuses(t, rec.Body.Bytes())
	if len(got) != 1 || got[0] != "ignored" {
		t.Fatalf("statuses = %v, want [ignored]", got)
	}

	row, err := store.GetProgress(context.Background(), "profile-1", "movie-lww")
	if err != nil || row == nil {
		t.Fatalf("progress after stale sync = (%v, %v), want the seeded row", row, err)
	}
	if row.PositionSeconds != 900 {
		t.Fatalf("PositionSeconds = %v, want the newer 900 to survive", row.PositionSeconds)
	}
}
