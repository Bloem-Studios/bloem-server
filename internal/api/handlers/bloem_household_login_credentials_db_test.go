package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// A storefront gives each login a temporary password the person replaces at
// their first sign-in, and an email address of its own: one another account
// already uses is refused as such, so the storefront can say so plainly.

func (f *householdLoginsFixture) passwordChangeRequired(id int) bool {
	f.t.Helper()
	var required bool
	if err := f.pool.QueryRow(f.t.Context(), `SELECT password_change_required FROM users WHERE id = $1`, id).Scan(&required); err != nil {
		f.t.Fatal(err)
	}
	return required
}

func TestCreateLoginWithATemporaryPasswordAndEmail(t *testing.T) {
	f := newHouseholdLoginsFixture(t)
	main := f.main("main")
	login := f.create(main, "sam", fmt.Sprintf(`"max_streams":1,"max_profiles":1,"require_password_change":true,"email":"sam-%s@example.test"`, f.suffix))
	if !f.passwordChangeRequired(login.ID) {
		t.Fatal("login created with require_password_change holds a permanent password")
	}
	plain := f.create(main, "kim", `"max_streams":1,"max_profiles":1`)
	if f.passwordChangeRequired(plain.ID) {
		t.Fatal("login created without require_password_change holds a temporary password")
	}
}

func TestCreateLoginRefusesAnEmailInUse(t *testing.T) {
	f := newHouseholdLoginsFixture(t)
	main := f.main("main")
	for _, email := range []string{
		"main-" + f.suffix + "@example.test", // the main account's own
		"MAIN-" + f.suffix + "@Example.Test", // the same, in other case
	} {
		rec := f.do(http.MethodPost, main, fmt.Sprintf(`{"username":"jo-%s","password":"login password","profile_name":"Jo","max_streams":1,"max_profiles":1,"email":%q}`, f.suffix, email))
		if rec.Code != http.StatusConflict || errorCode(t, rec) != "email_in_use" {
			t.Fatalf("login with email %s: %d %s; want 409 email_in_use", email, rec.Code, rec.Body)
		}
	}
	rec := f.do(http.MethodPost, main, fmt.Sprintf(`{"username":"main-%s","password":"login password","profile_name":"Jo","max_streams":1,"max_profiles":1}`, f.suffix))
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "duplicate" {
		t.Fatalf("login with a username in use: %d %s; want 409 duplicate", rec.Code, rec.Body)
	}
}

func TestMoveProfileToALoginWithATemporaryPasswordAndEmail(t *testing.T) {
	f := newMovesFixture(t)
	main := f.main("main")
	f.addProfile(main, "Main")
	sam := f.addProfile(main, "Sam")
	alex := f.addProfile(main, "Alex")
	rec := f.post(fmt.Sprintf("/accounts/%d/logins/move-profile", main), fmt.Sprintf(
		`{"profile_id":%q,"username":"alex-%s","password":"login password","max_streams":1,"max_profiles":1,"email":"main-%s@example.test"}`,
		alex, f.suffix, f.suffix))
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "email_in_use" {
		t.Fatalf("move with the main account's email: %d %s; want 409 email_in_use", rec.Code, rec.Body)
	}
	if ids, _ := f.profiles(main); !containsString(ids, alex) {
		t.Fatalf("refused move took profile %s off the main account", alex)
	}
	rec = f.post(fmt.Sprintf("/accounts/%d/logins/move-profile", main), fmt.Sprintf(
		`{"profile_id":%q,"username":"sam-%s","password":"login password","max_streams":1,"max_profiles":1,"require_password_change":true,"email":"sam-%s@example.test"}`,
		sam, f.suffix, f.suffix))
	if rec.Code != http.StatusCreated {
		t.Fatalf("move: %d %s", rec.Code, rec.Body)
	}
	var out createdLogin
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	f.ids = append(f.ids, out.ID)
	if !f.passwordChangeRequired(out.ID) {
		t.Fatal("login moved with require_password_change holds a permanent password")
	}
}
