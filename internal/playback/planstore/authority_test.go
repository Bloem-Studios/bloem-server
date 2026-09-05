package planstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func authorityRequest(f *planstoreFixture) playback.AttemptReservationRequestV3 {
	id := "authority-" + uuid.NewString()
	return playback.AttemptReservationRequestV3{PlaybackAttemptID: id, UserID: f.userID, ProfileID: "profile-1", RequestedMediaFileID: f.mediaFileID, RequestDigest: "digest", NormalizedRequest: f.attemptRecord("", id, "digest").NormalizedRequest, OwnerID: uuid.NewString(), LeaseDuration: time.Minute, Retention: time.Hour}
}

func authorityPeer(t *testing.T, f *planstoreFixture) (*Postgres, string) {
	t.Helper()
	cfg := f.pool.Config().Copy()
	name := "authority-peer-" + uuid.NewString()
	cfg.ConnConfig.RuntimeParams["application_name"] = name
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return NewPostgres(pool), name
}

func expireAuthorityLease(t *testing.T, f *planstoreFixture, id string) {
	t.Helper()
	tag, err := f.pool.Exec(t.Context(), `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 minute' WHERE playback_attempt_id=$1`, id)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("expire lease: %v, rows=%d", err, tag.RowsAffected())
	}
}

func TestAttemptAuthorityReservation(t *testing.T) {
	f := newPlanstoreFixture(t)
	a := NewPostgres(f.pool)
	b, _ := authorityPeer(t, f)
	req := authorityRequest(f)
	type outcome struct {
		reservation playback.AttemptReservationV3
		err         error
	}
	results := make(chan outcome, 2)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i, store := range []*Postgres{a, b} {
		wg.Go(func() {
			<-gate
			request := req
			if i == 1 {
				request.OwnerID = uuid.NewString()
			}
			r, err := store.ReserveAttempt(t.Context(), request)
			results <- outcome{r, err}
		})
	}
	close(gate)
	wg.Wait()
	close(results)
	owned := 0
	var authority playback.AttemptAuthorityV3
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.reservation.Owned {
			owned++
			authority = result.reservation.Authority
		}
	}
	if owned != 1 || authority.State != playback.AttemptPreparingV3 || authority.Epoch < 1 {
		t.Fatalf("owners=%d authority=%+v", owned, authority)
	}
	replay, err := b.ReserveAttempt(t.Context(), req)
	if err != nil || replay.Owned || !sameAuthority(replay.Authority, authority) {
		t.Fatalf("live reservation replay: %+v, %v", replay, err)
	}
	for name, mutate := range map[string]func(*playback.AttemptReservationRequestV3){
		"digest":  func(r *playback.AttemptReservationRequestV3) { r.RequestDigest = "different" },
		"user":    func(r *playback.AttemptReservationRequestV3) { r.UserID++ },
		"profile": func(r *playback.AttemptReservationRequestV3) { r.ProfileID = "another" },
		"file":    func(r *playback.AttemptReservationRequestV3) { r.RequestedMediaFileID = f.altFileID },
	} {
		t.Run(name, func(t *testing.T) {
			changed := req
			mutate(&changed)
			_, err := b.ReserveAttempt(t.Context(), changed)
			if !errors.Is(err, playback.ErrIdempotencyKeyReusedV3) {
				t.Fatalf("mismatch: %v", err)
			}
		})
	}
}

func TestAttemptAuthorityTakeoverAndFencing(t *testing.T) {
	f := newPlanstoreFixture(t)
	a := NewPostgres(f.pool)
	b, _ := authorityPeer(t, f)
	req := authorityRequest(f)
	first, err := a.ReserveAttempt(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	record := f.attemptRecord(uuid.NewString(), req.PlaybackAttemptID, req.RequestDigest)
	assertStale := func(authority playback.AttemptAuthorityV3) {
		t.Helper()
		if _, err := a.RenewAttempt(t.Context(), authority, time.Minute); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
			t.Fatalf("renew stale: %v", err)
		}
		if err := a.PublishAttempt(t.Context(), authority, record); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
			t.Fatalf("publish stale: %v", err)
		}
		if err := a.StopAttempt(t.Context(), authority); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
			t.Fatalf("stop stale: %v", err)
		}
	}
	wrong := first.Authority
	wrong.OwnerID = uuid.NewString()
	assertStale(wrong)
	wrong = first.Authority
	wrong.Epoch++
	assertStale(wrong)
	expireAuthorityLease(t, f, req.PlaybackAttemptID)
	// A caller cannot extend its authority by supplying a future local timestamp.
	forged := first.Authority
	forged.LeaseExpiresAt = time.Now().Add(100 * 365 * 24 * time.Hour)
	assertStale(forged)
	req.OwnerID = uuid.NewString()
	second, err := b.ReserveAttempt(t.Context(), req)
	if err != nil || !second.Owned || second.Authority.Epoch != first.Authority.Epoch+1 {
		t.Fatalf("takeover: %+v, %v", second, err)
	}
	assertStale(first.Authority)
	// Conversely, an old local timestamp must not expire a database-live lease.
	localPast := second.Authority
	localPast.LeaseExpiresAt = time.Time{}
	renewed, err := b.RenewAttempt(t.Context(), localPast, time.Minute)
	if err != nil {
		t.Fatalf("database-live renewal: %v", err)
	}
	var dbNow time.Time
	if err := f.pool.QueryRow(t.Context(), `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
		t.Fatal(err)
	}
	if renewed.LeaseExpiresAt.Before(dbNow) || renewed.LeaseExpiresAt.After(dbNow.Add(2*time.Minute)) {
		t.Fatalf("renewed lease outside DB clock bound: %v now=%v", renewed.LeaseExpiresAt, dbNow)
	}
	if err := b.PublishAttempt(t.Context(), renewed, record); err != nil {
		t.Fatal(err)
	}
	expireAuthorityLease(t, f, req.PlaybackAttemptID)
	active, err := a.ReserveAttempt(t.Context(), req)
	if err != nil || active.Owned || active.Authority.State != playback.AttemptActiveV3 || active.Record == nil || active.Record.SessionID != record.SessionID {
		t.Fatalf("active must replay despite expired preparing lease: %+v %v", active, err)
	}
}

func TestAttemptAuthorityTerminalAndStoppedReplay(t *testing.T) {
	f := newPlanstoreFixture(t)
	store := NewPostgres(f.pool)
	peer, _ := authorityPeer(t, f)
	for _, terminal := range []bool{true, false} {
		name := "stopped"
		if terminal {
			name = "terminal"
		}
		t.Run(name, func(t *testing.T) {
			req := authorityRequest(f)
			r, err := store.ReserveAttempt(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			record := f.attemptRecord(uuid.NewString(), req.PlaybackAttemptID, req.RequestDigest)
			expected := playback.AttemptStoppedV3
			if terminal {
				record.SessionID = ""
				record.CurrentPlanID = ""
				record.CurrentPlan = playback.PlanV3{}
				record.FrozenRecipe = playback.ExecutableRecipeV3{}
				record.StartResponse = playback.NewTerminalResponseV3("adaptation_unavailable", "No route", false)
				err = store.PublishAttempt(t.Context(), r.Authority, record)
				expected = playback.AttemptTerminalV3
			} else {
				err = store.StopAttempt(t.Context(), r.Authority)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := store.PublishAttempt(t.Context(), r.Authority, record); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
				t.Fatalf("final state allowed publication: %v", err)
			}
			got, err := peer.ReserveAttempt(t.Context(), req)
			if err != nil || got.Owned || got.Authority.State != expected || got.Record == nil {
				t.Fatalf("final replay: %+v %v", got, err)
			}
			if terminal && string(mustJSON(t, got.Record.StartResponse)) != string(mustJSON(t, record.StartResponse)) {
				t.Fatal("terminal response changed on replay")
			}
		})
	}
}

func TestAttemptAuthorityCancellationRollback(t *testing.T) {
	f := newPlanstoreFixture(t)
	store := NewPostgres(f.pool)
	peer, name := authorityPeer(t, f)
	req := authorityRequest(f)
	first, err := store.ReserveAttempt(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	}()
	if _, err := tx.Exec(t.Context(), `SELECT 1 FROM playback_v3_attempts WHERE playback_attempt_id=$1 FOR UPDATE`, req.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := peer.RenewAttempt(ctx, first.Authority, time.Minute); done <- err }()
	observe, stopObserve := context.WithTimeout(t.Context(), 5*time.Second)
	defer stopObserve()
	for {
		var blocked bool
		err := f.pool.QueryRow(observe, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, name).Scan(&blocked)
		if err != nil {
			t.Fatalf("renew did not reach row lock: %v", err)
		}
		if blocked {
			break
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-observe.Done():
		t.Fatal("canceled renewal did not finish")
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	replay, err := store.ReserveAttempt(t.Context(), req)
	if err != nil || !sameAuthority(replay.Authority, first.Authority) {
		t.Fatalf("canceled renewal changed authority: %+v %v", replay, err)
	}
	if _, err := peer.RenewAttempt(t.Context(), first.Authority, time.Minute); err != nil {
		t.Fatalf("cancellation leaked lock/transaction: %v", err)
	}
	canceled, stop := context.WithCancel(t.Context())
	stop()
	fresh := authorityRequest(f)
	if _, err := peer.ReserveAttempt(canceled, fresh); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reserve: %v", err)
	}
	reserved, err := store.ReserveAttempt(t.Context(), fresh)
	if err != nil || !reserved.Owned {
		t.Fatalf("canceled reserve left ownership: %+v %v", reserved, err)
	}
}

func sameAuthority(a, b playback.AttemptAuthorityV3) bool {
	return a.PlaybackAttemptID == b.PlaybackAttemptID && a.OwnerID == b.OwnerID && a.Epoch == b.Epoch && a.State == b.State && a.LeaseExpiresAt.Equal(b.LeaseExpiresAt)
}

func TestAttemptAuthorityLegacyWritersCannotBypass(t *testing.T) {
	f := newPlanstoreFixture(t)
	store := NewPostgres(f.pool)
	for _, state := range []string{"preparing", "active", "stopped"} {
		t.Run(state, func(t *testing.T) {
			req := authorityRequest(f)
			reservation, err := store.ReserveAttempt(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			record := f.attemptRecord(uuid.NewString(), req.PlaybackAttemptID, req.RequestDigest)
			if state != "preparing" {
				if err := store.PublishAttempt(t.Context(), reservation.Authority, record); err != nil {
					t.Fatal(err)
				}
			}
			if state == "stopped" {
				if err := store.StopAttempt(t.Context(), reservation.Authority); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.SaveAttempt(t.Context(), record); err == nil {
				t.Fatal("legacy SaveAttempt accepted controlled state")
			}
			// Preparing has no published session; active/stopped exercise the explicit
			// legacy control-state guards using their actual stored session identity.
			if _, err := store.BeginReplan(t.Context(), record.SessionID, "legacy-request", "legacy-digest", "", time.Now().Add(time.Minute)); err == nil {
				t.Fatal("legacy BeginReplan accepted controlled state")
			}
			record.CurrentPlanID = "bypass-plan"
			record.CurrentPlan.PlanID = "bypass-plan"
			if err := store.CompleteReplan(t.Context(), record.SessionID, "legacy-request", uuid.NewString(), "", []byte(`{}`), record); err == nil {
				t.Fatal("legacy CompleteReplan accepted controlled state")
			}
			replay, err := store.ReserveAttempt(t.Context(), req)
			if err != nil || string(replay.Authority.State) != state || replay.Record == nil || replay.Record.CurrentPlanID == "bypass-plan" {
				t.Fatalf("legacy writer changed controlled record: %+v %v", replay, err)
			}
		})
	}
}

func TestAttemptAuthorityLeaseCheckedAfterLockWait(t *testing.T) {
	f := newPlanstoreFixture(t)
	store := NewPostgres(f.pool)
	peer, name := authorityPeer(t, f)
	req := authorityRequest(f)
	reservation, err := store.ReserveAttempt(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	}()
	if _, err := tx.Exec(t.Context(), `SELECT 1 FROM playback_v3_attempts WHERE playback_attempt_id=$1 FOR UPDATE`, req.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { _, err := peer.RenewAttempt(ctx, reservation.Authority, time.Minute); done <- err }()
	for {
		var waiting bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, name).Scan(&waiting); err != nil {
			t.Fatalf("renew never blocked: %v", err)
		}
		if waiting {
			break
		}
	}
	if _, err := tx.Exec(t.Context(), `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 minute' WHERE playback_attempt_id=$1`, req.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
			t.Fatalf("post-wait expired lease renewed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("renew did not resume after lock release")
	}
	replay, err := store.ReserveAttempt(t.Context(), req)
	if err != nil || !replay.Owned || replay.Authority.Epoch != reservation.Authority.Epoch+1 {
		t.Fatalf("expired waiting renewal changed ownership: %+v %v", replay, err)
	}
}

func TestAttemptAuthorityRetentionUsesDatabaseClock(t *testing.T) {
	f := newPlanstoreFixture(t)
	store := NewPostgres(f.pool)
	req := authorityRequest(f)
	req.Retention = req.LeaseDuration
	reservation, err := store.ReserveAttempt(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !reservation.Authority.LeaseExpiresAt.Equal(reservation.Record.ExpiresAt) {
		t.Fatal("initial lease must not outlive equal retention")
	}
	renewed, err := store.RenewAttempt(t.Context(), reservation.Authority, 24*time.Hour)
	if err != nil || !renewed.LeaseExpiresAt.Equal(reservation.Record.ExpiresAt) {
		t.Fatalf("renewal exceeded retention: %+v %v", renewed, err)
	}
	if err := store.StopAttempt(t.Context(), renewed); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CleanupExpired(t.Context(), time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	replay, err := store.ReserveAttempt(t.Context(), req)
	if err != nil || replay.Owned || replay.Authority.State != playback.AttemptStoppedV3 {
		t.Fatalf("caller clock removed live tombstone: %+v %v", replay, err)
	}
}
