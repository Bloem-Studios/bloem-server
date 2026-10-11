//go:build integration

package nativestorage

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The real INSERT must run before this gate: its immediate source FK is the
// lock that made an attachment race with installation-first management.
type attachmentInsertGate struct {
	pgx.Tx
	inserted chan<- int
	release  <-chan struct{}
}

func (g attachmentInsertGate) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	tag, err := g.Tx.Exec(ctx, sql, arguments...)
	if err != nil || !strings.HasPrefix(sql, "INSERT INTO library_storage_locations") {
		return tag, err
	}
	g.inserted <- int(g.Conn().PgConn().PID())
	select {
	case <-g.release:
		return tag, nil
	case <-ctx.Done():
		return tag, ctx.Err()
	}
}

func attachmentSourceFixture(t *testing.T) (*pgxpool.Pool, storagesource.SourceConfig) {
	t.Helper()
	pool, source := storageSourceFixture(t)
	// Native management requires an installation that has actually started;
	// attachment's older fixture alone intentionally exercises no runtime.
	if _, err := pool.Exec(t.Context(), `UPDATE plugin_installations SET runtime_generation=1 WHERE id=$1`, source.InstallationID); err != nil {
		t.Fatal(err)
	}
	return pool, source
}

func attachmentManagementActor(t *testing.T, pool *pgxpool.Pool) auth.AdminContextClaims {
	t.Helper()
	actor := auth.AdminContextClaims{Scope: auth.AdminScopePlatform, SessionID: uuid.NewString(), ExpiresAt: time.Now().Add(time.Hour)}
	name := "attachment-" + uuid.NewString()
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role,enabled) VALUES($1,$2,'synthetic','admin',true) RETURNING id,account_incarnation_id`, name, name+"@example.test").Scan(&actor.AccountID, &actor.AccountIncarnationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO auth_sessions(id,user_id,device_name,expires_at) VALUES($1,$2,'Synthetic attachment fixture',now()+interval '1 hour')`, actor.SessionID, actor.AccountID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth_sessions WHERE id=$1`, actor.SessionID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor.AccountID)
	})
	return actor
}

func waitAttachmentLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool, waiter, holder int, result <-chan error) string {
	t.Helper()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		var blocked bool
		var query string
		if err := pool.QueryRow(ctx, `SELECT $2::integer=ANY(pg_blocking_pids(pid)),query FROM pg_stat_activity WHERE pid=$1`, waiter, holder).Scan(&blocked, &query); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return query
		}
		select {
		case <-poll.C:
		case err := <-result:
			t.Fatalf("operation finished before expected database lock wait: %v; last query: %s", err, query)
		case <-ctx.Done():
			t.Fatalf("expected database lock wait did not appear; last query: %s", query)
		}
	}
}

func attachmentRowCounts(t *testing.T, pool *pgxpool.Pool, source storagesource.SourceConfig, name string) (int, int) {
	t.Helper()
	var folders, locations int
	if err := pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM media_folders WHERE name=$1),
		(SELECT count(*) FROM library_storage_locations WHERE source_key=$2)`, name, source.Key).Scan(&folders, &locations); err != nil {
		t.Fatal(err)
	}
	return folders, locations
}

func TestLibraryAttachmentSerializesWithNativeManagement(t *testing.T) {
	pool, source := attachmentSourceFixture(t)
	actor := attachmentManagementActor(t, pool)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	inserted := make(chan int, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	defer resume()
	attachmentResult := make(chan error, 1)
	name := "attachment-lock-" + uuid.NewString()
	go func() {
		_, err := catalog.NewFolderRepository(pool).Create(ctx, catalog.CreateFolderInput{
			Type: "ebooks", Name: name,
			AttachStorage: func(ctx context.Context, tx pgx.Tx, folderID int) error {
				return NewLibraryLocations(pool).AttachTx(ctx, attachmentInsertGate{Tx: tx, inserted: inserted, release: release}, source.Key, folderID)
			},
		})
		attachmentResult <- err
	}()
	var attachmentPID int
	select {
	case attachmentPID = <-inserted:
	case err := <-attachmentResult:
		t.Fatalf("attachment did not reach insertion: %v", err)
	case <-ctx.Done():
		t.Fatal("attachment did not reach insertion")
	}
	mutation, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mutation.Rollback(context.Background()) }()
	mutationPID := int(mutation.Conn().PgConn().PID())
	managementResult := make(chan error, 1)
	go func() {
		err := resourcetenancy.NewStore(pool).RequireNativeManagementTx(ctx, mutation, actor, source.Key, source.InstallationID, source.OwnerID, true)
		if err == nil {
			_, err = mutation.Exec(ctx, `UPDATE bloem_storage_sources SET configuration_revision=configuration_revision+1 WHERE key=$1`, source.Key)
		}
		if err == nil {
			err = mutation.Commit(ctx)
		} else {
			_ = mutation.Rollback(context.Background())
		}
		managementResult <- err
	}()
	query := waitAttachmentLock(t, ctx, pool, mutationPID, attachmentPID, managementResult)
	t.Logf("native management blocked by attachment at: %s", query)
	resume()
	// On the original order, management waits for the inserted source FK while
	// attachment waits for management's installation lock: record the real cycle.
	if strings.Contains(query, "FROM bloem_storage_sources") {
		waitAttachmentLock(t, ctx, pool, attachmentPID, mutationPID, attachmentResult)
		t.Log("observed mutual pg_blocking_pids: attachment -> management -> attachment")
	}
	var attachmentErr, managementErr error
	select {
	case attachmentErr = <-attachmentResult:
	case <-ctx.Done():
		t.Fatal("attachment did not finish")
	}
	select {
	case managementErr = <-managementResult:
	case <-ctx.Done():
		t.Fatal("management did not finish")
	}
	folders, locations := attachmentRowCounts(t, pool, source, name)
	var revision int64
	if err := pool.QueryRow(ctx, `SELECT configuration_revision FROM bloem_storage_sources WHERE key=$1`, source.Key).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	t.Logf("attachment=%v management=%v folders=%d locations=%d revision=%d", attachmentErr, managementErr, folders, locations, revision)
	if attachmentErr != nil || managementErr != nil || folders != 1 || locations != 1 || revision != 2 {
		t.Fatalf("attachment and native management must serialize successfully: attachment=%v management=%v folders=%d locations=%d revision=%d", attachmentErr, managementErr, folders, locations, revision)
	}
}

func TestLibraryAttachmentRechecksAfterNativeManagement(t *testing.T) {
	for _, effect := range []string{"unchanged", "configuration", "installation-disabled", "source-disabled"} {
		t.Run(effect, func(t *testing.T) {
			pool, source := attachmentSourceFixture(t)
			actor := attachmentManagementActor(t, pool)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			mutation, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = mutation.Rollback(context.Background()) }()
			if err := resourcetenancy.NewStore(pool).RequireNativeManagementTx(ctx, mutation, actor, source.Key, source.InstallationID, source.OwnerID, true); err != nil {
				t.Fatal(err)
			}
			mutationPID := int(mutation.Conn().PgConn().PID())
			started := make(chan int, 1)
			result := make(chan error, 1)
			name := "attachment-wait-" + uuid.NewString()
			go func() {
				_, err := catalog.NewFolderRepository(pool).Create(ctx, catalog.CreateFolderInput{
					Type: "ebooks", Name: name,
					AttachStorage: func(ctx context.Context, tx pgx.Tx, folderID int) error {
						started <- int(tx.Conn().PgConn().PID())
						return NewLibraryLocations(pool).AttachTx(ctx, tx, source.Key, folderID)
					},
				})
				result <- err
			}()
			var attachmentPID int
			select {
			case attachmentPID = <-started:
			case err := <-result:
				t.Fatalf("attachment did not start: %v", err)
			case <-ctx.Done():
				t.Fatal("attachment did not start")
			}
			query := waitAttachmentLock(t, ctx, pool, attachmentPID, mutationPID, result)
			t.Logf("attachment blocked by native management at: %s", query)
			switch effect {
			case "configuration":
				_, err = mutation.Exec(ctx, `UPDATE bloem_storage_sources SET configuration_revision=configuration_revision+1 WHERE key=$1`, source.Key)
			case "installation-disabled":
				_, err = mutation.Exec(ctx, `UPDATE plugin_installations SET enabled=false WHERE id=$1`, source.InstallationID)
			case "source-disabled":
				_, err = mutation.Exec(ctx, `UPDATE bloem_storage_sources SET enabled=false WHERE key=$1`, source.Key)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := mutation.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-result:
			case <-ctx.Done():
				t.Fatal("attachment did not finish after management commit")
			}
			folders, locations := attachmentRowCounts(t, pool, source, name)
			t.Logf("effect=%s attachment=%v folders=%d locations=%d", effect, err, folders, locations)
			want := 0
			if effect == "unchanged" {
				want = 1
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrLocationSourceUnavailable) {
				t.Fatalf("attachment ignored committed management change: %v", err)
			}
			if folders != want || locations != want {
				t.Fatalf("folders=%d locations=%d, want %d", folders, locations, want)
			}
		})
	}
}
