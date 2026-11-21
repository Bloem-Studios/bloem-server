package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

// householdPoolFixture serves the pool routes beside the logins routes.
func newHouseholdPoolFixture(t *testing.T) *householdLoginsFixture {
	t.Helper()
	f := newHouseholdLoginsFixture(t)
	f.router.Put("/accounts/{account_id}/household-pool", f.h.HandleBloemPlatformHouseholdPoolPut)
	f.router.Get("/accounts/{account_id}/household-pool", f.h.HandleBloemPlatformHouseholdPoolGet)
	f.router.Delete("/accounts/{account_id}/household-pool", f.h.HandleBloemPlatformHouseholdPoolDelete)
	prefix := "household-pool-route-" + f.suffix + "-"
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.WithoutCancel(t.Context()), `DELETE FROM playback_capacity_reservations WHERE session_id LIKE $1`, prefix+"%")
	})
	return f
}

func (f *householdLoginsFixture) poolRoute(method string, account int, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, fmt.Sprintf("/accounts/%d/household-pool", account), strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// reserve records a live (or, with lease "-", an expired) reservation.
func (f *householdLoginsFixture) reserve(session string, account int, transcode bool, lease string) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.t.Context(), `INSERT INTO playback_capacity_reservations(session_id,account_id,profile_id,is_transcode,lease_until)
		VALUES($1,$2,'p',$3,now() + $4::interval)`, "household-pool-route-"+f.suffix+"-"+session, account, transcode, lease); err != nil {
		f.t.Fatal(err)
	}
}

type householdPoolView struct {
	AccountID       int `json:"account_id"`
	MaxStreams      int `json:"max_streams"`
	MaxTranscodes   int `json:"max_transcodes"`
	StreamsInUse    int `json:"streams_in_use"`
	TranscodesInUse int `json:"transcodes_in_use"`
	Logins          int `json:"logins"`
}

func decodePool(t *testing.T, rec *httptest.ResponseRecorder) householdPoolView {
	t.Helper()
	var view householdPoolView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return view
}

func TestHouseholdPoolPutAndGet(t *testing.T) {
	f := newHouseholdPoolFixture(t)
	main := f.main("main")
	login := f.create(main, "sam", `"max_streams":1,"max_profiles":1`)
	f.reserve("main", main, false, "1 minute")
	f.reserve("login", login.ID, true, "1 minute")
	f.reserve("gone", login.ID, true, "-1 minute")

	rec := f.poolRoute(http.MethodPut, main, `{"max_streams":3,"max_transcodes":2}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	want := householdPoolView{AccountID: main, MaxStreams: 3, MaxTranscodes: 2, StreamsInUse: 2, TranscodesInUse: 1, Logins: 1}
	if got := decodePool(t, rec); got != want {
		t.Fatalf("put answered %+v, want %+v", got, want)
	}
	rec = f.poolRoute(http.MethodPut, main, `{"max_streams":4,"max_transcodes":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("second put: %d %s", rec.Code, rec.Body)
	}
	rec = f.poolRoute(http.MethodGet, main, "")
	want.MaxStreams, want.MaxTranscodes = 4, 0
	if got := decodePool(t, rec); rec.Code != http.StatusOK || got != want {
		t.Fatalf("get: %d %+v, want %+v", rec.Code, got, want)
	}
}

func TestHouseholdPoolRefusesLoginAndAdmin(t *testing.T) {
	f := newHouseholdPoolFixture(t)
	main := f.main("main")
	login := f.create(main, "sam", `"max_streams":1,"max_profiles":1`)
	if rec := f.poolRoute(http.MethodPut, login.ID, `{"max_streams":3,"max_transcodes":2}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "household_nested") {
		t.Fatalf("pool on a login: %d %s", rec.Code, rec.Body)
	}
	admin, err := f.users.Create(t.Context(), models.CreateUserInput{
		Username: "pool-admin-" + f.suffix, Email: "pool-admin-" + f.suffix + "@example.test", Password: "admin password", Role: models.RoleAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.ids = append(f.ids, admin.ID)
	if rec := f.poolRoute(http.MethodPut, admin.ID, `{"max_streams":3,"max_transcodes":2}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "household_main_ineligible") {
		t.Fatalf("pool on an admin: %d %s", rec.Code, rec.Body)
	}
	if rec := f.poolRoute(http.MethodPut, 2147480000, `{"max_streams":3,"max_transcodes":2}`); rec.Code != http.StatusNotFound {
		t.Fatalf("pool on an unknown account: %d %s", rec.Code, rec.Body)
	}
}

// Both values are required: a missing one must not quietly mean unlimited.
func TestHouseholdPoolRefusesNegative(t *testing.T) {
	f := newHouseholdPoolFixture(t)
	main := f.main("main")
	for _, body := range []string{`{"max_streams":-1,"max_transcodes":0}`, `{"max_streams":1,"max_transcodes":-1}`, `{"max_streams":1}`, `{}`, `nope`} {
		if rec := f.poolRoute(http.MethodPut, main, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("put %s: %d %s; want 400", body, rec.Code, rec.Body)
		}
	}
	if rec := f.poolRoute(http.MethodGet, main, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("a refused put left a pool: %d %s", rec.Code, rec.Body)
	}
}

func TestHouseholdPoolDeleteIsIdempotent(t *testing.T) {
	f := newHouseholdPoolFixture(t)
	main := f.main("main")
	if rec := f.poolRoute(http.MethodPut, main, `{"max_streams":3,"max_transcodes":2}`); rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if rec := f.poolRoute(http.MethodDelete, main, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := f.poolRoute(http.MethodGet, main, ""); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "household_pool_not_found") {
		t.Fatalf("get after delete: %d %s", rec.Code, rec.Body)
	}
	if rec := f.poolRoute(http.MethodDelete, main, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("second delete: %d %s", rec.Code, rec.Body)
	}
}

func TestCapabilitiesReportHouseholdPool(t *testing.T) {
	rec := httptest.NewRecorder()
	NewBloemSystemHandler(nil).HandleCapabilities(rec, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/capabilities", nil))
	var body struct {
		FeatureTokens []string `json:"feature_tokens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(body.FeatureTokens, "household_pool_v1") {
		t.Fatalf("feature tokens %v; want household_pool_v1", body.FeatureTokens)
	}
}
