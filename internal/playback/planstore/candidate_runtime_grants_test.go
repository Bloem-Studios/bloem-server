package planstore

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

type candidateRecipeReader struct {
	locator playback.ExecutorRecipeLocatorV3
	card    playback.RecipeCard
}

func (r candidateRecipeReader) GetImmutable(_ context.Context, l playback.ExecutorRecipeLocatorV3) (*playback.RecipeCard, error) {
	if l != r.locator {
		return nil, playback.ErrStaleAttemptAuthorityV3
	}
	return &r.card, nil
}

func TestCandidateRuntimeExecutionAndCutover(t *testing.T) {
	f, doc := replacementFixture(t)
	ctx := t.Context()
	if _, err := f.store.StageBoundRouteReplacement(ctx, f.binding, doc); err != nil {
		t.Fatal(err)
	}
	recipes := candidateRecipeReader{doc.Locator, playback.RecipeCard{SessionID: doc.Next.SessionID, TranscodeTransportID: doc.Route.TransportID, Executor: &doc.Route.Executor}}
	policy := playback.RuntimeGrantPolicyV3{MaxDuration: time.Second, SafetyMargin: 100 * time.Millisecond, RenewBefore: 200 * time.Millisecond, PollInterval: time.Millisecond}
	worker, err := NewExecutorRuntime(f.store, recipes, doc.Route.ExecutionNodeID, new(runtimeTestClock), policy)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewExecutorRuntime(f.store, recipes, doc.Route.ExecutionNodeID+100, new(runtimeTestClock), policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Resolve(ctx, doc.Route.TransportID, doc.Route.Executor); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Resolve(ctx, doc.Route.TransportID, doc.Route.Executor); err == nil {
		t.Fatal("other node resolved candidate")
	}
	if _, err := worker.Acquire(ctx, doc.Route.TransportID, doc.Route.Executor, playback.AttemptGrantServeV3); err == nil {
		t.Fatal("candidate serving acquired")
	}
	grant, err := worker.Acquire(ctx, doc.Route.TransportID, doc.Route.Executor, playback.AttemptGrantExecuteV3)
	if err != nil {
		t.Fatal(err)
	}
	grant.Close()
	request := f.request
	request.Executor = doc.Route.Executor
	request.TransportID = doc.Route.TransportID
	request.PlanID = doc.Next.CurrentPlanID
	request.Duration = 10 * time.Millisecond
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, request); err != nil {
		t.Fatal(err)
	}
	stageReadyReplacement(t, f, doc)
	retiring, err := f.store.BeginBoundRouteRetirement(ctx, f.binding, doc.Key)
	if err != nil {
		t.Fatal(err)
	}
	request.Duration = 3 * time.Second
	renewed, err := f.store.IssueAttemptGrant(ctx, f.authority, request)
	if err != nil {
		t.Fatal(err)
	}
	if !renewed.NotAfter.After(retiring.DrainNotBefore) {
		t.Fatal("candidate did not extend aggregate")
	}
	stored, err := f.store.ReadBoundRouteReplacement(ctx, f.binding, doc.Key)
	if err != nil || !stored.DrainNotBefore.Equal(retiring.DrainNotBefore) {
		t.Fatal("candidate changed frozen drain", err)
	}
	if _, err := worker.Resolve(ctx, doc.Route.TransportID, doc.Route.Executor); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, f.request); err == nil {
		t.Fatal("predecessor renewed")
	}
	for _, purpose := range []playback.AttemptGrantPurposeV3{playback.AttemptGrantServeV3, playback.AttemptGrantTransferV3} {
		bad := request
		bad.Purpose = purpose
		bad.NodeID = doc.Route.EgressNodeID
		if purpose == playback.AttemptGrantTransferV3 {
			bad.NodeID = doc.Route.ExecutionNodeID
			bad.EgressNodeID = doc.Route.EgressNodeID
			bad.OutputTransferID = uuid.NewString()
		}
		if _, err := f.store.IssueAttemptGrant(ctx, f.authority, bad); err == nil {
			t.Fatal("candidate obtained", purpose)
		}
	}
	waitInitialDatabaseTime(t, f, retiring.DrainNotBefore)
	if _, err := f.store.CompleteBoundRouteReplacement(ctx, f.binding, doc.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, request); err != nil {
		t.Fatal("execute renewal after cutover", err)
	}
	request.Purpose = playback.AttemptGrantServeV3
	request.NodeID = doc.Route.EgressNodeID
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, request); err != nil {
		t.Fatal("committed serve", err)
	}
}

func TestCandidateGrantRevocation(t *testing.T) {
	for _, action := range []string{"stop", "owner", "expiry", "withdraw", "cancel", "wrong_plan", "wrong_node"} {
		t.Run(action, func(t *testing.T) {
			f, doc := replacementFixture(t)
			ctx := t.Context()
			stageReadyReplacement(t, f, doc)
			request := f.request
			request.Executor = doc.Route.Executor
			request.TransportID = doc.Route.TransportID
			request.PlanID = doc.Next.CurrentPlanID
			if _, err := f.store.IssueAttemptGrant(ctx, f.authority, request); err != nil {
				t.Fatal(err)
			}
			var err error
			switch action {
			case "stop":
				_, err = f.store.BeginBoundStop(ctx, f.binding, uuid.NewString())
			case "owner":
				_, err = f.pool.Exec(ctx, `UPDATE playback_v3_attempts SET control_owner=$2::uuid WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID, uuid.NewString())
			case "expiry":
				_, err = f.pool.Exec(ctx, `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID)
			case "withdraw":
				_, err = f.pool.Exec(ctx, `UPDATE playback_source_registrations SET admission_state='retiring' WHERE user_id=$1`, f.userID)
			case "cancel":
				_, err = f.store.CancelBoundRouteReplacement(ctx, f.binding, doc.Key)
			case "wrong_plan":
				request.PlanID = uuid.NewString()
			case "wrong_node":
				request.NodeID += 100
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.IssueAttemptGrant(ctx, f.authority, request); err == nil {
				t.Fatal("revoked candidate renewed")
			}
			if action == "withdraw" {
				if _, err := f.store.IssueAttemptGrant(ctx, f.authority, f.request); err == nil {
					t.Fatal("withdrawn source current grant issued")
				}
			}
		})
	}
}

func TestCandidateGrantRechecksAfterLockWait(t *testing.T) {
	for _, kind := range []string{"withdrawal", "owner_expiry"} {
		t.Run(kind, func(t *testing.T) {
			f, doc := replacementFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			stageReadyReplacement(t, f, doc)
			request := f.request
			request.Executor = doc.Route.Executor
			request.TransportID = doc.Route.TransportID
			request.PlanID = doc.Next.CurrentPlanID
			blocker, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rollbackAuthority(blocker)
			var expiry time.Time
			var queryFragment string
			if kind == "withdrawal" {
				_, err = blocker.Exec(ctx, `UPDATE playback_source_registrations SET admission_state='retiring' WHERE user_id=$1`, f.userID)
				queryFragment = "SELECT backend,source_id::text,selection_generation,admission_id::text,admission_state"
			} else {
				if err := f.pool.QueryRow(ctx, `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()+interval '300 milliseconds' WHERE playback_attempt_id=$1 RETURNING control_lease_expires_at`, f.authority.PlaybackAttemptID).Scan(&expiry); err != nil {
					t.Fatal(err)
				}
				_, err = blocker.Exec(ctx, `SELECT 1 FROM playback_v3_replans WHERE session_id=$1::uuid AND replan_request_id=$2 FOR UPDATE`, f.record.SessionID, doc.Key.RequestID)
				queryFragment = "SELECT replan_request_id,request_digest,base_replan_request_id,lease_owner,route_replacement"
			}
			if err != nil {
				t.Fatal(err)
			}
			issued := make(chan error, 1)
			go func() { _, err := f.store.IssueAttemptGrant(ctx, f.authority, request); issued <- err }()
			for {
				var waiting bool
				if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1)`, "%"+queryFragment+"%").Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				runtime.Gosched()
			}
			if kind == "owner_expiry" {
				waitInitialDatabaseTime(t, f, expiry)
			}
			if err := blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-issued; err == nil {
				t.Fatal("grant issued after revocation during lock wait")
			}
			var deadline *time.Time
			if err := f.pool.QueryRow(ctx, `SELECT control_grant_not_after FROM playback_v3_attempts WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID).Scan(&deadline); err != nil {
				t.Fatal(err)
			}
			if deadline != nil {
				t.Fatal("rejected issuance changed aggregate")
			}
		})
	}
}

func TestCandidateGrantStopIncludesLostRenewal(t *testing.T) {
	f, doc := replacementFixture(t)
	ctx := t.Context()
	stageReadyReplacement(t, f, doc)
	retiring, err := f.store.BeginBoundRouteRetirement(ctx, f.binding, doc.Key)
	if err != nil {
		t.Fatal(err)
	}
	request := f.request
	request.Executor = doc.Route.Executor
	request.TransportID = doc.Route.TransportID
	request.PlanID = doc.Next.CurrentPlanID
	grant, err := f.store.IssueAttemptGrant(ctx, f.authority, request)
	if err != nil {
		t.Fatal(err)
	}
	stopping, err := f.store.BeginBoundStop(ctx, f.binding, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if stopping.DrainNotBefore.Before(grant.NotAfter) || !stopping.DrainNotBefore.After(retiring.DrainNotBefore) {
		t.Fatal("stop omitted candidate maximum")
	}
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, request); err == nil {
		t.Fatal("candidate renewed after stop")
	}
}

func TestCandidateRuntimeClosesOnWithdrawnRenewal(t *testing.T) {
	f, doc := replacementFixture(t)
	ctx := t.Context()
	stageReadyReplacement(t, f, doc)
	recipes := candidateRecipeReader{doc.Locator, playback.RecipeCard{SessionID: doc.Next.SessionID, TranscodeTransportID: doc.Route.TransportID, Executor: &doc.Route.Executor}}
	clock := new(runtimeTestClock)
	policy := playback.RuntimeGrantPolicyV3{MaxDuration: time.Second, SafetyMargin: 100 * time.Millisecond, RenewBefore: 200 * time.Millisecond, PollInterval: time.Millisecond}
	worker, err := NewExecutorRuntime(f.store, recipes, doc.Route.ExecutionNodeID, clock, policy)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := worker.Acquire(ctx, doc.Route.TransportID, doc.Route.Executor, playback.AttemptGrantExecuteV3)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	if _, err := f.pool.Exec(ctx, `UPDATE playback_source_registrations SET admission_state='retiring' WHERE user_id=$1`, f.userID); err != nil {
		t.Fatal(err)
	}
	clock.tick.Store(int64(750 * time.Millisecond))
	timeout, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	select {
	case <-grant.Context().Done():
	case <-timeout.Done():
		t.Fatal("candidate did not close after withdrawn renewal")
	}
	if err := grant.Check(); err == nil {
		t.Fatal("withdrawn candidate revived")
	}
}
