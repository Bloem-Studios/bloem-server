package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/cache"
	evt "github.com/Silo-Server/silo-server/internal/events"
)

// syncProgressStatuses decodes a POST /api/bloem/v1/sync/progress response and returns the
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

// postSyncProgress runs one POST /api/bloem/v1/sync/progress request against the handler and
// returns the recorded response.
func postSyncProgress(t *testing.T, handler *ProgressHandler, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, NativeAPIPrefix+"/sync/progress", strings.NewReader(body))
	req = req.WithContext(newAuthorizedPlaybackContext())
	rec := httptest.NewRecorder()
	handler.HandleBloemSyncProgress(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	return rec
}

// A client cannot act on a batch result that says "ok" for both a row the server
// wrote and a row the min-resume floor discarded: the discarded row looks like a
// landed write, so the client stops resending it and the position is lost. The
// wire vocabulary is `updated` / `ignored` / `error`.
func TestBloemSyncProgressUsesContractStatusVocabulary(t *testing.T) {
	store := newPlaybackTestStore(t)
	handler := &ProgressHandler{storeProvider: testUserStoreProvider{store: store}, LibraryLookup: allowAllProgressLookup{}}

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
	if resp.Results[2].Error != syncErrMissingMediaItemID {
		t.Fatalf("error = %q, want %q", resp.Results[2].Error, syncErrMissingMediaItemID)
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
func TestBloemSyncProgressOfflineItemsReportContractStatuses(t *testing.T) {
	store := newPlaybackTestStore(t)
	handler := &ProgressHandler{storeProvider: testUserStoreProvider{store: store}, LibraryLookup: allowAllProgressLookup{}}

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
func TestBloemSyncProgressReportsLastWriteWinsLossAsIgnored(t *testing.T) {
	store := newPlaybackTestStore(t)
	handler := &ProgressHandler{storeProvider: testUserStoreProvider{store: store}, LibraryLookup: allowAllProgressLookup{}}

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

// The Silo-compatible projection must keep saying "ok". Silo clients parse that
// value, and the finer vocabulary above is precisely why it is tempting to
// change here — so the temptation is nailed down by a test rather than by a
// comment.
func TestV1SyncProgressKeepsTheSiloStatusVocabulary(t *testing.T) {
	store := newPlaybackTestStore(t)
	handler := &ProgressHandler{storeProvider: testUserStoreProvider{store: store}}

	body := `{"items":[
		{"media_item_id":"movie-below-floor","position":10,"duration":1000},
		{"media_item_id":"movie-above-floor","position":500,"duration":1000},
		{"media_item_id":"","position":500,"duration":1000}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync/progress", strings.NewReader(body))
	req = req.WithContext(newAuthorizedPlaybackContext())
	rec := httptest.NewRecorder()
	handler.HandleSyncProgress(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	got := syncProgressStatuses(t, rec.Body.Bytes())
	want := []string{"ok", "ok", "error"}
	if len(got) != len(want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("statuses = %v, want %v", got, want)
		}
	}
}

// allowAllProgressLookup answers every requested id as visible; the tests that
// use it are about status vocabulary, not access enforcement.
type allowAllProgressLookup struct{}

func (allowAllProgressLookup) GetItemsInFolder(context.Context, []string, int) (map[string]bool, error) {
	return nil, nil
}

func (allowAllProgressLookup) FilterAccessibleContentIDs(_ context.Context, ids []string, _, _ []int, _ string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// The native sync must apply the viewer's library and rating scope before it
// writes anything: a profile may not record progress (or fan out user-state
// events) for an item it cannot see. An inaccessible item and a nonexistent one
// answer identically, so the response is not an existence oracle.
func TestBloemSyncProgressEnforcesViewerAccess(t *testing.T) {
	store := newPlaybackTestStore(t)
	lookup := &fakeProgressLookup{accessible: map[string]bool{"visible": true}}
	hub := evt.NewHub("test", &cache.NoopEventBus{})
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	handler := &ProgressHandler{storeProvider: testUserStoreProvider{store: store}, LibraryLookup: lookup, EventsHub: hub}

	body := `{"items":[
		{"media_item_id":"hidden","position":500,"duration":1000},
		{"media_item_id":"visible","position":500,"duration":1000},
		{"media_item_id":"does-not-exist","position":500,"duration":1000}
	]}`
	req := httptest.NewRequest(http.MethodPost, NativeAPIPrefix+"/sync/progress", strings.NewReader(body))
	ctx := access.SetScope(newAuthorizedPlaybackContext(), access.Scope{UserID: 1, AllowedLibraryIDs: []int{4}, MaxContentRating: "PG"})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.HandleBloemSyncProgress(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Results []struct {
			MediaItemID string `json:"media_item_id"`
			Status      string `json:"status"`
			Error       string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 3 || resp.Results[0].Status != "error" || resp.Results[1].Status != "updated" || resp.Results[2].Status != "error" {
		t.Fatalf("results = %+v", resp.Results)
	}
	if resp.Results[0].Error != resp.Results[2].Error {
		t.Fatalf("hidden and missing answers differ: %q vs %q", resp.Results[0].Error, resp.Results[2].Error)
	}
	if lookup.gotRating != "PG" || len(lookup.gotAllowed) != 1 || lookup.gotAllowed[0] != 4 {
		t.Fatalf("scope not forwarded: %+v", lookup)
	}
	for _, id := range []string{"hidden", "does-not-exist"} {
		if row, err := store.GetProgress(context.Background(), "profile-1", id); err != nil || row != nil {
			t.Fatalf("%s progress = (%+v, %v), want no row", id, row, err)
		}
	}

	var published []string
	for done := false; !done; {
		select {
		case e := <-events:
			var payload struct {
				ContentID string `json:"content_id"`
			}
			_ = json.Unmarshal(e.Data, &payload)
			published = append(published, payload.ContentID)
		case <-time.After(200 * time.Millisecond):
			done = true
		}
	}
	if len(published) != 1 || published[0] != "visible" {
		t.Fatalf("user-state events for %v, want only [visible]", published)
	}
}

// Without a resolved viewer scope the native sync fails closed.
func TestBloemSyncProgressWithoutScopeWritesNothing(t *testing.T) {
	store := newPlaybackTestStore(t)
	handler := &ProgressHandler{storeProvider: testUserStoreProvider{store: store}, LibraryLookup: allowAllProgressLookup{}}
	req := httptest.NewRequest(http.MethodPost, NativeAPIPrefix+"/sync/progress",
		strings.NewReader(`{"items":[{"media_item_id":"x","position":500,"duration":1000}]}`))
	ctx := apimw.SetProfileID(apimw.SetClaims(context.Background(), &auth.Claims{UserID: 1, Role: "user", TokenType: auth.TokenTypeAccess}), "profile-1")
	rec := httptest.NewRecorder()
	handler.HandleBloemSyncProgress(rec, req.WithContext(ctx))
	if rec.Code == http.StatusOK {
		t.Fatalf("status = 200, want a failure (body %s)", rec.Body.String())
	}
	if row, _ := store.GetProgress(context.Background(), "profile-1", "x"); row != nil {
		t.Fatalf("unscoped write landed: %+v", row)
	}
}

// The batch is bounded the same way the v2 operation is: at most
// bloemSyncProgressMaxItems items and a 1 MiB body.
func TestBloemSyncProgressRejectsOversizedBatches(t *testing.T) {
	store := newPlaybackTestStore(t)
	handler := &ProgressHandler{storeProvider: testUserStoreProvider{store: store}, LibraryLookup: allowAllProgressLookup{}}
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, NativeAPIPrefix+"/sync/progress", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handler.HandleBloemSyncProgress(rec, req.WithContext(newAuthorizedPlaybackContext()))
		return rec
	}

	items := make([]string, 101)
	for i := range items {
		items[i] = `{"media_item_id":"m` + strconv.Itoa(i) + `","position":500,"duration":1000}`
	}
	if rec := post(`{"items":[` + strings.Join(items, ",") + `]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("101 items: status = %d, want 400", rec.Code)
	}
	if row, _ := store.GetProgress(context.Background(), "profile-1", "m0"); row != nil {
		t.Fatalf("over-cap batch wrote rows")
	}
	if rec := post(`{"items":[` + strings.Join(items[:100], ",") + `]}`); rec.Code != http.StatusOK {
		t.Fatalf("100 items: status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	huge := `{"items":[{"media_item_id":"big","position":500,"duration":1000,"pad":"` + strings.Repeat("a", 1<<20) + `"}]}`
	if rec := post(huge); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: status = %d, want 413", rec.Code)
	}
}
