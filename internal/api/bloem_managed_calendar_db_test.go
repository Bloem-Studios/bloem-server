//go:build integration

package api

import (
	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"strconv"
	"testing"
	"time"
)

func TestManagedCalendarUsesSiloPersonalFollowing(t *testing.T) {
	pool := coverRecipeDatabase(t)
	var userID, folder int
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,password_hash,role,enabled) VALUES('calendar-personal-fixture','synthetic','user',true) RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	store, err := pgstore.NewPostgresProvider(pool).ForUser(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	profile, other := uuid.NewString(), uuid.NewString()
	for _, id := range []string{profile, other} {
		if err := store.CreateProfile(t.Context(), userstore.Profile{ID: id, Name: "Synthetic calendar viewer"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name) VALUES('movies','Synthetic calendar films') RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"favorite", "watchlist", "progress", "other-profile", "unrelated"} {
		exec(`INSERT INTO media_items(content_id,type,title,release_date) VALUES($1,'movie',$1,'2026-10-12')`, id)
		exec(`INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, id, folder)
	}
	exec(`INSERT INTO user_favorites(user_id,profile_id,media_item_id) VALUES($1,$2,'favorite'),($1,$3,'other-profile')`, userID, profile, other)
	exec(`INSERT INTO user_watchlist(user_id,profile_id,media_item_id) VALUES($1,$2,'watchlist')`, userID, profile)
	exec(`INSERT INTO user_watch_progress(user_id,profile_id,media_item_id,completed) VALUES($1,$2,'progress',true)`, userID, profile)
	repo := catalog.NewCalendarRepository(pool)
	scope := access.Scope{UserID: userID, ProfileID: profile, ProfileVerified: true, AllowedLibraryIDs: []int{folder}}
	source := &managedCalendarSource{resolver: managedCalendarResolverFixture{scope: scope}, pool: pool, repo: repo, handler: handlers.NewCalendarHandler(repo, nil, nil, nil)}
	p := managedtracking.Profile{AccountID: strconv.Itoa(userID), ProfileID: profile, AccountActive: true, Active: true}
	day := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	events, err := source.Events(t.Context(), p, day, day)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"favorite": true, "watchlist": true, "progress": true}
	if len(events) != len(want) {
		t.Fatalf("expected only personal calendar entries, got %d", len(events))
	}
	for _, event := range events {
		if !want[event.ContentId] {
			t.Fatalf("unrelated profile/catalog item exported: %s", event.ContentId)
		}
		if event.ContentId == "progress" && !event.Watched {
			t.Fatal("completed personal progress omitted")
		}
	}
	before, err := source.Authority(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	exec(`DELETE FROM user_favorites WHERE user_id=$1 AND profile_id=$2`, userID, profile)
	after, err := source.Authority(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("personal membership removal did not invalidate authority")
	}
	exec(`DELETE FROM user_watchlist WHERE user_id=$1 AND profile_id=$2`, userID, profile)
	exec(`DELETE FROM user_watch_progress WHERE user_id=$1 AND profile_id=$2`, userID, profile)
	events, err = source.Events(t.Context(), p, day, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("empty personal calendar exported %d catalog entries", len(events))
	}
}
