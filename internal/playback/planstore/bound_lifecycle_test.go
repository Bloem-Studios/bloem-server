package planstore

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

func activatedLifecycleFixture(t *testing.T) *initialActivationFixture {
	t.Helper()
	f := newInitialActivationFixture(t)
	f.acknowledge(t)
	f.stage(t)
	if _, err := f.store.PublishInitialActivation(t.Context(), f.binding, f.record); err != nil {
		t.Fatal(err)
	}
	return f
}
func TestBoundLifecycleSourceAndAuthorityLookup(t *testing.T) {
	f := activatedLifecycleFixture(t)
	admitted, err := f.store.GetAdmittedPlaybackSource(t.Context(), f.userID)
	if err != nil || admitted.Source != f.binding.Source || admitted.AdmissionID != f.binding.AdmissionID {
		t.Fatalf("admitted %+v: %v", admitted, err)
	}
	got, err := f.store.GetActivatedPlaybackAuthority(t.Context(), f.userID, f.binding.Scope.ProfileID, f.binding.Scope.SessionID)
	if err != nil || got.Binding != f.binding || got.Authority.State != playback.AttemptActiveV3 {
		t.Fatalf("authority %+v: %v", got, err)
	}
	if _, err := f.store.GetActivatedPlaybackAuthority(t.Context(), f.userID, "wrong", f.binding.Scope.SessionID); !errors.Is(err, playback.ErrSessionNotFound) {
		t.Fatalf("wrong profile: %v", err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE playback_source_registrations SET admission_state='blocked' WHERE user_id=$1`, f.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GetAdmittedPlaybackSource(t.Context(), f.userID); !errors.Is(err, playback.ErrInitialActivationUnavailableV3) {
		t.Fatalf("blocked: %v", err)
	}
	if _, err := f.store.GetActivatedPlaybackAuthority(t.Context(), f.userID, f.binding.Scope.ProfileID, f.binding.Scope.SessionID); err == nil {
		t.Fatal("blocked registration authorized progress")
	}
	// Closing a live exact authority remains safe after admission is withdrawn.
	if _, err := f.store.BeginBoundStop(t.Context(), f.binding, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
}
func TestBoundStopGrantBarrierAndExpiredReceiptReplay(t *testing.T) {
	f := activatedLifecycleFixture(t)
	request := f.request
	request.Duration = 300 * time.Millisecond
	grant, err := f.store.IssueAttemptGrant(t.Context(), f.authority, request)
	if err != nil {
		t.Fatal(err)
	}
	stopID := uuid.NewString()
	// Deliberately discard the first committed response.
	if _, err := f.store.BeginBoundStop(t.Context(), f.binding, stopID); err != nil {
		t.Fatal(err)
	}
	stopping, err := f.store.BeginBoundStop(t.Context(), f.binding, stopID)
	if err != nil || stopping.Phase != playback.InitialActivationStoppingV3 || stopping.DrainNotBefore.Before(grant.NotAfter) {
		t.Fatalf("stopping %+v: %v", stopping, err)
	}
	if _, err := f.store.IssueAttemptGrant(t.Context(), f.authority, request); err == nil {
		t.Fatal("grant after stop")
	}
	if _, err := f.store.BeginBoundStop(t.Context(), f.binding, "different"); err == nil {
		t.Fatal("stop ID changed")
	}
	terminal := terminalInitialReceipt(t, f, stopID)
	receipt := f.receipt(t, terminal)
	if _, err := f.store.CompleteBoundStop(t.Context(), f.binding, stopID, receipt); err == nil {
		t.Fatal("completed before grant drain")
	}
	// A failed early completion cannot restore activation or grant issuance.
	observed, err := f.store.ReadInitialActivation(t.Context(), f.binding)
	if err != nil || observed.Phase != playback.InitialActivationStoppingV3 {
		t.Fatalf("failed completion changed state: %+v %v", observed, err)
	}
	waitInitialDatabaseTime(t, f, stopping.DrainNotBefore)
	if _, err := f.pool.Exec(t.Context(), `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 hour',expires_at=control_grant_not_after WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CleanupExpired(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	lookup, err := f.store.GetActivatedPlaybackAuthority(t.Context(), f.userID, f.binding.Scope.ProfileID, f.binding.Scope.SessionID)
	if err != nil || lookup.Authority.State != playback.AttemptDrainingV3 || lookup.Activation.StopID != stopID {
		t.Fatalf("expired stop lookup %+v: %v", lookup, err)
	}
	// Discard terminal completion, then resolve the exact receipt after expiry.
	if _, err := f.store.CompleteBoundStop(t.Context(), f.binding, stopID, receipt); err != nil {
		t.Fatal(err)
	}
	stopped, err := f.store.CompleteBoundStop(t.Context(), f.binding, stopID, receipt)
	if err != nil || stopped.Phase != playback.InitialActivationStoppedV3 || !reflect.DeepEqual(stopped.Terminal, &terminal) {
		t.Fatalf("terminal %+v: %v", stopped, err)
	}
	if _, err := f.store.BeginBoundStop(t.Context(), f.binding, stopID); err != nil {
		t.Fatal(err)
	}
	lookup, err = f.store.GetActivatedPlaybackAuthority(t.Context(), f.userID, f.binding.Scope.ProfileID, f.binding.Scope.SessionID)
	if err != nil || lookup.Authority.State != playback.AttemptStoppedV3 {
		t.Fatalf("stopped lookup %+v: %v", lookup, err)
	}
	wrong := terminal
	wrong.Stop = new(*terminal.Stop)
	wrong.Stop.StopID = "different"
	if _, err := f.store.CompleteBoundStop(t.Context(), f.binding, stopID, f.receipt(t, wrong)); err == nil {
		t.Fatal("different terminal stop accepted")
	}
}
func TestBoundStopCannotStartFromExpiredOrPreparingOwner(t *testing.T) {
	f := newInitialActivationFixture(t)
	f.begin(t)
	if _, err := f.store.BeginBoundStop(t.Context(), f.binding, uuid.NewString()); err == nil {
		t.Fatal("pending became normal stop")
	}
	f.acknowledge(t)
	if _, err := f.store.PublishInitialActivation(t.Context(), f.binding, f.record); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GetActivatedPlaybackAuthority(t.Context(), f.userID, f.binding.Scope.ProfileID, f.binding.Scope.SessionID); err == nil {
		t.Fatal("expired progress authorized")
	}
	if _, err := f.store.BeginBoundStop(t.Context(), f.binding, uuid.NewString()); err == nil {
		t.Fatal("expired owner began stop")
	}
}

func TestBoundStopAndGrantSerializeBothOrders(t *testing.T) {
	for _, stopFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "grant-first", true: "stop-first"}[stopFirst], func(t *testing.T) {
			f := activatedLifecycleFixture(t)
			stopPeer, stopName := authorityPeer(t, f.planstoreFixture)
			issuePeer, issueName := authorityPeer(t, f.planstoreFixture)
			issuePeer.grantMaxDuration = time.Second
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			tx, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			if _, err := tx.Exec(ctx, `SELECT 1 FROM playback_v3_attempts WHERE playback_attempt_id=$1 FOR UPDATE`, f.authority.PlaybackAttemptID); err != nil {
				t.Fatal(err)
			}
			stopDone, issueDone := make(chan error, 1), make(chan error, 1)
			var stopped playback.InitialActivationV3
			var grant playback.AttemptGrantV3
			stop := func() {
				go func() {
					var err error
					stopped, err = stopPeer.BeginBoundStop(ctx, f.binding, uuid.NewString())
					stopDone <- err
				}()
				waitInitialLock(t, f, ctx, stopName)
			}
			issue := func() {
				go func() {
					var err error
					grant, err = issuePeer.IssueAttemptGrant(ctx, f.authority, f.request)
					issueDone <- err
				}()
				waitInitialLock(t, f, ctx, issueName)
			}
			if stopFirst {
				stop()
				issue()
			} else {
				issue()
				stop()
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-stopDone; err != nil {
				t.Fatal(err)
			}
			issueErr := <-issueDone
			if stopFirst && issueErr == nil {
				t.Fatal("later grant escaped stop")
			}
			if !stopFirst && (issueErr != nil || stopped.DrainNotBefore.Before(grant.NotAfter)) {
				t.Fatalf("earlier grant not drained: %+v %v", stopped, issueErr)
			}
		})
	}
}
