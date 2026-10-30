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

func newArrivalsRequest(t *testing.T, h *BloemNewArrivalsHandler, query string, claims *auth.Claims) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/admin/platform/new-arrivals"+query, nil)
	if claims != nil {
		req = req.WithContext(middleware.SetClaims(req.Context(), claims))
	}
	rec := httptest.NewRecorder()
	h.HandleGet(rec, req)
	return rec
}

func TestBloemNewArrivalsRefusesOtherCredentialsAndBadQueries(t *testing.T) {
	h := NewBloemNewArrivalsHandler(nil)
	user := &auth.Claims{UserID: 7, Role: "user", TokenType: auth.TokenTypeAPIKey}
	if rec := newArrivalsRequest(t, h, "", user); rec.Code != http.StatusForbidden {
		t.Fatalf("user key = %d %s", rec.Code, rec.Body.String())
	}
	admin := &auth.Claims{UserID: 7, Role: "admin", TokenType: auth.TokenTypeAPIKey}
	for _, query := range []string{"?days=0", "?days=32", "?limit=0", "?limit=51", "?library=x", "?other=1"} {
		if rec := newArrivalsRequest(t, h, query, admin); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", query, rec.Code, rec.Body.String())
		}
	}
}

// Films first seen in a chosen library inside the window are listed, and so
// are series with episodes first seen there, counted; older items, other
// libraries and disabled libraries are not.
func TestBloemNewArrivalsListsRecentFilmsAndEpisodes(t *testing.T) {
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
	var admin int
	if err := pool.QueryRow(ctx, `INSERT INTO users (username,email,password_hash,role,enabled) VALUES ('arrivals-admin','arrivals@example.test','x','admin',true) RETURNING id`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO media_folders (id,name,type,enabled,sort_order) VALUES
		(911,'Films','movies',true,1), (912,'Shows','series',true,2), (913,'Private','movies',true,3), (914,'Off','movies',false,4)`)
	exec(`INSERT INTO media_items (content_id,type,title,year) VALUES
		('na-new','movie','New Film',2026), ('na-old','movie','Old Film',2001),
		('na-private','movie','Private Film',2026), ('na-off','movie','Disabled Film',2026),
		('na-show','series','Long Show',2019), ('na-fresh','series','Fresh Show',2026)`)
	exec(`INSERT INTO media_item_libraries (content_id,media_folder_id,first_seen_at) VALUES
		('na-new',911,now()-interval '1 day'), ('na-old',911,now()-interval '60 days'),
		('na-private',913,now()-interval '1 day'), ('na-off',914,now()-interval '1 day'),
		('na-show',912,now()-interval '400 days'), ('na-fresh',912,now()-interval '2 days')`)
	exec(`INSERT INTO seasons (content_id,series_id,season_number) VALUES ('na-show-5','na-show',5), ('na-fresh-1','na-fresh',1)`)
	exec(`INSERT INTO episodes (content_id,series_id,season_id,season_number,episode_number,title) VALUES
		('na-e1','na-show','na-show-5',5,1,'A'), ('na-e2','na-show','na-show-5',5,2,'B'), ('na-e0','na-show','na-show-5',5,0,'Old'),
		('na-f1','na-fresh','na-fresh-1',1,1,'Pilot')`)
	exec(`INSERT INTO episode_libraries (episode_id,media_folder_id,first_seen_at) VALUES
		('na-e1',912,now()-interval '3 days'), ('na-e2',912,now()-interval '1 day'), ('na-e0',912,now()-interval '90 days'),
		('na-f1',912,now()-interval '2 days')`)

	h := NewBloemNewArrivalsHandler(pool)
	key := &auth.Claims{UserID: admin, Role: "admin", TokenType: auth.TokenTypeAPIKey}
	rec := newArrivalsRequest(t, h, "?days=7&library=911&library=912&library=914", key)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	var got BloemNewArrivals
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	byID := map[string]BloemNewArrival{}
	for _, item := range got.Items {
		byID[item.ContentID] = item
	}
	if len(byID) != 3 {
		t.Fatalf("items = %+v, want New Film, Long Show and Fresh Show", got.Items)
	}
	if film := byID["na-new"]; film.Kind != "movie" || film.Title != "New Film" || film.Year == nil || *film.Year != 2026 {
		t.Fatalf("film = %+v", film)
	}
	if show := byID["na-show"]; show.Kind != "series" || show.NewEpisodes != 2 || show.NewSeries {
		t.Fatalf("long show = %+v, want 2 new episodes of an existing series", show)
	}
	if fresh := byID["na-fresh"]; !fresh.NewSeries || fresh.NewEpisodes != 1 {
		t.Fatalf("fresh show = %+v, want a new series", fresh)
	}
	if got.Items[0].ContentID != "na-new" && got.Items[0].ContentID != "na-show" {
		t.Fatalf("not newest first: %+v", got.Items)
	}
	if strings.Contains(rec.Body.String(), "Private Film") || strings.Contains(rec.Body.String(), "Disabled Film") {
		t.Fatalf("listed a library that was not asked for or is disabled: %s", rec.Body.String())
	}
}
