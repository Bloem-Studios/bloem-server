package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestProgressSyncSurfacesAreServedByTheRealRouter drives both projections of
// the same batch through the real router.
//
// The point is not that either handler works — that is covered per-handler —
// but that a client hitting /api/v2 gets the finer vocabulary while a Silo
// client hitting /api/v1 keeps getting `ok` from the identical request against
// the identical store. The two must never be one route with a mode flag.
func TestProgressSyncSurfacesAreServedByTheRealRouter(t *testing.T) {
	fixture := newClientSurfaceFixture(t)
	seedWatchCatalog(t, fixture.pool)
	headers := fixture.profileHeaders()

	// 10/1000 is 1%, under the default 5% min-resume floor, so it is accepted
	// and not written; 500/1000 is a real resume point.
	batch := `{"items":[
		{"media_item_id":"` + watchMovieContentID + `","position":10,"duration":1000},
		{"media_item_id":"` + watchSeriesContentID + `","position":500,"duration":1000}
	]}`

	native := performJSONRequest(t, fixture.router, http.MethodPost, "/api/v2/sync/progress", batch, fixture.token, headers)
	if native.Code != http.StatusOK {
		t.Fatalf("v2 sync = %d %s", native.Code, native.Body.String())
	}
	if got, want := syncStatuses(t, native.Body.Bytes()), []string{"ignored", "updated"}; !equalStrings(got, want) {
		t.Errorf("v2 statuses = %v, want %v", got, want)
	}

	legacy := performJSONRequest(t, fixture.router, http.MethodPost, "/api/v1/sync/progress", batch, fixture.token, headers)
	if legacy.Code != http.StatusOK {
		t.Fatalf("v1 sync = %d %s", legacy.Code, legacy.Body.String())
	}
	if got, want := syncStatuses(t, legacy.Body.Bytes()), []string{"ok", "ok"}; !equalStrings(got, want) {
		t.Errorf("v1 statuses = %v, want %v — Silo clients parse this value", got, want)
	}

	// The native route is profile-scoped like every other document it serves.
	withoutProfile := performJSONRequest(t, fixture.router, http.MethodPost, "/api/v2/sync/progress", batch, fixture.token, nil)
	if withoutProfile.Code != http.StatusBadRequest {
		t.Errorf("v2 sync without a profile = %d %s", withoutProfile.Code, withoutProfile.Body.String())
	}

	unauthenticated := performJSONRequest(t, fixture.router, http.MethodPost, "/api/v2/sync/progress", batch, "", headers)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Errorf("v2 sync without a token = %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}
}

func syncStatuses(t *testing.T, body []byte) []string {
	t.Helper()
	var decoded struct {
		Results []struct {
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode sync response %s: %v", body, err)
	}
	statuses := make([]string, 0, len(decoded.Results))
	for _, result := range decoded.Results {
		statuses = append(statuses, result.Status)
	}
	return statuses
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
