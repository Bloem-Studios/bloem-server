//go:build integration

package nativestorage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The real authorizer is always called. This barrier observes the existing
// callback's transaction identity, without adding a product hook or grant.
func nativeArtifactPhaseBarrier(t *testing.T, actual plugins.NativeStorageAuthorizeTx) (plugins.NativeStorageAuthorizeTx, <-chan struct{}, chan<- struct{}) {
	t.Helper()
	ready, release := make(chan struct{}), make(chan struct{})
	var preparation pgx.Tx
	reached := false
	return func(ctx context.Context, tx pgx.Tx) error {
		if preparation == nil {
			preparation = tx
		}
		if tx != preparation && !reached {
			reached = true
			close(ready)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return actual(ctx, tx)
	}, ready, release
}
func nativeArtifactAwait(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(20 * time.Second):
		t.Fatal("actual write transaction after rolled-back preparation was not reached")
	}
}
func nativeArtifactUnchanged(t *testing.T, pool *pgxpool.Pool, before string, err error) {
	t.Helper()
	var unknown *catalog.MutationOutcomeUnknown
	if err == nil || errors.As(err, &unknown) {
		t.Fatal("pre-write refusal was acknowledged or reported as mutation uncertainty")
	}
	if nativeDomainLogicalState(t, pool) != before {
		t.Fatal("refused artifact/authority witness changed complete logical registry rows")
	}
}

func TestNativeOnboardingConfigurationPreparationRollbackDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	_, actor := nativeDomainActor(t, pool)
	svc, view, cmd := nativeDomainSourceFixture(t, pool, actor)
	source, err := storagesource.NewRepository(pool).Source(t.Context(), view.SourceKey)
	if err != nil {
		t.Fatal(err)
	}
	actual := svc.sourceMutationAuthorizer(actor, source, source.InstallationID, 1)
	var preparation pgx.Tx
	writeSeen := false
	authorize := func(ctx context.Context, tx pgx.Tx) error {
		if err := actual(ctx, tx); err != nil {
			return err
		}
		if preparation == nil {
			preparation = tx
		}
		if tx == preparation {
			_, err := tx.Exec(ctx, "UPDATE plugin_installations SET update_policy='prep-effect' WHERE id=$1", *source.InstallationID)
			return err
		}
		if !writeSeen {
			var policy string
			if err := tx.QueryRow(ctx, "SELECT update_policy FROM plugin_installations WHERE id=$1", *source.InstallationID).Scan(&policy); err != nil {
				return err
			}
			if policy == "prep-effect" {
				return errors.New("preparation callback effect was committed")
			}
			writeSeen = true
		}
		_, err := tx.Exec(ctx, "UPDATE plugin_installations SET update_policy='write-effect' WHERE id=$1", *source.InstallationID)
		return err
	}
	revision, err := svc.registry.ReplaceConfigurationAuthorized(t.Context(), source.Key, source.OwnerID, 1, cmd.Config, authorize)
	if err != nil || revision != 2 || !writeSeen {
		t.Fatal("preparation must roll back its callback effects and retain genuine write transaction", err)
	}
	var policy string
	if err := pool.QueryRow(t.Context(), "SELECT update_policy FROM plugin_installations WHERE id=$1", *source.InstallationID).Scan(&policy); err != nil || policy != "write-effect" {
		t.Fatal("actual write callback effect did not commit", err)
	}
	// A preparation callback failure is a definite refusal, including its effects.
	current, err := storagesource.NewRepository(pool).Source(t.Context(), source.Key)
	if err != nil {
		t.Fatal(err)
	}
	actual = svc.sourceMutationAuthorizer(actor, current, current.InstallationID, 2)
	before := nativeDomainLogicalState(t, pool)
	denied := errors.New("test preparation refusal")
	_, err = svc.registry.ReplaceConfigurationAuthorized(t.Context(), source.Key, source.OwnerID, 2, cmd.Config, func(ctx context.Context, tx pgx.Tx) error {
		if e := actual(ctx, tx); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, "UPDATE plugin_installations SET update_policy='denied-prep' WHERE id=$1", *source.InstallationID); e != nil {
			return e
		}
		return denied
	})
	if !errors.Is(err, denied) {
		t.Fatal("preparation refusal lost cause")
	}
	nativeArtifactUnchanged(t, pool, before, err)
	canceledCtx, cancel := context.WithCancel(t.Context())
	before = nativeDomainLogicalState(t, pool)
	_, err = svc.registry.ReplaceConfigurationAuthorized(canceledCtx, source.Key, source.OwnerID, 2, cmd.Config, func(ctx context.Context, tx pgx.Tx) error {
		if e := actual(ctx, tx); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, "UPDATE plugin_installations SET update_policy='canceled-prep' WHERE id=$1", *source.InstallationID); e != nil {
			return e
		}
		cancel()
		return ctx.Err()
	})
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("preparation cancellation lost cause")
	}
	nativeArtifactUnchanged(t, pool, before, err)
	t.Log("preparation transaction-local effects rolled back; actual write effects committed (commit hook covered by registry recovery); preparation refusal definite")
}

func TestNativeOnboardingConfigurationArtifactWitnessDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	_, actor := nativeDomainActor(t, pool)
	svc, view, cmd := nativeDomainSourceFixture(t, pool, actor)
	source, err := storagesource.NewRepository(pool).Source(t.Context(), view.SourceKey)
	if err != nil {
		t.Fatal(err)
	}
	var archive, manifest []byte
	var checksum string
	if err := pool.QueryRow(t.Context(), "SELECT archive_bytes,manifest_json,checksum FROM plugin_archives WHERE plugin_installation_id=$1", *source.InstallationID).Scan(&archive, &manifest, &checksum); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, sql, restore string }{
		{"compressed_bytes_only", "UPDATE plugin_archives SET archive_bytes=archive_bytes||decode('00','hex') WHERE plugin_installation_id=$1", "archive"},
		{"manifest", "UPDATE plugin_archives SET manifest_json=convert_to(jsonb_set(convert_from(manifest_json,'UTF8')::jsonb,'{version}','\"changed\"')::text,'UTF8') WHERE plugin_installation_id=$1", "archive"},
		{"checksum", "UPDATE plugin_archives SET checksum=repeat('0',64) WHERE plugin_installation_id=$1", "archive"},
		{"archive_deleted", "DELETE FROM plugin_archives WHERE plugin_installation_id=$1", "archive"},
		{"source_identity", "UPDATE bloem_storage_sources SET root_entry_id=root_entry_id||'-changed' WHERE installation_id=$1", "identity"},
		{"expected_S", "UPDATE bloem_storage_sources SET configuration_revision=configuration_revision+1 WHERE installation_id=$1", "revision"},
		{"actual_login_revoked", "UPDATE auth_sessions SET revoked_at=clock_timestamp() WHERE id=$1", "session"},
	}
	for _, tc := range cases {
		if !t.Run(tc.name, func(t *testing.T) {
			actual := svc.sourceMutationAuthorizer(actor, source, source.InstallationID, 1)
			authorize, ready, release := nativeArtifactPhaseBarrier(t, actual)
			result := nativeArtifactStartWriter(t, func(ctx context.Context) error {
				_, err := svc.registry.ReplaceConfigurationAuthorized(ctx, source.Key, source.OwnerID, 1, cmd.Config, authorize)
				return err
			})
			nativeArtifactAwait(t, ready)
			param := any(*source.InstallationID)
			if tc.restore == "session" {
				param = actor.SessionID
			}
			var interferenceErr error
			if tc.restore == "session" {
				interferenceErr = auth.NewSessionRepository(pool).Revoke(t.Context(), actor.SessionID)
			} else {
				_, interferenceErr = pool.Exec(t.Context(), tc.sql, param)
			}
			if interferenceErr != nil {
				close(release)
				<-result
				nativeArtifactSetupCause(t, interferenceErr)
				t.Fatal("private witness mutation setup failed")
			}
			before := nativeDomainLogicalState(t, pool)
			close(release)
			commandErr := <-result
			nativeArtifactUnchanged(t, pool, before, commandErr)
			if tc.restore == "revision" {
				nativeDomainRequireCode(t, nativeDomainMap(commandErr), "revision_conflict")
			}
			if tc.restore == "session" {
				nativeDomainRequireCode(t, nativeDomainMap(commandErr), "authorization_state_stale")
			}
			var restorationErr error
			switch tc.restore {
			case "archive":
				// Only this verified private clone's controlled archive witness is restored.
				_, restorationErr = pool.Exec(t.Context(), `INSERT INTO plugin_archives(plugin_installation_id,manifest_json,checksum,archive_bytes) VALUES($1,$2,$3,$4) ON CONFLICT(plugin_installation_id) DO UPDATE SET manifest_json=EXCLUDED.manifest_json,checksum=EXCLUDED.checksum,archive_bytes=EXCLUDED.archive_bytes`, *source.InstallationID, manifest, checksum, archive)
			case "identity":
				_, restorationErr = pool.Exec(t.Context(), "UPDATE bloem_storage_sources SET root_entry_id=$2 WHERE key=$1", source.Key, source.RootEntryID)
			case "revision":
				_, restorationErr = pool.Exec(t.Context(), "UPDATE bloem_storage_sources SET configuration_revision=1 WHERE key=$1", source.Key)
			}
			if restorationErr != nil {
				nativeArtifactSetupCause(t, restorationErr)
				t.Fatal("private witness restoration failed")
			}
			t.Log("actual prepared/current witness refusal; full post-interference rows preserved")
		}) {
			t.FailNow()
		}
	}
}

func nativeArtifactArchiveWait(t *testing.T, pool *pgxpool.Pool, pid uint32, expiry time.Time) {
	t.Helper()
	deadline := time.Now().Add(1800 * time.Millisecond)
	for {
		var blocked bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%plugin_archives%' AND query LIKE '%FOR SHARE%')`, int64(pid)).Scan(&blocked); err != nil {
			t.Fatal("archive wait observation failed")
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual final archive FOR SHARE wait not observed")
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
	for {
		var expired bool
		if err := pool.QueryRow(t.Context(), "SELECT clock_timestamp()>$1", expiry).Scan(&expired); err != nil {
			t.Fatal("database clock observation failed")
		}
		if expired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("authority did not expire inside frozen archive lock deadline")
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
	t.Log("actual final archive lock wait observed and database clock passed originating authority expiry")
}
func TestNativeOnboardingConfigurationFinalArchiveExpiryDB(t *testing.T) {
	for _, kind := range []string{"login", "signed_admin"} {
		t.Run(kind, func(t *testing.T) {
			pool := nativeDomainDatabase(t)
			_, actor := nativeDomainActor(t, pool)
			svc, view, cmd := nativeDomainSourceFixture(t, pool, actor)
			source, err := storagesource.NewRepository(pool).Source(t.Context(), view.SourceKey)
			if err != nil {
				t.Fatal(err)
			}
			// This real signed token remains valid through expensive preparation. Its
			// expiry is observed using the database clock while the write waits below.
			if kind == "signed_admin" {
				actor.ExpiresAt = time.Now().Truncate(time.Second).Add(15 * time.Second)
				tokens := auth.NewAdminContextTokenService("native-domain-fixture-signing")
				token, e := tokens.Mint(actor)
				if e != nil {
					t.Fatal(e)
				}
				actor, e = tokens.Parse(token)
				if e != nil {
					t.Fatal(e)
				}
			}
			actual := svc.sourceMutationAuthorizer(actor, source, source.InstallationID, 1)
			authorize, ready, release := nativeArtifactPhaseBarrier(t, actual)
			result := nativeArtifactStartWriter(t, func(ctx context.Context) error {
				_, err := svc.registry.ReplaceConfigurationAuthorized(ctx, source.Key, source.OwnerID, 1, cmd.Config, authorize)
				return err
			})
			nativeArtifactAwait(t, ready)
			// No source/actor lock is retained at this boundary: preparation rolled back.
			blocker, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = blocker.Rollback(context.Background()) })
			var id int64
			if err = blocker.QueryRow(t.Context(), "SELECT plugin_installation_id FROM plugin_archives WHERE plugin_installation_id=$1 FOR UPDATE", *source.InstallationID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			expiry := actor.ExpiresAt
			if kind == "login" {
				if err = pool.QueryRow(t.Context(), "UPDATE auth_sessions SET expires_at=clock_timestamp()+interval '650 milliseconds' WHERE id=$1 RETURNING expires_at", actor.SessionID).Scan(&expiry); err != nil {
					t.Fatal(err)
				}
			} else {
				// Arrange the write to start just before this genuine signed token expires.
				for time.Until(expiry) > 800*time.Millisecond {
					select {
					case <-time.After(10 * time.Millisecond):
					case <-t.Context().Done():
						t.Fatal(t.Context().Err())
					}
				}
			}
			before := nativeDomainLogicalState(t, pool)
			close(release)
			nativeArtifactArchiveWait(t, pool, blocker.Conn().PgConn().PID(), expiry)
			if err = blocker.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			err = <-result
			nativeDomainRequireCode(t, nativeDomainMap(err), "authorization_state_stale")
			nativeArtifactUnchanged(t, pool, before, err)
		})
	}
}

// Only safe structured fields from a private fixture error are diagnostic.
func nativeArtifactSetupCause(t *testing.T, err error) {
	t.Helper()
	var sqlerr *pgconn.PgError
	if errors.As(err, &sqlerr) {
		constraint := sqlerr.ConstraintName
		for _, c := range constraint {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
				constraint = ""
				break
			}
		}
		t.Logf("artifact setup stage=private-witness-interference SQLSTATE=%s constraint=%s", sqlerr.Code, constraint)
	} else {
		t.Logf("artifact setup stage=private-witness-interference cause_type=%T", err)
	}
}

// Cancellation and draining precede the already-registered clone cleanup.
// Background writers return errors; only the foreground test reports failures.
func nativeArtifactStartWriter(t *testing.T, run func(context.Context) error) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() { defer close(finished); result <- run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(15 * time.Second):
			t.Error("owned artifact writer did not drain before clone cleanup")
		}
	})
	return result
}
