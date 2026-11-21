package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// movesFixture is the household logins fixture with the two move routes.
func newMovesFixture(t *testing.T) *householdLoginsFixture {
	t.Helper()
	f := newHouseholdLoginsFixture(t)
	f.router.Post("/accounts/{account_id}/logins/move-profile", f.h.HandleBloemPlatformHouseholdLoginMoveProfile)
	f.router.Post("/accounts/{account_id}/logins/{login_id}/move-to-main", f.h.HandleBloemPlatformHouseholdLoginMoveToMain)
	return f
}

// addProfile gives an account a profile; an account's first is its primary.
func (f *householdLoginsFixture) addProfile(userID int, name string) string {
	f.t.Helper()
	id := pgstore.NewProfileID()
	store, err := pgstore.NewPostgresProvider(f.pool).ForUser(f.t.Context(), userID)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := store.CreateProfile(f.t.Context(), userstore.Profile{ID: id, Name: name}); err != nil {
		f.t.Fatalf("create profile %s: %v", name, err)
	}
	return id
}

// profiles lists an account's profile ids, and which is primary.
func (f *householdLoginsFixture) profiles(userID int) (ids []string, primary string) {
	f.t.Helper()
	rows, err := f.pool.Query(f.t.Context(), `SELECT id, is_primary FROM user_profiles WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var isPrimary bool
		if err := rows.Scan(&id, &isPrimary); err != nil {
			f.t.Fatal(err)
		}
		ids = append(ids, id)
		if isPrimary {
			primary = id
		}
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return ids, primary
}

func (f *householdLoginsFixture) post(path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *householdLoginsFixture) moveProfile(main int, profileID, username string) *httptest.ResponseRecorder {
	f.t.Helper()
	rec := f.post(fmt.Sprintf("/accounts/%d/logins/move-profile", main), fmt.Sprintf(
		`{"profile_id":%q,"username":%q,"password":"login password","max_streams":1,"max_profiles":1,"transcode_allowed":false}`,
		profileID, username+"-"+f.suffix))
	if rec.Code == http.StatusCreated {
		var out createdLogin
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err == nil {
			f.ids = append(f.ids, out.ID)
		}
	}
	return rec
}

func TestMoveProfileMakesALoginWithIt(t *testing.T) {
	f := newMovesFixture(t)
	main := f.main("main")
	mainPrimary := f.addProfile(main, "Main")
	sam := f.addProfile(main, "Sam")

	rec := f.moveProfile(main, sam, "sam")
	if rec.Code != http.StatusCreated {
		t.Fatalf("move profile: %d %s", rec.Code, rec.Body)
	}
	var login createdLogin
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	if login.HouseholdMainUserID != main || !login.Enabled {
		t.Fatalf("login %+v; want an enabled login of %d", login, main)
	}
	if ids, primary := f.profiles(login.ID); !slices.Equal(ids, []string{sam}) || primary != sam {
		t.Fatalf("login profiles %v (primary %q); want Sam alone, as its primary", ids, primary)
	}
	if ids, primary := f.profiles(main); !slices.Equal(ids, []string{mainPrimary}) || primary != mainPrimary {
		t.Fatalf("main profiles %v (primary %q); want only its primary", ids, primary)
	}
	view, err := f.h.GetAdminAccount(t.Context(), login.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := view.User
	if got.MaxStreams == nil || *got.MaxStreams != 1 || got.MaxProfilesOverride == nil || *got.MaxProfilesOverride != 1 ||
		got.TranscodeAllowed == nil || *got.TranscodeAllowed {
		t.Fatalf("login limits %+v; want 1 stream, 1 profile, no transcoding", got)
	}
}

func TestMoveProfileUnderDisabledMainStartsDisabled(t *testing.T) {
	f := newMovesFixture(t)
	main := f.main("main")
	f.addProfile(main, "Main")
	sam := f.addProfile(main, "Sam")
	if err := f.users.Update(t.Context(), main, models.UpdateUserInput{Enabled: new(false)}); err != nil {
		t.Fatal(err)
	}
	rec := f.moveProfile(main, sam, "sam")
	if rec.Code != http.StatusCreated {
		t.Fatalf("move profile: %d %s", rec.Code, rec.Body)
	}
	var login createdLogin
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	if login.Enabled || !login.HouseholdDisabled {
		t.Fatalf("login under a disabled main %+v; want disabled with household_disabled", login)
	}
}

func TestMoveProfileRefusesThePrimary(t *testing.T) {
	f := newMovesFixture(t)
	main := f.main("main")
	primary := f.addProfile(main, "Main")
	f.addProfile(main, "Sam")
	rec := f.moveProfile(main, primary, "main")
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "primary_profile" {
		t.Fatalf("move the primary: %d %s; want 409 primary_profile", rec.Code, rec.Body)
	}
	if rec := f.moveProfile(main, "no-such-profile", "nobody"); rec.Code != http.StatusNotFound || errorCode(t, rec) != "profile_not_found" {
		t.Fatalf("move an unknown profile: %d %s; want 404 profile_not_found", rec.Code, rec.Body)
	}
}

func TestMoveProfileTakenUsernameMovesNothing(t *testing.T) {
	f := newMovesFixture(t)
	main := f.main("main")
	f.addProfile(main, "Main")
	sam := f.addProfile(main, "Sam")
	f.main("taken")
	rec := f.moveProfile(main, sam, "taken")
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "duplicate" {
		t.Fatalf("move with a taken username: %d %s; want 409 duplicate", rec.Code, rec.Body)
	}
	if ids, _ := f.profiles(main); !slices.Contains(ids, sam) {
		t.Fatalf("Sam left the main account: %v", ids)
	}
}

func TestMoveToMainBringsTheProfilesBackAndDeletesTheLogin(t *testing.T) {
	f := newMovesFixture(t)
	main := f.main("main")
	mainPrimary := f.addProfile(main, "Main")
	login := f.create(main, "sam", `"max_streams":1,"max_profiles":2`)
	loginProfiles, _ := f.profiles(login.ID)
	extra := f.addProfile(login.ID, "Sam's friend")

	rec := f.post(fmt.Sprintf("/accounts/%d/logins/%d/move-to-main", main, login.ID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("move to main: %d %s", rec.Code, rec.Body)
	}
	var out BloemHouseholdLoginMoveToMainResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	moved := append(slices.Clone(loginProfiles), extra)
	slices.Sort(moved)
	slices.Sort(out.ProfileIDs)
	if !slices.Equal(out.ProfileIDs, moved) {
		t.Fatalf("moved %v; want %v", out.ProfileIDs, moved)
	}
	var exists bool
	if err := f.pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, login.ID).Scan(&exists); err != nil || exists {
		t.Fatalf("the login still exists (%v)", err)
	}
	ids, primary := f.profiles(main)
	for _, id := range moved {
		if !slices.Contains(ids, id) {
			t.Errorf("profile %s is not the main account's: %v", id, ids)
		}
	}
	if primary != mainPrimary {
		t.Errorf("main account's primary is %q; want it unchanged", primary)
	}
}

func TestMoveToMainWithoutRoomChangesNothing(t *testing.T) {
	f := newMovesFixture(t)
	main := f.main("main")
	f.addProfile(main, "Main")
	login := f.create(main, "sam", `"max_streams":1,"max_profiles":1`)
	f.exec(`UPDATE users SET max_profiles = 1 WHERE id = $1`, main)

	rec := f.post(fmt.Sprintf("/accounts/%d/logins/%d/move-to-main", main, login.ID), "")
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "profile_limit" {
		t.Fatalf("move to a full main account: %d %s; want 409 profile_limit", rec.Code, rec.Body)
	}
	if ids, _ := f.profiles(login.ID); len(ids) != 1 {
		t.Fatalf("the login has %d profiles; want its one", len(ids))
	}
}

func TestMoveToMainOfAnotherAccountsLogin(t *testing.T) {
	f := newMovesFixture(t)
	mainA := f.main("main-a")
	mainB := f.main("main-b")
	f.addProfile(mainA, "A")
	loginB := f.create(mainB, "sam", `"max_streams":1,"max_profiles":1`)
	rec := f.post(fmt.Sprintf("/accounts/%d/logins/%d/move-to-main", mainA, loginB.ID), "")
	if rec.Code != http.StatusNotFound || errorCode(t, rec) != "login_not_found" {
		t.Fatalf("move another household's login: %d %s; want 404 login_not_found", rec.Code, rec.Body)
	}
}

func (f *householdLoginsFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.t.Context(), sql, args...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

// A profile with parental limits would manage itself as its login's primary
// profile, and Silo applies no library limit to a primary profile.
func TestMoveProfileRefusesARestrictedProfile(t *testing.T) {
	f := newMovesFixture(t)
	main := f.main("main")
	f.addProfile(main, "Main")
	kid := f.addProfile(main, "Kid")
	f.exec(`UPDATE user_profiles SET library_restrictions_enabled = true WHERE user_id = $1 AND id = $2`, main, kid)
	rec := f.moveProfile(main, kid, "kid")
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "restricted_profile" {
		t.Fatalf("move a restricted profile: %d %s; want 409 restricted_profile", rec.Code, rec.Body)
	}
	if ids, _ := f.profiles(main); !slices.Contains(ids, kid) {
		t.Fatalf("the restricted profile left the main account: %v", ids)
	}
}

// Two profiles of one account with the same name make Jellyfin sign-in by
// name ambiguous, so a login whose profile shares a name with one of the main
// account's stays a login.
func TestMoveToMainRefusesAProfileNameTheMainAccountHas(t *testing.T) {
	f := newMovesFixture(t)
	main := f.main("main")
	f.addProfile(main, "Main")
	f.addProfile(main, "Sam")
	login := f.create(main, "sam", `"max_streams":1,"max_profiles":1`)
	rec := f.post(fmt.Sprintf("/accounts/%d/logins/%d/move-to-main", main, login.ID), "")
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "profile_name_conflict" {
		t.Fatalf("move a same-named profile to main: %d %s; want 409 profile_name_conflict", rec.Code, rec.Body)
	}
	if ids, _ := f.profiles(login.ID); len(ids) != 1 {
		t.Fatalf("the login has %d profiles; want its one", len(ids))
	}
}
