package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/migrations"
)

func libraryFiguresRequest(t *testing.T, h *BloemLibraryFiguresHandler, target string, claims *auth.Claims, admin *auth.AdminContextClaims) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if claims != nil {
		req = req.WithContext(middleware.SetClaims(req.Context(), claims))
	}
	if admin != nil {
		req = req.WithContext(middleware.SetAdminContextClaims(req.Context(), *admin))
	}
	rec := httptest.NewRecorder()
	h.HandleGet(rec, req)
	return rec
}

// Only a Platform context or an administrator's unscoped API key gets past the
// handler's own check; every refusal is no-store.
func TestBloemLibraryFiguresRefusesOtherCredentials(t *testing.T) {
	h := NewBloemLibraryFiguresHandler(nil)
	path := NativeAPIPrefix + "/admin/platform/library-figures"
	for name, tc := range map[string]struct {
		claims *auth.Claims
		admin  *auth.AdminContextClaims
	}{
		"no credential":        {},
		"organization context": {admin: &auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopeOrganization}},
		"user API key":         {claims: &auth.Claims{UserID: 7, Role: "user", TokenType: auth.TokenTypeAPIKey}},
		"scoped admin API key": {claims: &auth.Claims{UserID: 7, Role: "admin", TokenType: auth.TokenTypeAPIKey, APIKeyScopes: []string{auth.ScopeAdminMonitorRead}}},
		"admin access token":   {claims: &auth.Claims{UserID: 7, Role: "admin", TokenType: auth.TokenTypeAccess}},
	} {
		rec := libraryFiguresRequest(t, h, path, tc.claims, tc.admin)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"error":"insufficient_platform_authority"`) || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: response=%d %q %s", name, rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
		}
	}
	platform := &auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopePlatform}
	if rec := libraryFiguresRequest(t, h, path+"?refresh=1", nil, platform); rec.Code != http.StatusBadRequest {
		t.Fatalf("query: response=%d %s", rec.Code, rec.Body.String())
	}
	if rec := libraryFiguresRequest(t, h, path, nil, platform); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"library_figures_unavailable"`) {
		t.Fatalf("no database: response=%d %s", rec.Code, rec.Body.String())
	}
}

// Each library counts the items and episodes listed in it, an item listed in
// two libraries counts in both, and an empty library reports zeros.
func TestBloemLibraryFiguresCountsEachLibrary(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool := newCompatAdapterDisposableDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	var admin, disabled, user int
	for _, row := range []struct {
		target *int
		name   string
		role   string
		on     bool
	}{{&admin, "figures-admin", "admin", true}, {&disabled, "figures-disabled", "admin", false}, {&user, "figures-user", "user", true}} {
		if err := pool.QueryRow(ctx, `INSERT INTO users (username,email,password_hash,role,enabled) VALUES ($1,$2,'x',$3,$4) RETURNING id`, row.name, row.name+"@example.test", row.role, row.on).Scan(row.target); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO media_folders (id,name,type,enabled,sort_order) VALUES
		(901,'Films','movies',true,2), (902,'Shows','series',true,1), (903,'Books','ebooks',false,3), (904,'Empty','mixed',true,4)`)
	exec(`INSERT INTO media_items (content_id,type,title) VALUES
		('fig-m1','movie','One'), ('fig-m2','movie','Two'), ('fig-s1','series','Show'),
		('fig-b1','ebook','Book'), ('fig-a1','audiobook','Listen')`)
	exec(`INSERT INTO media_item_libraries (content_id,media_folder_id) VALUES
		('fig-m1',901), ('fig-m2',901), ('fig-m2',902), ('fig-s1',902), ('fig-b1',903), ('fig-a1',903)`)
	exec(`INSERT INTO seasons (content_id,series_id,season_number) VALUES ('fig-s1-1','fig-s1',1)`)
	exec(`INSERT INTO episodes (content_id,series_id,season_id,season_number,episode_number,title) VALUES
		('fig-e1','fig-s1','fig-s1-1',1,1,'A'), ('fig-e2','fig-s1','fig-s1-1',1,2,'B'), ('fig-e3','fig-s1','fig-s1-1',1,3,'C')`)
	exec(`INSERT INTO episode_libraries (episode_id,media_folder_id) VALUES ('fig-e1',902), ('fig-e2',902), ('fig-e3',902)`)

	h := NewBloemLibraryFiguresHandler(pool)
	path := NativeAPIPrefix + "/admin/platform/library-figures"
	rec := libraryFiguresRequest(t, h, path, &auth.Claims{UserID: admin, Role: "admin", TokenType: auth.TokenTypeAPIKey}, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
	}
	var got BloemLibraryFigures
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.CountedAt.IsZero() {
		t.Fatal("counted_at is missing")
	}
	byID := map[int64]BloemLibraryFigure{}
	order := []int64{}
	for _, f := range got.Libraries {
		if f.LibraryID >= 901 && f.LibraryID <= 904 {
			byID[f.LibraryID] = f
			order = append(order, f.LibraryID)
		}
	}
	if len(order) != 4 || order[0] != 902 || order[1] != 901 {
		t.Fatalf("library order = %v", order)
	}
	want := map[int64]BloemLibraryFigure{
		901: {LibraryID: 901, Name: "Films", Type: "movies", Enabled: true, Movies: 2},
		902: {LibraryID: 902, Name: "Shows", Type: "series", Enabled: true, Movies: 1, Series: 1, Episodes: 3},
		903: {LibraryID: 903, Name: "Books", Type: "ebooks", Enabled: false, Audiobooks: 1, Ebooks: 1},
		904: {LibraryID: 904, Name: "Empty", Type: "mixed", Enabled: true},
	}
	for id, w := range want {
		if byID[id] != w {
			t.Fatalf("library %d = %+v, want %+v", id, byID[id], w)
		}
	}

	// Authority is rechecked against the account, not trusted from the token.
	for name, id := range map[string]int{"disabled admin": disabled, "demoted account": user} {
		rec := libraryFiguresRequest(t, h, path, nil, &auth.AdminContextClaims{AccountID: id, Scope: auth.AdminScopePlatform})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: response=%d %s", name, rec.Code, rec.Body.String())
		}
	}
}
