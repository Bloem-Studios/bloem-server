package planstore

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"

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

func TestFirstAdmissionSerializesLegacyAttemptPublication(t *testing.T) {
	f := newPlanstoreFixture(t)
	provider := pgstore.NewPostgresProvider(f.pool)
	installation := "ab860a0a-7da8-408d-a8de-5eb0fcd482d2"
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO server_settings(key,value) VALUES('diagnostics.server_instance_id',$1),('userdb.backend','postgres') ON CONFLICT(key) DO UPDATE SET value=excluded.value`, installation); err != nil {
		t.Fatal(err)
	}
	var username string
	if err := f.pool.QueryRow(t.Context(), `SELECT username FROM users WHERE id=$1`, f.userID).Scan(&username); err != nil {
		t.Fatal(err)
	}
	intent := pgstore.FirstAdmissionIntent{AccountID: f.userID, ExpectedUsername: username, InstallationID: installation, Backend: "postgres", SourceID: uuid.NewString(), IntentID: uuid.NewString()}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	launchCtx, release, err := provider.AcquireLegacyPlaybackAdmission(ctx, f.userID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	admissions := make(chan error, 1)
	go func() { _, err := provider.FirstAdmission(ctx, intent, true); admissions <- err }()
	for {
		var waiting bool
		err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%pg_advisory_xact_lock(hashtextextended(''playback-source:%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		runtime.Gosched()
	}
	// Save joins the launch lease despite the exclusive waiter. First admission
	// must then observe this exact retained attempt and refuse, not bless it.
	record := f.attemptRecord(uuid.NewString(), uuid.NewString(), "legacy")
	store := NewPostgres(f.pool)
	if err := store.SaveAttempt(launchCtx, record); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-admissions; err == nil {
		t.Fatal("admission ignored the published legacy attempt")
	}
	if _, err := f.pool.Exec(ctx, `DELETE FROM playback_v3_attempts WHERE user_id=$1`, f.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.FirstAdmission(ctx, intent, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAttempt(ctx, f.attemptRecord(uuid.NewString(), uuid.NewString(), "after")); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("legacy Save after admission: %v", err)
	}
	if _, err := store.ReserveAttempt(ctx, authorityRequest(f)); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("unbound reservation after admission: %v", err)
	}
}

func TestAdmittedAccountCanRetainNonExecutableTerminalDecision(t *testing.T) {
	f := newPlanstoreFixture(t)
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO playback_source_registrations(user_id,backend,source_id,selection_generation,admission_id,admission_state) VALUES($1,'postgres',$2,1,$3,'admitting')`, f.userID, uuid.NewString(), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	record := f.attemptRecord("", uuid.NewString(), "terminal")
	record.StartResponse = playback.NewTerminalResponseV3("unsupported", "This adaptation is unavailable.", false)
	record.CurrentPlanID = ""
	record.CurrentPlan = playback.PlanV3{}
	record.FrozenRecipe = playback.ExecutableRecipeV3{}
	if err := NewPostgres(f.pool).SaveAttempt(t.Context(), record); err != nil {
		t.Fatalf("non-executable refusal rejected: %v", err)
	}
}
