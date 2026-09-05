package planstore

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userdb"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Each source uses its own pool. Control has one connection, making a leaked
// control transaction observable before and throughout external source work.
func initialExactSource(t *testing.T, f *initialActivationFixture, backend string) (userstore.PlaybackSourceProvider, userstore.PlaybackSinkHandle, func()) {
	t.Helper()
	ctx := t.Context()
	f.binding.Source.Backend = backend
	if _, err := f.pool.Exec(ctx, `UPDATE playback_source_registrations SET backend=$2 WHERE user_id=$1`, f.userID, backend); err != nil {
		t.Fatal(err)
	}
	cfg := f.pool.Config().Copy()
	cfg.MaxConns = 1
	controlPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(controlPool.Close)
	f.store, err = NewPostgresWithGrantPolicy(controlPool, playback.AttemptGrantPolicyV3{MaxDuration: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := f.store.BeginInitialActivation(ctx, f.binding)
	if err != nil || pending.Phase != playback.InitialActivationPendingV3 {
		t.Fatalf("begin: %+v %v", pending, err)
	}
	if controlPool.Stat().AcquiredConns() != 0 {
		t.Fatal("Begin retained a control connection")
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := f.store.ReadInitialActivation(bounded, f.binding); err != nil {
		t.Fatalf("pending was not durable before source open: %v", err)
	}
	var provider userstore.PlaybackSourceProvider
	var seal func()
	ref := f.binding.Source
	if backend == "sqlite" {
		dir := t.TempDir()
		path := filepath.Join(dir, fmt.Sprintf("%d.db", f.userID))
		db, err := userdb.NewUserDB(path, f.userID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.DB.Exec(`INSERT INTO playback_source_markers(user_id,source_id,selection_generation,gate) VALUES(?,?,?,'writable')`, ref.AccountID, ref.SourceID, ref.SelectionGeneration); err != nil {
			t.Fatal(err)
		}
		store := userdb.NewSQLiteUserStore(db.DB)
		if err := store.CreateProfile(ctx, userstore.Profile{ID: f.binding.Scope.ProfileID, Name: "Initial source fixture"}); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		p := userdb.NewSQLiteProvider(userdb.NewUserDBPool(userdb.PoolConfig{DataDir: dir}))
		t.Cleanup(func() { _ = p.Close() })
		provider = p
		seal = func() {
			t.Helper()
			db, err := userdb.NewUserDB(path, f.userID)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			if _, err := db.DB.Exec(`UPDATE playback_source_markers SET gate='sealed' WHERE user_id=?`, f.userID); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		cfg := f.pool.Config().Copy()
		cfg.ConnConfig.RuntimeParams["application_name"] = "initial-source-" + ref.SourceID
		sourcePool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(sourcePool.Close)
		p := pgstore.NewPostgresProvider(sourcePool)
		provider = p
		if _, err := sourcePool.Exec(ctx, `INSERT INTO playback_source_markers(user_id,source_id,selection_generation,gate) VALUES($1,$2,$3,'writable')`, ref.AccountID, ref.SourceID, ref.SelectionGeneration); err != nil {
			t.Fatal(err)
		}
		store, err := p.ForUser(ctx, f.userID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CreateProfile(ctx, userstore.Profile{ID: f.binding.Scope.ProfileID, Name: "Initial source fixture"}); err != nil {
			t.Fatal(err)
		}
		seal = func() {
			t.Helper()
			if _, err := sourcePool.Exec(ctx, `UPDATE playback_source_markers SET gate='sealed' WHERE user_id=$1`, f.userID); err != nil {
				t.Fatal(err)
			}
		}
	}
	handle, err := provider.OpenPlaybackSink(bounded, ref)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return provider, handle, seal
}

func expireInitialSourceOwner(t *testing.T, f *initialActivationFixture) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute' WHERE playback_attempt_id=$1`, f.record.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
}

func TestInitialActivationExactSourceAbortRecovery(t *testing.T) {
	for _, backend := range []string{"postgres", "sqlite"} {
		for _, installed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/installed=%v", backend, installed), func(t *testing.T) {
				f := newInitialActivationFixture(t)
				provider, handle, _ := initialExactSource(t, f, backend)
				ctx := t.Context()
				install := userstore.InstallPlaybackAuthorityRequest{Scope: f.binding.Scope, Next: f.binding.Fence}
				// Discard the successful reply, then recover by exact source read/replay.
				if _, err := handle.InstallPlaybackAuthority(ctx, install); err != nil {
					t.Fatal(err)
				}
				if err := handle.Close(); err != nil {
					t.Fatal(err)
				}
				handle, err := provider.OpenPlaybackSink(ctx, f.binding.Source)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = handle.Close() }()
				_, err = handle.ReadPlaybackProgress(ctx, f.binding.Scope)
				if err != nil {
					t.Fatal(err)
				}
				if installed {
					if _, err := f.store.AcknowledgeInitialInstallation(ctx, f.binding, initialSourceReceipt(t, f, handle)); err != nil {
						t.Fatal(err)
					}
				}
				expireInitialSourceOwner(t, f)
				abortID := uuid.NewString()
				aborting, err := f.store.AbortInitialActivation(ctx, f.binding, abortID)
				if err != nil || aborting.Phase != playback.InitialActivationAbortingV3 {
					t.Fatalf("abort after retention: %+v %v", aborting, err)
				}
				if _, err := f.store.CleanupExpired(ctx, time.Now()); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.ReadInitialActivation(ctx, f.binding); err != nil {
					t.Fatalf("cleanup removed unresolved intent: %v", err)
				}
				retry, err := handle.InstallPlaybackAuthority(ctx, install)
				if err != nil || retry.Outcome != "replayed" {
					t.Fatalf("retry install: %+v %v", retry, err)
				}
				terminal, err := handle.StopPlaybackProgress(ctx, userstore.StopPlaybackProgressRequest{Scope: f.binding.Scope, Fence: f.binding.Fence, StopID: abortID})
				if err != nil || terminal.State.Stop == nil || terminal.HistoryCreated {
					t.Fatalf("no-sample stop: %+v %v", terminal, err)
				}
				first, err := f.store.CompleteInitialAbort(ctx, f.binding, abortID, initialSourceReceipt(t, f, handle))
				if err != nil {
					t.Fatal(err)
				}
				// Lost control completion reply: same receipt remains immutable on retry.
				replay, err := f.store.CompleteInitialAbort(ctx, f.binding, abortID, initialSourceReceipt(t, f, handle))
				if err != nil || !reflect.DeepEqual(first, replay) {
					t.Fatalf("complete replay: %+v %v", replay, err)
				}
				delayed, err := handle.InstallPlaybackAuthority(ctx, install)
				if err != nil || delayed.Outcome != "replayed" || delayed.State.Stop == nil {
					t.Fatalf("delayed install reopened: %+v %v", delayed, err)
				}
				if _, err := f.store.PublishInitialActivation(ctx, f.binding, f.record); err == nil {
					t.Fatal("aborted work activated")
				}
			})
		}
	}
}

func TestInitialActivationUnavailableExactSourcePreservesIntent(t *testing.T) {
	for _, backend := range []string{"postgres", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			f := newInitialActivationFixture(t)
			provider, handle, seal := initialExactSource(t, f, backend)
			if err := handle.Close(); err != nil {
				t.Fatal(err)
			}
			seal()
			if got, err := provider.OpenPlaybackSink(t.Context(), f.binding.Source); err == nil {
				if got != nil {
					_ = got.Close()
				}
				t.Fatal("sealed source opened")
			}
			expireInitialSourceOwner(t, f)
			if _, err := f.store.AbortInitialActivation(t.Context(), f.binding, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.CleanupExpired(t.Context(), time.Now()); err != nil {
				t.Fatal(err)
			}
			got, err := f.store.ReadInitialActivation(t.Context(), f.binding)
			if err != nil || got.Phase != playback.InitialActivationAbortingV3 || got.Terminal != nil {
				t.Fatalf("unavailable source erased unresolved intent: %+v %v", got, err)
			}
		})
	}
}

func TestInitialActivationSourceBlockedInstallDoesNotHoldControl(t *testing.T) {
	f := newInitialActivationFixture(t)
	_, handle, _ := initialExactSource(t, f, "postgres")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	holder, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	if _, err := holder.Exec(ctx, `SELECT user_id FROM playback_source_markers WHERE user_id=$1 FOR UPDATE`, f.userID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := handle.InstallPlaybackAuthority(ctx, userstore.InstallPlaybackAuthorityRequest{Scope: f.binding.Scope, Next: f.binding.Fence})
		result <- err
	}()
	for {
		var blocked bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, "initial-source-"+f.binding.Source.SourceID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
	}
	state, err := f.store.ReadInitialActivation(ctx, f.binding)
	if err != nil || state.Phase != playback.InitialActivationPendingV3 {
		t.Fatalf("source wait retained control transaction: %+v %v", state, err)
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestInitialActivationSourceTerminalReceiptSurvivesLostAcknowledgment(t *testing.T) {
	for _, backend := range []string{"postgres", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			f := newInitialActivationFixture(t)
			_, handle, _ := initialExactSource(t, f, backend)
			ctx := t.Context()
			install := userstore.InstallPlaybackAuthorityRequest{Scope: f.binding.Scope, Next: f.binding.Fence}
			_, err := handle.InstallPlaybackAuthority(ctx, install)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.AcknowledgeInitialInstallation(ctx, f.binding, initialSourceReceipt(t, f, handle)); err != nil {
				t.Fatal(err)
			}
			if _, err := handle.ApplyPlaybackProgress(ctx, userstore.ApplyPlaybackProgressRequest{Scope: f.binding.Scope, Fence: f.binding.Fence, Sample: userstore.PlaybackProgressSample{Sequence: 1, PositionSeconds: 950, DurationSeconds: 1000}}); err != nil {
				t.Fatal(err)
			}
			stop := userstore.StopPlaybackProgressRequest{Scope: f.binding.Scope, Fence: f.binding.Fence, StopID: uuid.NewString()}
			terminal, err := handle.StopPlaybackProgress(ctx, stop)
			if err != nil || !terminal.HistoryCreated {
				t.Fatalf("terminal history: %+v %v", terminal, err)
			}
			expireInitialSourceOwner(t, f)
			abortID := uuid.NewString()
			if _, err := f.store.AbortInitialActivation(ctx, f.binding, abortID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.CompleteInitialAbort(ctx, f.binding, abortID, initialSourceReceipt(t, f, handle)); err != nil {
				t.Fatal(err)
			}
			// Retry after losing both source stop and control completion replies.
			replay, err := handle.StopPlaybackProgress(ctx, stop)
			if err != nil || replay.HistoryCreated || replay.State.Stop.History.ID != terminal.State.Stop.History.ID {
				t.Fatalf("source receipt replay: %+v %v", replay, err)
			}
			acknowledged, err := f.store.CompleteInitialAbort(ctx, f.binding, abortID, initialSourceReceipt(t, f, handle))
			if err != nil || !reflect.DeepEqual(acknowledged.Terminal, &terminal.State) {
				t.Fatalf("control terminal replay: %+v %v", acknowledged, err)
			}
			history, err := handle.(userstore.UserStore).ListHistory(ctx, f.binding.Scope.ProfileID, 10, 0)
			if err != nil || len(history) != 1 {
				t.Fatalf("history duplicated: %+v %v", history, err)
			}
		})
	}
}

func TestInitialActivationSourceActivatedAbortRefused(t *testing.T) {
	for _, backend := range []string{"postgres", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			f := newInitialActivationFixture(t)
			_, handle, _ := initialExactSource(t, f, backend)
			ctx := t.Context()
			_, err := handle.InstallPlaybackAuthority(ctx, userstore.InstallPlaybackAuthorityRequest{Scope: f.binding.Scope, Next: f.binding.Fence})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.AcknowledgeInitialInstallation(ctx, f.binding, initialSourceReceipt(t, f, handle)); err != nil {
				t.Fatal(err)
			}
			published, err := f.store.PublishInitialActivation(ctx, f.binding, f.record)
			if err != nil || published.Phase != playback.InitialActivationActivatedV3 {
				t.Fatalf("publish: %+v %v", published, err)
			}
			expireInitialSourceOwner(t, f)
			if _, err := f.store.AbortInitialActivation(ctx, f.binding, uuid.NewString()); err == nil {
				t.Fatal("initial abort accepted activated source")
			}
		})
	}
}

func initialSourceReceipt(t *testing.T, f *initialActivationFixture, handle userstore.PlaybackSinkHandle) playback.InitialActivationReceiptV3 {
	t.Helper()
	receipt, err := playback.ReadInitialActivationReceiptV3(t.Context(), f.binding, handle)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}
