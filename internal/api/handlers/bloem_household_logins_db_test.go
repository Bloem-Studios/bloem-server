package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

type householdLoginsFixture struct {
	t      *testing.T
	pool   *pgxpool.Pool
	users  *auth.UserRepository
	h      *AdminHandler
	router chi.Router
	group  int64
	suffix string
	ids    []int
}

func newHouseholdLoginsFixture(t *testing.T) *householdLoginsFixture {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	users := auth.NewUserRepository(pool)
	f := &householdLoginsFixture{t: t, pool: pool, users: users, h: NewAdminHandler(users, pool, pgstore.NewPostgresProvider(pool)), suffix: strconv.FormatInt(time.Now().UnixNano(), 10)}
	if err := pool.QueryRow(ctx, `INSERT INTO access_groups (organization_id, name, max_streams, max_transcodes, transcode_allowed, max_profiles)
		SELECT id, $1, 3, 2, true, 5 FROM organizations WHERE is_default RETURNING id`, "household-plan-"+f.suffix).Scan(&f.group); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE household_main_user_id = ANY($1)`, f.ids)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, f.ids)
		_, _ = pool.Exec(ctx, `DELETE FROM access_groups WHERE id = $1`, f.group)
		pool.Close()
	})
	f.h.AccessGroups = access.NewTenantGroupStore(pool)
	r := chi.NewRouter()
	r.Post("/accounts/{account_id}/logins", f.h.HandleBloemPlatformHouseholdLoginCreate)
	r.Get("/accounts/{account_id}/logins", f.h.HandleBloemPlatformHouseholdLoginList)
	f.router = r
	return f
}

// main creates a customer's account on the fixture's plan group.
func (f *householdLoginsFixture) main(name string) int {
	f.t.Helper()
	user, err := f.users.Create(f.t.Context(), models.CreateUserInput{
		Username: name + "-" + f.suffix, Email: name + "-" + f.suffix + "@example.test", Password: "main password", Role: models.RoleUser, AccessGroupID: &f.group,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	f.ids = append(f.ids, user.ID)
	return user.ID
}

func (f *householdLoginsFixture) do(method string, main int, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, fmt.Sprintf("/accounts/%d/logins", main), strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

type createdLogin struct {
	ID                  int  `json:"id"`
	Enabled             bool `json:"enabled"`
	HouseholdMainUserID int  `json:"household_main_user_id"`
	HouseholdDisabled   bool `json:"household_disabled"`
}

func (f *householdLoginsFixture) create(main int, username string, limits string) createdLogin {
	f.t.Helper()
	rec := f.do(http.MethodPost, main, fmt.Sprintf(`{"username":%q,"password":"login password","profile_name":"Sam",%s}`, username+"-"+f.suffix, limits))
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("create login: %d %s", rec.Code, rec.Body)
	}
	var out createdLogin
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	f.ids = append(f.ids, out.ID)
	return out
}

func TestCreateLoginJoinsMainGroupWithOwnLimits(t *testing.T) {
	f := newHouseholdLoginsFixture(t)
	ctx := t.Context()
	main := f.main("main")
	login := f.create(main, "sam", `"max_streams":1,"max_transcodes":1,"transcode_allowed":true,"max_profiles":1`)
	if login.HouseholdMainUserID != main || !login.Enabled || login.HouseholdDisabled {
		t.Fatalf("created login %+v; want an enabled login of %d", login, main)
	}
	view, err := f.h.GetAdminAccount(ctx, login.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := view.User
	if got.MaxStreams == nil || *got.MaxStreams != 1 || got.MaxTranscodes == nil || *got.MaxTranscodes != 1 ||
		got.TranscodeAllowed == nil || !*got.TranscodeAllowed || got.MaxProfilesOverride == nil || *got.MaxProfilesOverride != 1 {
		t.Fatalf("login limits %+v; want 1 stream, 1 transcode, 1 profile", got)
	}
	if got.AccessGroupID == nil || *got.AccessGroupID != f.group {
		t.Fatalf("login group %v; want the main account's %d", got.AccessGroupID, f.group)
	}
	var profiles int
	var profileName string
	if err := f.pool.QueryRow(ctx, `SELECT count(*), max(name) FROM user_profiles WHERE user_id = $1`, login.ID).Scan(&profiles, &profileName); err != nil {
		t.Fatal(err)
	}
	if profiles != 1 || profileName != "Sam" {
		t.Fatalf("login profiles: %d (%q); want one named Sam", profiles, profileName)
	}
	var sameOrganization bool
	if err := f.pool.QueryRow(ctx, `SELECT l.organization_id = m.organization_id FROM organization_memberships l, organization_memberships m
		WHERE l.account_id = $1 AND m.account_id = $2`, login.ID, main).Scan(&sameOrganization); err != nil || !sameOrganization {
		t.Fatalf("login organization matches the main account's: %t (%v)", sameOrganization, err)
	}

	// No transcodes: transcoding off, no transcode limit written.
	off := f.create(main, "alex", `"max_streams":1,"transcode_allowed":false,"max_profiles":1`)
	view, err = f.h.GetAdminAccount(ctx, off.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.User.TranscodeAllowed == nil || *view.User.TranscodeAllowed || view.User.MaxTranscodes != nil {
		t.Fatalf("login without transcodes: transcode_allowed=%v max_transcodes=%v", view.User.TranscodeAllowed, view.User.MaxTranscodes)
	}
}

func TestCreateLoginUnderDisabledMainStartsDisabled(t *testing.T) {
	f := newHouseholdLoginsFixture(t)
	main := f.main("main")
	if err := f.users.Update(t.Context(), main, models.UpdateUserInput{Enabled: new(false)}); err != nil {
		t.Fatal(err)
	}
	login := f.create(main, "sam", `"max_streams":1,"max_profiles":1`)
	if login.Enabled || !login.HouseholdDisabled {
		t.Fatalf("login created under a disabled main account: %+v; want disabled by the household", login)
	}
	if err := f.users.Update(t.Context(), main, models.UpdateUserInput{Enabled: new(true)}); err != nil {
		t.Fatal(err)
	}
	stored, err := f.users.GetByID(t.Context(), login.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled {
		t.Fatal("the login did not come back with its main account")
	}
}

func TestCreateLoginUnderLoginIsRefused(t *testing.T) {
	f := newHouseholdLoginsFixture(t)
	login := f.create(f.main("main"), "sam", `"max_streams":1,"max_profiles":1`)
	rec := f.do(http.MethodPost, login.ID, `{"username":"nested-`+f.suffix+`","password":"login password","profile_name":"Kim","max_streams":1,"max_profiles":1}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "household_nested") {
		t.Fatalf("login under a login: %d %s; want 409 household_nested", rec.Code, rec.Body)
	}
}

func TestCreateLoginRefusesInvalidRequests(t *testing.T) {
	f := newHouseholdLoginsFixture(t)
	main := f.main("main")
	taken := f.create(main, "sam", `"max_streams":1,"max_profiles":1`)
	_ = taken
	for _, tc := range []struct {
		name   string
		main   int
		body   string
		status int
	}{
		{"no stream", main, `{"username":"a-` + f.suffix + `","password":"login password","profile_name":"A","max_streams":0,"max_profiles":1}`, http.StatusBadRequest},
		{"no profile", main, `{"username":"b-` + f.suffix + `","password":"login password","profile_name":"B","max_streams":1,"max_profiles":0}`, http.StatusBadRequest},
		{"no profile name", main, `{"username":"c-` + f.suffix + `","password":"login password","max_streams":1,"max_profiles":1}`, http.StatusBadRequest},
		{"unknown main", 2147483000, `{"username":"d-` + f.suffix + `","password":"login password","profile_name":"D","max_streams":1,"max_profiles":1}`, http.StatusNotFound},
		{"username taken", main, `{"username":"sam-` + f.suffix + `","password":"login password","profile_name":"E","max_streams":1,"max_profiles":1}`, http.StatusConflict},
	} {
		if rec := f.do(http.MethodPost, tc.main, tc.body); rec.Code != tc.status {
			t.Errorf("%s: %d %s; want %d", tc.name, rec.Code, rec.Body, tc.status)
		}
	}
}

func TestListLoginsInCreationOrder(t *testing.T) {
	f := newHouseholdLoginsFixture(t)
	main := f.main("main")
	first := f.create(main, "sam", `"max_streams":1,"max_profiles":1`)
	second := f.create(main, "alex", `"max_streams":1,"max_profiles":1`)
	rec := f.do(http.MethodGet, main, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Logins []struct {
			ID                int    `json:"id"`
			Username          string `json:"username"`
			Enabled           bool   `json:"enabled"`
			HouseholdDisabled bool   `json:"household_disabled"`
		} `json:"logins"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Logins) != 2 || out.Logins[0].ID != first.ID || out.Logins[1].ID != second.ID || out.Logins[0].Username != "sam-"+f.suffix || !out.Logins[1].Enabled {
		t.Fatalf("logins %+v; want sam then alex", out.Logins)
	}
	if rec := f.do(http.MethodGet, 2147483000, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("list for an unknown main account: %d", rec.Code)
	}
}

func TestDeleteMainDeletesItsLogins(t *testing.T) {
	f := newHouseholdLoginsFixture(t)
	main := f.main("main")
	login := f.create(main, "sam", `"max_streams":1,"max_profiles":1`)
	if err := f.h.DeleteAdminAccount(t.Context(), main, -1, 0); err != nil {
		t.Fatalf("delete main account: %v", err)
	}
	var left int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM users WHERE id = ANY($1)`, []int{main, login.ID}).Scan(&left); err != nil || left != 0 {
		t.Fatalf("accounts left after deleting the main account: %d (%v)", left, err)
	}
}

// Disabling a main account through the account service also ends its logins'
// live connections, as it does the main account's own.
func TestDisablingMainEndsItsLoginsLiveSessions(t *testing.T) {
	f := newHouseholdLoginsFixture(t)
	main := f.main("main")
	login := f.create(main, "sam", `"max_streams":1,"max_profiles":1`)
	var mu sync.Mutex
	var revoked []int
	f.h.OnUserSessionsRevoked = func(_ context.Context, id int) {
		mu.Lock()
		defer mu.Unlock()
		revoked = append(revoked, id)
	}
	if _, err := f.h.UpdateAdminAccount(t.Context(), main, -1, 0, models.UpdateUserInput{Enabled: new(false)}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(revoked, main) || !slices.Contains(revoked, login.ID) {
		t.Fatalf("live sessions ended for %v; want the main account %d and its login %d", revoked, main, login.ID)
	}
}

func TestCapabilitiesReportHouseholdLogins(t *testing.T) {
	rec := httptest.NewRecorder()
	NewBloemSystemHandler(nil).HandleCapabilities(rec, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/capabilities", nil))
	var body struct {
		FeatureTokens []string `json:"feature_tokens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(body.FeatureTokens, "household_logins_v1") {
		t.Fatalf("feature tokens %v; want household_logins_v1", body.FeatureTokens)
	}
}
