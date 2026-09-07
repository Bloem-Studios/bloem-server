package planstore

import (
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

func TestReservationCapturesAdmissionAndRefusesRefresh(t *testing.T) {
	f := newPlanstoreFixture(t)
	store := NewPostgres(f.pool)
	request := authorityRequest(f)
	admission, source := uuid.NewString(), uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO playback_source_registrations(user_id,backend,source_id,selection_generation,admission_id,admission_state) VALUES($1,'postgres',$2,1,$3,'admitting')`, f.userID, source, admission); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveAttempt(t.Context(), request); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("unbound reservation after registration: %v", err)
	}
	request.ExpectedAdmissionID = admission
	first, err := store.ReserveAttempt(t.Context(), request)
	if err != nil || !first.Owned {
		t.Fatalf("captured reservation: %+v %v", first, err)
	}
	replay, err := store.ReserveAttempt(t.Context(), request)
	if err != nil || replay.Owned {
		t.Fatalf("replay allocated: %+v %v", replay, err)
	}
	replacement := uuid.NewString()
	// Simulate an independently changed registration: retry may not refresh the
	// original admission identity even though the current registration matches it.
	if _, err := f.pool.Exec(t.Context(), `UPDATE playback_source_registrations SET admission_id=$2 WHERE user_id=$1`, f.userID, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveAttempt(t.Context(), request); !errors.Is(err, userstore.ErrPlaybackSourceUnavailable) {
		t.Fatalf("stale admission accepted: %v", err)
	}
	request.ExpectedAdmissionID = replacement
	if _, err := store.ReserveAttempt(t.Context(), request); !errors.Is(err, playback.ErrIdempotencyKeyReusedV3) {
		t.Fatalf("refreshed retry: %v", err)
	}
}
