package planstore

import (
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

func TestInitialOwnerCancellationIsDurable(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "installed"}[installed], func(t *testing.T) {
			f := newInitialActivationFixture(t)
			if installed {
				f.acknowledge(t)
			} else {
				f.begin(t)
			}
			abortID := uuid.NewString()
			if _, err := f.store.AbortInitialActivation(t.Context(), f.binding, abortID); err == nil {
				t.Fatal("live owner was abandoned without cancellation")
			}
			if _, err := f.store.CancelInitialActivation(t.Context(), f.binding, abortID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(t.Context(), `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID); err != nil {
				t.Fatal(err)
			}
			canceled, err := f.store.CancelInitialActivation(t.Context(), f.binding, abortID)
			if err != nil || canceled.Phase != playback.InitialActivationAbortingV3 {
				t.Fatalf("expired replay %+v: %v", canceled, err)
			}
			if _, err := f.store.CancelInitialActivation(t.Context(), f.binding, uuid.NewString()); err == nil {
				t.Fatal("changed cancellation ID accepted")
			}
			if _, err := f.store.RenewAttempt(t.Context(), f.authority, time.Minute); err == nil {
				t.Fatal("canceled owner renewed")
			}
			terminal := terminalInitialReceipt(t, f, abortID)
			if _, err := f.store.CompleteInitialAbort(t.Context(), f.binding, abortID, f.receipt(t, terminal)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInitialFrozenProgressBindingCannotChange(t *testing.T) {
	f := newInitialActivationFixture(t)
	f.binding.Progress.DurationSeconds = 180
	f.binding.Progress.Thresholds.WatchedPct = 85
	f.binding.Progress.Hints.FileID = 123
	f.binding.HistoryIdentityJSON = `{"stable_type":"movie","provider_ids":{"tmdb":"42"}}`
	f.begin(t)
	frozen, err := f.store.ReadInitialActivation(t.Context(), f.binding)
	if err != nil || frozen.Binding.Progress != f.binding.Progress || frozen.Binding.HistoryIdentityJSON != f.binding.HistoryIdentityJSON {
		t.Fatalf("frozen %+v: %v", frozen, err)
	}
	changed := f.binding
	changed.Progress.Thresholds.WatchedPct = 90
	if _, err := f.store.BeginInitialActivation(t.Context(), changed); err == nil {
		t.Fatal("mutable settings replaced frozen policy")
	}
	changed = f.binding
	changed.HistoryIdentityJSON = `{"stable_type":"episode"}`
	if _, err := f.store.ReadInitialActivation(t.Context(), changed); err == nil {
		t.Fatal("changed identity read exact binding")
	}
}
