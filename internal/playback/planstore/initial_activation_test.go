package planstore

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

type initialActivationFixture struct {
	*grantFixture
	binding playback.InitialActivationBindingV3
	install userstore.PlaybackProgressState
}

func newInitialActivationFixture(t *testing.T) *initialActivationFixture {
	t.Helper()
	f := newGrantFixture(t)
	b := playback.InitialActivationBindingV3{
		Source:   userstore.PlaybackSourceRef{Backend: "postgres", AccountID: f.userID, SourceID: uuid.NewString(), SelectionGeneration: 1},
		Scope:    userstore.PlaybackProgressScope{ProfileID: f.record.ProfileID, SessionID: f.record.SessionID, MediaItemID: "activation-item"},
		Fence:    userstore.PlaybackProgressFence{AttemptID: f.authority.PlaybackAttemptID, Incarnation: f.authority.Incarnation, OwnerID: f.authority.OwnerID, Epoch: f.authority.Epoch},
		IntentID: uuid.NewString(), AdmissionID: uuid.NewString(),
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO playback_source_registrations(user_id,backend,source_id,selection_generation,admission_id,admission_state) VALUES($1,$2,$3,$4,$5,'admitting')`, f.userID, b.Source.Backend, b.Source.SourceID, b.Source.SelectionGeneration, b.AdmissionID); err != nil {
		t.Fatal(err)
	}
	return &initialActivationFixture{grantFixture: f, binding: b, install: userstore.PlaybackProgressState{Version: 1, Scope: b.Scope, Fence: b.Fence}}
}

func (f *initialActivationFixture) begin(t *testing.T) {
	t.Helper()
	if _, err := f.store.BeginInitialActivation(t.Context(), f.binding); err != nil {
		t.Fatal(err)
	}
}
func (f *initialActivationFixture) acknowledge(t *testing.T) {
	t.Helper()
	f.begin(t)
	if _, err := f.store.AcknowledgeInitialInstallation(t.Context(), f.binding, f.receipt(t, f.install)); err != nil {
		t.Fatal(err)
	}
}

func TestInitialActivationConcurrentBeginAndExactIntent(t *testing.T) {
	f := newInitialActivationFixture(t)
	peer, _ := authorityPeer(t, f.planstoreFixture)
	done := make(chan error, 8)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 8 {
		store := f.store
		if i%2 != 0 {
			store = peer
		}
		wg.Go(func() { <-gate; _, err := store.BeginInitialActivation(t.Context(), f.binding); done <- err })
	}
	close(gate)
	wg.Wait()
	close(done)
	for err := range done {
		if err != nil {
			t.Fatal(err)
		}
	}
	state, err := f.store.ReadInitialActivation(t.Context(), f.binding)
	if err != nil || !reflect.DeepEqual(state.Binding, f.binding) {
		t.Fatalf("begin receipt: %+v %v", state, err)
	}
	changed := f.binding
	changed.IntentID = uuid.NewString()
	if _, err := f.store.BeginInitialActivation(t.Context(), changed); err == nil {
		t.Fatal("changed intent replaced pending activation")
	}
	changed = f.binding
	changed.AdmissionID = uuid.NewString()
	if _, err := f.store.ReadInitialActivation(t.Context(), changed); err == nil {
		t.Fatal("changed admission read accepted")
	}
}

func TestInitialActivationRegistrationAdmission(t *testing.T) {
	for _, variation := range []string{"missing", "default blocked", "retiring", "source", "generation", "admission"} {
		t.Run(variation, func(t *testing.T) {
			f := newInitialActivationFixture(t)
			query := ""
			args := []any{f.userID}
			switch variation {
			case "missing":
				query = "DELETE FROM playback_source_registrations WHERE user_id=$1"
			case "default blocked":
				if _, err := f.pool.Exec(t.Context(), "DELETE FROM playback_source_registrations WHERE user_id=$1", f.userID); err != nil {
					t.Fatal(err)
				}
				query = "INSERT INTO playback_source_registrations(user_id,backend,source_id,selection_generation,admission_id) VALUES($1,'postgres',$2,1,$3)"
				args = append(args, f.binding.Source.SourceID, f.binding.AdmissionID)
			case "retiring":
				query = "UPDATE playback_source_registrations SET admission_state='retiring' WHERE user_id=$1"
			case "source":
				query = "UPDATE playback_source_registrations SET source_id=$2 WHERE user_id=$1"
				args = append(args, uuid.NewString())
			case "generation":
				query = "UPDATE playback_source_registrations SET selection_generation=2 WHERE user_id=$1"
			case "admission":
				query = "UPDATE playback_source_registrations SET admission_id=$2 WHERE user_id=$1"
				args = append(args, uuid.NewString())
			}
			if _, err := f.pool.Exec(t.Context(), query, args...); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.BeginInitialActivation(t.Context(), f.binding); err == nil {
				t.Fatal("registration failed to block begin")
			}
		})
	}
}

func TestInitialActivationAcknowledgmentAndPublicationRecheckAdmission(t *testing.T) {
	for _, operation := range []string{"ack", "publish", "publish replay"} {
		for _, variation := range []string{"same source blocked", "changed token", "expired owner"} {
			t.Run(operation+"/"+variation, func(t *testing.T) {
				f := newInitialActivationFixture(t)
				f.begin(t)
				if operation != "ack" {
					f.acknowledge(t)
				}
				if operation == "publish replay" {
					if _, err := f.store.PublishInitialActivation(t.Context(), f.binding, f.record); err != nil {
						t.Fatal(err)
					}
				}
				switch variation {
				case "same source blocked":
					if _, err := f.pool.Exec(t.Context(), "UPDATE playback_source_registrations SET admission_state='blocked' WHERE user_id=$1", f.userID); err != nil {
						t.Fatal(err)
					}
				case "changed token":
					if _, err := f.pool.Exec(t.Context(), "UPDATE playback_source_registrations SET admission_id=$2 WHERE user_id=$1", f.userID, uuid.NewString()); err != nil {
						t.Fatal(err)
					}
				case "expired owner":
					expireAuthorityLease(t, f.planstoreFixture, f.authority.PlaybackAttemptID)
				}
				var err error
				if operation == "ack" {
					_, err = f.store.AcknowledgeInitialInstallation(t.Context(), f.binding, f.receipt(t, f.install))
				} else {
					_, err = f.store.PublishInitialActivation(t.Context(), f.binding, f.record)
				}
				if err == nil {
					t.Fatal("stale admission/owner accepted")
				}
				if _, err := f.store.ReadInitialActivation(t.Context(), f.binding); err != nil && variation == "expired owner" {
					t.Fatalf("expired read failed: %v", err)
				}
			})
		}
	}
}

func waitInitialLock(t *testing.T, f *initialActivationFixture, ctx context.Context, name string) {
	t.Helper()
	for {
		var blocked bool
		if err := f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')", name).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
	}
}

func TestInitialActivationGateWinsPublicationLockRace(t *testing.T) {
	f := newInitialActivationFixture(t)
	f.acknowledge(t)
	peer, name := authorityPeer(t, f.planstoreFixture)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	if _, err := tx.Exec(ctx, "UPDATE playback_source_registrations SET admission_state='retiring' WHERE user_id=$1", f.userID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := peer.PublishInitialActivation(ctx, f.binding, f.record); done <- err }()
	waitInitialLock(t, f, ctx, name)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("publication passed committed gate")
	}
}

func terminalInitialReceipt(t *testing.T, f *initialActivationFixture, stopID string) userstore.PlaybackProgressState {
	t.Helper()
	change, err := userstore.PreparePlaybackStop(&f.install, userstore.StopPlaybackProgressRequest{Scope: f.binding.Scope, Fence: f.binding.Fence, StopID: stopID})
	if err != nil {
		t.Fatal(err)
	}
	return change.Result.State
}

func TestInitialActivationExpiredAbortRetainsGrantBoundAndReceipt(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "installed"}[installed], func(t *testing.T) {
			f := newInitialActivationFixture(t)
			f.begin(t)
			request := f.request
			request.Duration = 300 * time.Millisecond
			var grant playback.AttemptGrantV3
			if installed {
				f.acknowledge(t)
				f.stage(t)
				var err error
				grant, err = f.store.IssueAttemptGrant(t.Context(), f.authority, request)
				if err != nil {
					t.Fatal(err)
				}
				waitInitialDatabaseTime(t, f, grant.NotAfter)
			}
			if _, err := f.pool.Exec(t.Context(), `UPDATE playback_v3_attempts SET expires_at=COALESCE(control_grant_not_after,clock_timestamp()-interval '1 hour'),control_lease_expires_at=clock_timestamp()-interval '1 hour' WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID); err != nil {
				t.Fatal(err)
			}
			abortID := uuid.NewString()
			aborted, err := f.store.AbortInitialActivation(t.Context(), f.binding, abortID)
			if err != nil || aborted.Phase != playback.InitialActivationAbortingV3 || aborted.DrainNotBefore.Before(grant.NotAfter) {
				t.Fatalf("expired abort: %+v %v", aborted, err)
			}
			replay, err := f.store.AbortInitialActivation(t.Context(), f.binding, abortID)
			if err != nil || !replay.DrainNotBefore.Equal(aborted.DrainNotBefore) {
				t.Fatalf("abort replay changed drain: %+v %v", replay, err)
			}
			if _, err := f.store.AbortInitialActivation(t.Context(), f.binding, uuid.NewString()); err == nil {
				t.Fatal("changed abort ID accepted")
			}
			if _, err := f.store.RenewAttempt(t.Context(), f.authority, time.Minute); err == nil {
				t.Fatal("aborting owner renewed")
			}
			if _, err := f.store.IssueAttemptGrant(t.Context(), f.authority, request); err == nil {
				t.Fatal("reconciler received execution grant")
			}
			changedBinding := f.binding
			changedBinding.IntentID = uuid.NewString()
			if _, err := f.store.AbortInitialActivation(t.Context(), changedBinding, abortID); err == nil {
				t.Fatal("changed intent aborted")
			}
			terminal := terminalInitialReceipt(t, f, "preexisting-stop")
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			for {
				var elapsed bool
				if err := f.pool.QueryRow(ctx, "SELECT clock_timestamp()>=$1", aborted.DrainNotBefore).Scan(&elapsed); err != nil {
					t.Fatal(err)
				}
				if elapsed {
					break
				}
			}
			if _, err := f.store.CompleteInitialAbort(ctx, f.binding, uuid.NewString(), f.receipt(t, terminal)); err == nil {
				t.Fatal("changed abort ID completed")
			}
			final, err := f.store.CompleteInitialAbort(ctx, f.binding, abortID, f.receipt(t, terminal))
			if err != nil || final.Phase != playback.InitialActivationAbortedV3 || !reflect.DeepEqual(final.Terminal, &terminal) {
				t.Fatalf("terminal completion: %+v %v", final, err)
			}
			finalReplay, err := f.store.CompleteInitialAbort(ctx, f.binding, abortID, f.receipt(t, terminal))
			if err != nil || !reflect.DeepEqual(finalReplay.Terminal, final.Terminal) {
				t.Fatalf("terminal replay: %+v %v", finalReplay, err)
			}
			changed := terminalInitialReceipt(t, f, "different-stop")
			if _, err := f.store.CompleteInitialAbort(ctx, f.binding, abortID, f.receipt(t, changed)); err == nil {
				t.Fatal("changed terminal receipt accepted")
			}
		})
	}
}

func TestInitialActivationLegacyCannotBypassBoundPhases(t *testing.T) {
	for _, phase := range []string{"pending", "installed", "aborting", "activated"} {
		t.Run(phase, func(t *testing.T) {
			f := newInitialActivationFixture(t)
			f.begin(t)
			if phase == "installed" || phase == "activated" {
				f.acknowledge(t)
			}
			if phase == "activated" {
				if _, err := f.store.PublishInitialActivation(t.Context(), f.binding, f.record); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "aborting" {
				if _, err := f.pool.Exec(t.Context(), "UPDATE playback_source_registrations SET admission_state='retiring' WHERE user_id=$1", f.userID); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.AbortInitialActivation(t.Context(), f.binding, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.store.PublishAttempt(t.Context(), f.authority, f.record); err == nil {
				t.Fatal("legacy publication bypass")
			}
			if err := f.store.StopAttempt(t.Context(), f.authority); err == nil {
				t.Fatal("legacy stop bypass")
			}
			if err := f.store.CompleteAttemptDrain(t.Context(), f.authority); err == nil {
				t.Fatal("legacy complete drain bypass")
			}
			if phase == "activated" {
				if _, err := f.store.AbortInitialActivation(t.Context(), f.binding, uuid.NewString()); err == nil {
					t.Fatal("activated attempt used initial abort")
				}
			}
			if _, err := f.pool.Exec(t.Context(), `UPDATE playback_v3_attempts SET expires_at=clock_timestamp()-interval '1 hour',control_lease_expires_at=clock_timestamp()-interval '1 hour' WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID); err != nil {
				t.Fatal(err)
			}
			req := f.reservation
			req.OwnerID = uuid.NewString()
			result, err := f.store.ReserveAttempt(t.Context(), req)
			if err == nil && result.Owned {
				t.Fatal("expired bound attempt reclaimed")
			}
			if err := f.store.SaveAttempt(t.Context(), f.record); err == nil {
				t.Fatal("legacy save replaced bound attempt")
			}
			if _, err := f.store.CleanupExpired(t.Context(), time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			state, err := f.store.ReadInitialActivation(t.Context(), f.binding)
			if err != nil || string(state.Phase) != phase {
				t.Fatalf("legacy paths lost bound state: %+v %v", state, err)
			}
		})
	}
}

func TestInitialActivationInstallReceiptAndAbortEligibility(t *testing.T) {
	f := newInitialActivationFixture(t)
	f.begin(t)
	if _, err := f.store.AbortInitialActivation(t.Context(), f.binding, uuid.NewString()); err == nil {
		t.Fatal("live admitted owner aborted without cancellation")
	}
	invalid := f.install
	invalid.Last = &userstore.PlaybackProgressReceipt{}
	if _, err := f.store.AcknowledgeInitialInstallation(t.Context(), f.binding, f.receipt(t, invalid)); err == nil {
		t.Fatal("invalid installation accepted")
	}
	f.acknowledge(t)
	invalid = f.install
	invalid.Last = &userstore.PlaybackProgressReceipt{}
	if _, err := f.store.AcknowledgeInitialInstallation(t.Context(), f.binding, f.receipt(t, invalid)); err == nil {
		t.Fatal("changed installation accepted")
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM playback_source_registrations WHERE user_id=$1", f.userID); err != nil {
		t.Fatal(err)
	}
	checks := []func() error{
		func() error { _, err := f.store.ReadInitialActivation(t.Context(), f.binding); return err },
		func() error {
			_, err := f.store.AcknowledgeInitialInstallation(t.Context(), f.binding, f.receipt(t, f.install))
			return err
		},
		func() error { _, err := f.store.PublishInitialActivation(t.Context(), f.binding, f.record); return err },
		func() error {
			_, err := f.store.AbortInitialActivation(t.Context(), f.binding, uuid.NewString())
			return err
		},
	}
	for _, check := range checks {
		if err := check(); err == nil {
			t.Fatal("missing registration accepted")
		}
	}
}

func TestInitialActivationPublicationHoldsRegistrationBeforeAttempt(t *testing.T) {
	f := newInitialActivationFixture(t)
	f.acknowledge(t)
	peer, name := authorityPeer(t, f.planstoreFixture)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	attemptLock, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer attemptLock.Rollback(context.Background()) //nolint:errcheck
	if _, err := attemptLock.Exec(ctx, "SELECT 1 FROM playback_v3_attempts WHERE playback_attempt_id=$1 FOR UPDATE", f.authority.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := peer.PublishInitialActivation(ctx, f.binding, f.record); done <- err }()
	waitInitialLock(t, f, ctx, name)
	gate, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = gate.Exec(ctx, "SELECT 1 FROM playback_source_registrations WHERE user_id=$1 FOR UPDATE NOWAIT", f.userID)
	if pgerr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgerr.Code != "55P03" {
		t.Fatalf("publication did not lock registration before attempt: %v", err)
	}
	if err := gate.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := attemptLock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE playback_source_registrations SET admission_state='retiring' WHERE user_id=$1", f.userID); err != nil {
		t.Fatal(err)
	}
	state, err := f.store.ReadInitialActivation(ctx, f.binding)
	if err != nil || state.Phase != playback.InitialActivationActivatedV3 {
		t.Fatalf("winning publication lost state: %+v %v", state, err)
	}
	if _, err := f.store.PublishInitialActivation(ctx, f.binding, f.record); err == nil {
		t.Fatal("publication replay bypassed later gate")
	}
}

func waitInitialDatabaseTime(t *testing.T, f *initialActivationFixture, deadline time.Time) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for {
		var elapsed bool
		if err := f.pool.QueryRow(ctx, "SELECT clock_timestamp()>=$1", deadline).Scan(&elapsed); err != nil {
			t.Fatal(err)
		}
		if elapsed {
			return
		}
	}
}

func TestInitialActivationAbortWaitsForIssuedGrant(t *testing.T) {
	f := newInitialActivationFixture(t)
	f.acknowledge(t)
	f.stage(t)
	request := f.request
	request.Duration = time.Second
	grant, err := f.store.IssueAttemptGrant(t.Context(), f.authority, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE playback_source_registrations SET admission_state='retiring' WHERE user_id=$1", f.userID); err != nil {
		t.Fatal(err)
	}
	abortID := uuid.NewString()
	state, err := f.store.AbortInitialActivation(t.Context(), f.binding, abortID)
	if err != nil || state.DrainNotBefore.Before(grant.NotAfter) {
		t.Fatalf("grant drain bound: %+v %v", state, err)
	}
	terminal := terminalInitialReceipt(t, f, abortID)
	if _, err := f.store.CompleteInitialAbort(t.Context(), f.binding, abortID, f.receipt(t, terminal)); err == nil {
		t.Fatal("terminal completion preceded grant deadline")
	}
	waitInitialDatabaseTime(t, f, state.DrainNotBefore)
	if _, err := f.store.CompleteInitialAbort(t.Context(), f.binding, abortID, f.receipt(t, terminal)); err != nil {
		t.Fatal(err)
	}
}

// A deliberately shaped handle isolates control transition tests from source storage.
// Real PostgreSQL/SQLite receipt integration lives in initial_activation_source_test.go.
type initialReceiptTestHandle struct {
	userstore.PlaybackSinkHandle
	ref   userstore.PlaybackSourceRef
	state userstore.PlaybackProgressState
}

func (h initialReceiptTestHandle) Source() userstore.PlaybackSourceRef { return h.ref }
func (h initialReceiptTestHandle) ReadPlaybackProgress(context.Context, userstore.PlaybackProgressScope) (userstore.PlaybackProgressState, error) {
	return h.state, nil
}
func (f *initialActivationFixture) receipt(t *testing.T, state userstore.PlaybackProgressState) playback.InitialActivationReceiptV3 {
	t.Helper()
	receipt, err := playback.ReadInitialActivationReceiptV3(t.Context(), f.binding, initialReceiptTestHandle{ref: f.binding.Source, state: state})
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestInitialActivationReceiptRejectsAnotherSourceAndZero(t *testing.T) {
	f := newInitialActivationFixture(t)
	f.begin(t)
	wrong := f.binding.Source
	wrong.SourceID = uuid.NewString()
	if _, err := playback.ReadInitialActivationReceiptV3(t.Context(), f.binding, initialReceiptTestHandle{ref: wrong, state: f.install}); err == nil {
		t.Fatal("matching receipt from wrong source accepted")
	}
	if _, err := f.store.AcknowledgeInitialInstallation(t.Context(), f.binding, playback.InitialActivationReceiptV3{}); err == nil {
		t.Fatal("zero receipt acknowledged")
	}
}

func TestInitialActivationPublicationRechecksLeaseAfterLockWait(t *testing.T) {
	f := newInitialActivationFixture(t)
	f.acknowledge(t)
	peer, name := authorityPeer(t, f.planstoreFixture)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	if _, err := tx.Exec(ctx, "SELECT 1 FROM playback_source_registrations WHERE user_id=$1 FOR UPDATE", f.userID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := peer.PublishInitialActivation(ctx, f.binding, f.record); done <- err }()
	waitInitialLock(t, f, ctx, name)
	if _, err := tx.Exec(ctx, "UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1", f.authority.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("publication used pre-lock live lease")
	}
}

func TestInitialActivationExecutionRequiresInstalledReceipt(t *testing.T) {
	for _, prestaged := range []bool{false, true} {
		t.Run(map[bool]string{false: "unstaged", true: "prestaged"}[prestaged], func(t *testing.T) {
			f := newInitialActivationFixture(t)
			if prestaged {
				f.stage(t)
			}
			f.begin(t)
			if err := f.store.StageAttemptRoute(t.Context(), f.authority, f.record, f.route); err == nil {
				t.Fatal("pending source staged execution")
			}
			if _, err := f.store.IssueAttemptGrant(t.Context(), f.authority, f.request); err == nil {
				t.Fatal("pending source issued execution grant")
			}
			f.acknowledge(t)
			f.stage(t)
			if _, err := f.store.IssueAttemptGrant(t.Context(), f.authority, f.request); err != nil {
				t.Fatalf("installed execution rejected: %v", err)
			}
			serve := f.request
			serve.Purpose = playback.AttemptGrantServeV3
			serve.NodeID = f.route.EgressNodeID
			if _, err := f.store.IssueAttemptGrant(t.Context(), f.authority, serve); err == nil {
				t.Fatal("installed unpublished source served")
			}
			if _, err := f.store.PublishInitialActivation(t.Context(), f.binding, f.record); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.IssueAttemptGrant(t.Context(), f.authority, serve); err != nil {
				t.Fatalf("activated serve rejected: %v", err)
			}
		})
	}
}

func TestInitialActivationRejectsPreviouslyIssuedUnboundGrant(t *testing.T) {
	f := newInitialActivationFixture(t)
	f.stage(t)
	request := f.request
	request.Duration = 100 * time.Millisecond
	grant, err := f.store.IssueAttemptGrant(t.Context(), f.authority, request)
	if err != nil {
		t.Fatalf("legacy unbound execution changed: %v", err)
	}
	if _, err := f.store.BeginInitialActivation(t.Context(), f.binding); err == nil {
		t.Fatal("live unbound grant adopted")
	}
	waitInitialDatabaseTime(t, f, grant.NotAfter)
	if _, err := f.store.BeginInitialActivation(t.Context(), f.binding); err == nil {
		t.Fatal("elapsed unbound grant adopted")
	}
	var activation []byte
	if err := f.pool.QueryRow(t.Context(), "SELECT control_activation FROM playback_v3_attempts WHERE playback_attempt_id=$1", f.authority.PlaybackAttemptID).Scan(&activation); err != nil || len(activation) != 0 {
		t.Fatalf("rejected begin persisted intent: %s %v", activation, err)
	}
}

func TestInitialActivationBeginAndUnboundIssueSerialize(t *testing.T) {
	for _, beginFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "issue queued first", true: "begin queued first"}[beginFirst], func(t *testing.T) {
			f := newInitialActivationFixture(t)
			f.stage(t)
			beginPeer, beginName := authorityPeer(t, f.planstoreFixture)
			issuePeer, issueName := authorityPeer(t, f.planstoreFixture)
			issuePeer.grantMaxDuration = time.Second
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			tx, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background()) //nolint:errcheck
			if _, err := tx.Exec(ctx, "SELECT 1 FROM playback_v3_attempts WHERE playback_attempt_id=$1 FOR UPDATE", f.authority.PlaybackAttemptID); err != nil {
				t.Fatal(err)
			}
			beginDone, issueDone := make(chan error, 1), make(chan error, 1)
			begin := func() {
				go func() { _, err := beginPeer.BeginInitialActivation(ctx, f.binding); beginDone <- err }()
				waitInitialLock(t, f, ctx, beginName)
			}
			issue := func() {
				go func() { _, err := issuePeer.IssueAttemptGrant(ctx, f.authority, f.request); issueDone <- err }()
				waitInitialLock(t, f, ctx, issueName)
			}
			if beginFirst {
				begin()
				issue()
			} else {
				issue()
				begin()
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			beginErr, issueErr := <-beginDone, <-issueDone
			if (beginErr == nil) == (issueErr == nil) {
				t.Fatalf("begin and issue must have one winner: begin=%v issue=%v", beginErr, issueErr)
			}
		})
	}
}
