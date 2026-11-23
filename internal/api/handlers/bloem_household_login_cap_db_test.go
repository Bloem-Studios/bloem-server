package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// loginCapFixture serves the login cap routes beside the logins and move
// routes.
func newLoginCapFixture(t *testing.T) *householdLoginsFixture {
	t.Helper()
	f := newMovesFixture(t)
	f.router.Put("/accounts/{account_id}/household-login-cap", f.h.HandleBloemPlatformHouseholdLoginCapPut)
	f.router.Get("/accounts/{account_id}/household-login-cap", f.h.HandleBloemPlatformHouseholdLoginCapGet)
	f.router.Delete("/accounts/{account_id}/household-login-cap", f.h.HandleBloemPlatformHouseholdLoginCapDelete)
	return f
}

func (f *householdLoginsFixture) capRoute(method string, account int, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, fmt.Sprintf("/accounts/%d/household-login-cap", account), strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

type householdLoginCapView struct {
	AccountID int  `json:"account_id"`
	MaxLogins int  `json:"max_logins"`
	Logins    int  `json:"logins"`
	OverCap   bool `json:"over_cap"`
}

func decodeLoginCap(t *testing.T, rec *httptest.ResponseRecorder) householdLoginCapView {
	t.Helper()
	var view householdLoginCapView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return view
}

func (f *householdLoginsFixture) tryCreate(main int, username string) *httptest.ResponseRecorder {
	f.t.Helper()
	rec := f.do(http.MethodPost, main, fmt.Sprintf(`{"username":%q,"password":"login password","profile_name":"Kim","max_streams":1,"max_profiles":1}`, username+"-"+f.suffix))
	if rec.Code == http.StatusCreated {
		var out createdLogin
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err == nil {
			f.ids = append(f.ids, out.ID)
		}
	}
	return rec
}

// With a cap of 2 a household gets two logins, made fresh or from a profile,
// and the third is refused either way; moving a login back to a profile is
// still allowed, and then there is room again.
func TestHouseholdLoginCapRefusesTheLoginAfterTheCap(t *testing.T) {
	f := newLoginCapFixture(t)
	main := f.main("main")
	if rec := f.capRoute(http.MethodPut, main, `{"max_logins":2}`); rec.Code != http.StatusOK {
		t.Fatalf("put cap: %d %s", rec.Code, rec.Body)
	}
	f.addProfile(main, "Main")
	first := f.create(main, "sam", `"max_streams":1,"max_profiles":1`)
	alex := f.addProfile(main, "Alex")
	if rec := f.moveProfile(main, alex, "alex"); rec.Code != http.StatusCreated {
		t.Fatalf("second login, moved from a profile: %d %s", rec.Code, rec.Body)
	}
	view := decodeLoginCap(t, f.capRoute(http.MethodGet, main, ""))
	if want := (householdLoginCapView{AccountID: main, MaxLogins: 2, Logins: 2}); view != want {
		t.Fatalf("cap at two logins: %+v, want %+v", view, want)
	}

	rec := f.tryCreate(main, "kim")
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "household_login_cap_reached" {
		t.Fatalf("third login: %d %s; want 409 household_login_cap_reached", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "2") {
		t.Fatalf("refusal %s does not say how many logins the plan allows", rec.Body)
	}
	jo := f.addProfile(main, "Jo")
	if rec := f.moveProfile(main, jo, "jo"); rec.Code != http.StatusConflict || errorCode(t, rec) != "household_login_cap_reached" {
		t.Fatalf("third login from a profile: %d %s; want 409 household_login_cap_reached", rec.Code, rec.Body)
	}
	if ids, _ := f.profiles(main); !containsString(ids, jo) {
		t.Fatalf("refused move took profile %s off the main account: %v", jo, ids)
	}

	if rec := f.post(fmt.Sprintf("/accounts/%d/logins/%d/move-to-main", main, first.ID), ""); rec.Code != http.StatusOK {
		t.Fatalf("move back at the cap: %d %s; want allowed", rec.Code, rec.Body)
	}
	if rec := f.tryCreate(main, "kim"); rec.Code != http.StatusCreated {
		t.Fatalf("login after one moved back: %d %s; want room again", rec.Code, rec.Body)
	}
}

// Lowering the cap below a household's logins leaves them working and blocks
// new ones; the view reports the household over its cap.
func TestHouseholdLoginCapLoweredBelowTheLoginsKeepsThem(t *testing.T) {
	f := newLoginCapFixture(t)
	main := f.main("main")
	sam := f.create(main, "sam", `"max_streams":1,"max_profiles":1`)
	f.create(main, "alex", `"max_streams":1,"max_profiles":1`)
	rec := f.capRoute(http.MethodPut, main, `{"max_logins":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("lower cap: %d %s", rec.Code, rec.Body)
	}
	if got, want := decodeLoginCap(t, rec), (householdLoginCapView{AccountID: main, MaxLogins: 1, Logins: 2, OverCap: true}); got != want {
		t.Fatalf("lowered cap: %+v, want %+v", got, want)
	}
	var enabled bool
	if err := f.pool.QueryRow(t.Context(), `SELECT enabled FROM users WHERE id = $1`, sam.ID).Scan(&enabled); err != nil || !enabled {
		t.Fatalf("login after the cap was lowered: enabled=%v err=%v; want it still working", enabled, err)
	}
	if rec := f.tryCreate(main, "kim"); rec.Code != http.StatusConflict || errorCode(t, rec) != "household_login_cap_reached" {
		t.Fatalf("login over the cap: %d %s; want 409 household_login_cap_reached", rec.Code, rec.Body)
	}
}

func TestHouseholdLoginCapRoutes(t *testing.T) {
	f := newLoginCapFixture(t)
	main := f.main("main")
	if rec := f.capRoute(http.MethodGet, main, ""); rec.Code != http.StatusNotFound || errorCode(t, rec) != "household_login_cap_not_found" {
		t.Fatalf("get without a cap: %d %s", rec.Code, rec.Body)
	}
	for _, body := range []string{`{}`, `{"max_logins":-1}`, `not json`} {
		if rec := f.capRoute(http.MethodPut, main, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("put %s: %d %s; want 400", body, rec.Code, rec.Body)
		}
	}
	login := f.create(main, "sam", `"max_streams":1,"max_profiles":1`)
	if rec := f.capRoute(http.MethodPut, login.ID, `{"max_logins":2}`); rec.Code != http.StatusConflict || errorCode(t, rec) != "household_nested" {
		t.Fatalf("cap on a login: %d %s; want 409 household_nested", rec.Code, rec.Body)
	}
	if rec := f.capRoute(http.MethodPut, main, `{"max_logins":0}`); rec.Code != http.StatusOK {
		t.Fatalf("put 0: %d %s", rec.Code, rec.Body)
	}
	if rec := f.tryCreate(main, "kim"); rec.Code != http.StatusConflict {
		t.Fatalf("login under a cap of 0: %d %s; want refused", rec.Code, rec.Body)
	}
	for i := 0; i < 2; i++ {
		if rec := f.capRoute(http.MethodDelete, main, ""); rec.Code != http.StatusNoContent {
			t.Fatalf("delete %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	if rec := f.tryCreate(main, "kim"); rec.Code != http.StatusCreated {
		t.Fatalf("login with the cap removed: %d %s; want no cap", rec.Code, rec.Body)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
