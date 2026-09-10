package organizations_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/requests"
	"github.com/Silo-Server/silo-server/migrations"
)

func TestRequestOrganizationIsolation(t *testing.T) {
	pool := ownershipDatabase(t)
	if err := database.RunMigrations(t.Context(), pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	users := []int{}
	for _, org := range []string{"request-a", "request-b"} {
		var organizationID int64
		if err := pool.QueryRow(t.Context(), `INSERT INTO organizations(name,slug) VALUES($1,$1) RETURNING id`, org).Scan(&organizationID); err != nil {
			t.Fatal(err)
		}
		var userID int
		if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,organization_id) VALUES($1,$2,'fixture',$3) RETURNING id`, org, org+"@example.test", organizationID).Scan(&userID); err != nil {
			t.Fatal(err)
		}
		users = append(users, userID)
	}
	repo := requests.NewRepository(pool, nil)
	create := func(id string, user, tmdb int) (*requests.Request, error) {
		return repo.CreateRequest(t.Context(), requests.CreateRequestRecord{ID: id, Requester: requests.Viewer{UserID: user}, Input: requests.CreateRequestInput{MediaType: requests.MediaTypeMovie, TMDBID: tmdb, Title: "Same title"}})
	}
	if _, err := create("a", users[0], 42); err != nil {
		t.Fatal(err)
	}
	if _, err := create("b", users[1], 42); err != nil {
		t.Fatalf("foreign request blocked creation: %v", err)
	}
	for i, id := range []string{"a", "b"} {
		matches, err := repo.ListActiveByTMDB(t.Context(), users[i], requests.MediaTypeMovie, []int{42})
		if err != nil || len(matches) != 1 || matches[42].ID != id {
			t.Fatalf("user %d matches=%+v err=%v", users[i], matches, err)
		}
	}

	var teammate int
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,organization_id) SELECT 'teammate','teammate@example.test','fixture',organization_id FROM users WHERE id=$1 RETURNING id`, users[0]).Scan(&teammate); err != nil {
		t.Fatal(err)
	}
	if _, err := create("same-org", teammate, 42); !errors.Is(err, requests.ErrAlreadyRequested) {
		t.Fatalf("same-org duplicate: %v", err)
	}
	matches, err := repo.ListActiveByTMDB(t.Context(), teammate, requests.MediaTypeMovie, []int{42})
	if err != nil || len(matches) != 1 || matches[42].ID != "a" {
		t.Fatalf("same-org lookup: %+v %v", matches, err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE media_requests SET organization_id=(SELECT organization_id FROM users WHERE id=$1) WHERE id='a'`, users[1]); err == nil {
		t.Fatal("request accepted a foreign organization")
	}
	// A downgrade must not delete requests to restore the old unique index.
	migration, err := migrations.FS.ReadFile("sql/20260910000708_organization_requests.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, _ := strings.Cut(string(migration), "-- +goose Down")
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, downErr := tx.Exec(t.Context(), down)
	_ = tx.Rollback(context.Background())
	if downErr == nil || !strings.Contains(downErr.Error(), "overlapping active requests") {
		t.Fatalf("downgrade: %v", downErr)
	}
	settings, err := repo.GetSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings.GlobalRequests = new(true)
	if _, err := repo.UpdateSettings(t.Context(), settings); err == nil {
		t.Fatal("global mode accepted conflicting active requests")
	} else {
		var validation *requests.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("wrong mode conflict: %v", err)
		}
	}
	unchanged, err := repo.GetSettings(t.Context())
	if err != nil || unchanged.GlobalRequests == nil || *unchanged.GlobalRequests {
		t.Fatalf("rejected mode change persisted: %+v %v", unchanged, err)
	}
	// Database uniqueness still arbitrates simultaneous requests inside an org.
	var wins atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 8 {
		wg.Go(func() {
			<-start
			_, err := create(fmt.Sprintf("race-%d", i), users[0], 99)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, requests.ErrAlreadyRequested) {
				t.Errorf("concurrent request: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("duplicate winners=%d", wins.Load())
	}
	if _, err := pool.Exec(t.Context(), `UPDATE media_requests SET outcome='failed' WHERE id IN ('a','b')`); err != nil {
		t.Fatal(err)
	}
	count, err := repo.DeleteFailedByTMDB(t.Context(), teammate, requests.MediaTypeMovie, 42)
	if err != nil || count != 1 {
		t.Fatalf("cleanup count=%d err=%v", count, err)
	}
	if _, err := repo.GetRequest(t.Context(), "b"); err != nil {
		t.Fatalf("foreign failed request deleted: %v", err)
	}
	// Unknown accounts cannot use cleanup or lookup to fall back to global scope.
	matches, err = repo.ListActiveByTMDB(t.Context(), -1, requests.MediaTypeMovie, []int{99})
	if err != nil || len(matches) != 0 {
		t.Fatalf("unknown user lookup: %+v %v", matches, err)
	}
	count, err = repo.DeleteFailedByTMDB(t.Context(), -1, requests.MediaTypeMovie, 42)
	if err != nil || count != 0 {
		t.Fatalf("unknown user cleanup: %d %v", count, err)
	}
	settings, err = repo.GetSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings.GlobalRequests = new(true)
	if _, err = repo.UpdateSettings(t.Context(), settings); err != nil {
		t.Fatalf("enable global: %v", err)
	}
	matches, err = repo.ListActiveByTMDB(t.Context(), users[1], requests.MediaTypeMovie, []int{99})
	if err != nil || len(matches) != 1 {
		t.Fatalf("global lookup: %+v %v", matches, err)
	}
	if _, err = create("global-duplicate", users[1], 99); !errors.Is(err, requests.ErrAlreadyRequested) {
		t.Fatalf("global duplicate: %v", err)
	}
	// Older settings clients omit the new field; that must preserve the mode.
	settings.GlobalRequests = nil
	saved, err := repo.UpdateSettings(t.Context(), settings)
	if err != nil || saved.GlobalRequests == nil || !*saved.GlobalRequests {
		t.Fatalf("omitted mode reset global: %+v %v", saved, err)
	}
	settings.GlobalRequests = new(false)
	if _, err = repo.UpdateSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	matches, err = repo.ListActiveByTMDB(t.Context(), users[1], requests.MediaTypeMovie, []int{99})
	if err != nil || len(matches) != 0 {
		t.Fatalf("organization mode still shares queue: %+v %v", matches, err)
	}
	if _, err = create("b-local", users[1], 99); err != nil {
		t.Fatalf("local request after switching: %v", err)
	}
	// Hold a global-mode update open, then observe a new insert waiting on it.
	// The inserted row must use the committed new mode, not its earlier snapshot.
	if _, err = pool.Exec(t.Context(), `UPDATE media_requests SET outcome='failed' WHERE id='b-local'`); err != nil {
		t.Fatal(err)
	}
	modeTx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer modeTx.Rollback(context.Background())
	if _, err = modeTx.Exec(t.Context(), `UPDATE request_settings SET global_requests=true WHERE id=true`); err != nil {
		t.Fatal(err)
	}
	insertDone := make(chan error, 1)
	go func() { _, err := create("during-mode-switch", users[1], 888); insertDone <- err }()
	waitCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(waitCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%INSERT INTO media_requests%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-insertDone:
			t.Fatalf("insert did not wait for mode change: %v", err)
		case <-waitCtx.Done():
			t.Fatal("insert never waited for mode change")
		case <-ticker.C:
		}
	}
	if err := modeTx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-insertDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-waitCtx.Done():
		t.Fatal("insert did not resume")
	}
	var scope int64
	if err := pool.QueryRow(t.Context(), `SELECT request_scope_id FROM media_requests WHERE id='during-mode-switch'`).Scan(&scope); err != nil || scope != 0 {
		t.Fatalf("concurrent insert scope=%d err=%v", scope, err)
	}

}

func TestRequestOwnershipMigrationBackfillsExistingRows(t *testing.T) {
	pool := ownershipDatabase(t)
	baseline := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.FS, "sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "20260910000708_organization_requests.sql" {
			continue
		}
		name := "sql/" + entry.Name()
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		baseline[name] = &fstest.MapFile{Data: body}
	}
	if err := database.RunMigrations(t.Context(), pool, baseline, "sql"); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), `
 INSERT INTO users(username,email,password_hash,organization_id)
 SELECT slug,slug||'@example.test','fixture',id FROM organizations;
 INSERT INTO media_requests(id,media_type,tmdb_id,title,status,requested_by_user_id)
 SELECT 'existing-'||id,'movie',id,'Preserved title','pending',id FROM users;
 `)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RunMigrations(t.Context(), pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_requests r JOIN users u ON u.id=r.requested_by_user_id WHERE r.organization_id=u.organization_id AND r.id='existing-'||u.id AND r.title='Preserved title' AND r.status='pending'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("preserved requests=%d err=%v", count, err)
	}
}
