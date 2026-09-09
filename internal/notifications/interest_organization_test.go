package notifications

import (
	"context"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Stored interests are deliberately stale: every account is interested in
// every library. Only current organization ownership/grants authorize fanout.
func TestFanoutCandidatesRespectCurrentOrganizationAccess(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(t.Context(), `
 CREATE TEMP TABLE organizations(id bigint PRIMARY KEY,status text);
 CREATE TEMP TABLE users(id integer PRIMARY KEY,organization_id bigint);
 CREATE TEMP TABLE media_folders(id integer PRIMARY KEY,organization_id bigint);
 CREATE TEMP TABLE organization_library_grants(organization_id bigint,media_folder_id integer);
 CREATE TEMP TABLE profile_series_interest(user_id integer,profile_id text,library_id integer,series_id text,favorite boolean,watchlist boolean,continue_watching boolean,next_up_candidate boolean,last_completed_episode_key integer,next_expected_episode_key integer,last_notified_episode_key integer,updated_at timestamptz);
 INSERT INTO organizations VALUES(1,'active'),(2,'active');
 INSERT INTO users VALUES(10,1),(20,2);
 INSERT INTO media_folders VALUES(11,1),(22,2),(33,NULL),(44,NULL);
 INSERT INTO organization_library_grants VALUES(1,33),(2,33);
 INSERT INTO profile_series_interest SELECT u.id,'profile-'||u.id,f.id,'series',true,false,false,false,NULL,NULL,NULL,now() FROM users u CROSS JOIN media_folders f;
 `)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewInterestRepository(pool)
	check := func(library int, want []int) {
		t.Helper()
		rows, err := repo.ListActiveBySeries(t.Context(), tx, library, "series")
		if err != nil {
			t.Fatal(err)
		}
		got := []int{}
		for _, row := range rows {
			got = append(got, row.UserID)
		}
		slices.Sort(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("library %d: recipients %v, want %v", library, got, want)
		}
	}
	check(11, []int{10})
	check(22, []int{20})
	check(33, []int{10, 20})
	check(44, []int{})
	if _, err = tx.Exec(t.Context(), `DELETE FROM organization_library_grants WHERE organization_id=1 AND media_folder_id=33`); err != nil {
		t.Fatal(err)
	}
	check(33, []int{20})
	if _, err = tx.Exec(t.Context(), `UPDATE organizations SET status='suspended' WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	check(22, []int{})
	check(33, []int{})
	check(11, []int{10})
}
