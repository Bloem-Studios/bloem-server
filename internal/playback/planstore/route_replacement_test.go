package planstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

func replacementFixture(t *testing.T) (*initialActivationFixture, playback.RouteReplacementV3) {
	t.Helper()
	f := activatedLifecycleFixture(t)
	locator := playback.ExecutorRecipeLocatorV3{Executor: f.route.Executor, Digest: strings.Repeat("a", 64)}
	if err := f.store.PublishAttemptRecipeLocator(t.Context(), f.authority, nil, locator); err != nil {
		t.Fatal(err)
	}
	key := playback.RouteReplacementKeyV3{RequestID: uuid.NewString(), Digest: "replacement-digest"}
	lease, err := f.store.BeginBoundReplan(t.Context(), f.authority, f.record.SessionID, key.RequestID, key.Digest, "", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	key.LeaseToken = lease.LeaseToken
	next := f.record
	next.CurrentPlanID = uuid.NewString()
	next.CurrentPlan.PlanID = next.CurrentPlanID
	next.FrozenRecipe.PlanID = next.CurrentPlanID
	next.CurrentReplanRequestID = key.RequestID
	next.CurrentPlan.Timeline.SourceStartSeconds = 42
	next.StartResponse = playback.DecisionResponseV3{ProtocolVersion: 3, Outcome: playback.OutcomePlayableV3, SessionID: next.SessionID, PlaybackPlan: &next.CurrentPlan}
	response, _ := json.Marshal(next.StartResponse)
	route := f.route
	route.Executor.ExecutorID = uuid.NewString()
	route.TransportID = uuid.NewString()
	return f, playback.RouteReplacementV3{Key: key, PreviousPlanID: f.record.CurrentPlanID, PreviousRoute: f.route, PreviousLocator: locator, Next: next, Route: route, Locator: playback.ExecutorRecipeLocatorV3{Executor: route.Executor, Digest: strings.Repeat("b", 64)}, Response: response}
}
func stageReadyReplacement(t *testing.T, f *initialActivationFixture, doc playback.RouteReplacementV3) {
	t.Helper()
	if _, err := f.store.StageBoundRouteReplacement(t.Context(), f.binding, doc); err != nil {
		t.Fatal(err)
	}
	ready := playback.RouteReplacementReadyReceiptV3{Route: doc.Route, Locator: doc.Locator, ReceiptID: "prepared-exact-candidate"}
	if _, err := f.store.AcknowledgeBoundRouteReplacement(t.Context(), f.binding, doc.Key, ready); err != nil {
		t.Fatal(err)
	}
}

func TestRouteReplacementCutoverAndLostReply(t *testing.T) {
	f, doc := replacementFixture(t)
	ctx := t.Context()
	var before []byte
	var retention time.Time
	if err := f.pool.QueryRow(ctx, `SELECT control_activation,expires_at FROM playback_v3_attempts WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID).Scan(&before, &retention); err != nil {
		t.Fatal(err)
	}
	stageReadyReplacement(t, f, doc)
	// A lost grant response is represented by the persisted maximum, independent
	// of whether the caller retained the returned grant object.
	request := f.request
	request.Duration = 100 * time.Millisecond
	grant, err := f.store.IssueAttemptGrant(ctx, f.authority, request)
	if err != nil {
		t.Fatal(err)
	}
	candidate := request
	candidate.Executor = doc.Route.Executor
	candidate.TransportID = doc.Route.TransportID
	candidate.PlanID = doc.Next.CurrentPlanID
	candidate.Purpose = playback.AttemptGrantServeV3
	candidate.NodeID = doc.Route.EgressNodeID
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, candidate); err == nil {
		t.Fatal("candidate served before commit")
	}
	retiring, err := f.store.BeginBoundRouteRetirement(ctx, f.binding, doc.Key)
	if err != nil {
		t.Fatal(err)
	}
	if retiring.DrainNotBefore.Before(grant.NotAfter) {
		t.Fatal("lost issued grant omitted from barrier")
	}
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, request); err == nil {
		t.Fatal("predecessor renewed after retirement")
	}
	if _, err := f.store.CompleteBoundRouteReplacement(ctx, f.binding, doc.Key); err == nil {
		t.Fatal("cutover before predecessor deadline")
	}
	if _, err := f.store.CancelBoundRouteReplacement(ctx, f.binding, doc.Key); err == nil {
		t.Fatal("retired predecessor revived")
	}
	// Simulate future candidate issuance through its storage seam: it grows the
	// terminal aggregate, never the already frozen predecessor retirement bound.
	if _, err := f.pool.Exec(ctx, `UPDATE playback_v3_attempts SET control_grant_not_after=clock_timestamp()+interval '3 seconds' WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	waitInitialDatabaseTime(t, f, retiring.DrainNotBefore)
	if _, err := f.store.CompleteBoundRouteReplacement(ctx, f.binding, doc.Key); err != nil {
		t.Fatal(err)
	} // discard commit result
	replay, err := f.store.CompleteBoundRouteReplacement(ctx, f.binding, doc.Key)
	if err != nil || replay.Phase != playback.RouteReplacementCommittedV3 {
		t.Fatalf("commit replay %+v %v", replay, err)
	}
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, candidate); err != nil {
		t.Fatalf("committed successor grant: %v", err)
	}
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, request); err == nil {
		t.Fatal("predecessor grant after commit")
	}
	var after, route, locator []byte
	var expires, aggregate time.Time
	var plan string
	if err := f.pool.QueryRow(ctx, `SELECT control_activation,expires_at,control_route,control_recipe_locator,current_plan_id,control_grant_not_after FROM playback_v3_attempts WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID).Scan(&after, &expires, &route, &locator, &plan, &aggregate); err != nil {
		t.Fatal(err)
	}
	var gotRoute playback.AttemptGrantRouteV3
	var gotLocator playback.ExecutorRecipeLocatorV3
	_ = json.Unmarshal(route, &gotRoute)
	_ = json.Unmarshal(locator, &gotLocator)
	if string(before) != string(after) || !retention.Equal(expires) || gotRoute != doc.Route || gotLocator != doc.Locator || plan != doc.Next.CurrentPlanID || !aggregate.After(retiring.DrainNotBefore) {
		t.Fatal("cutover altered immutable envelope/retention or failed atomic route publication")
	}
	stored, err := f.store.GetAttemptByPlaybackAttemptID(ctx, f.authority.PlaybackAttemptID)
	if err != nil || stored.CurrentPlanID != doc.Next.CurrentPlanID {
		t.Fatalf("projection %v %v", stored, err)
	}
}

func TestRouteReplacementPendingCannotBeErasedOrBypassed(t *testing.T) {
	f, doc := replacementFixture(t)
	ctx := t.Context()
	if _, err := f.store.StageBoundRouteReplacement(ctx, f.binding, doc); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.StageBoundRouteReplacement(ctx, f.binding, doc); err != nil {
		t.Fatalf("stage replay: %v", err)
	}
	changed := doc
	changed.Route.TransportID = uuid.NewString()
	if _, err := f.store.StageBoundRouteReplacement(ctx, f.binding, changed); err == nil {
		t.Fatal("changed candidate accepted")
	}
	badKey := doc.Key
	badKey.Digest = "changed"
	if _, err := f.store.ReadBoundRouteReplacement(ctx, f.binding, badKey); err == nil {
		t.Fatal("changed digest read")
	}
	if _, err := f.store.BeginBoundReplan(ctx, f.authority, f.record.SessionID, doc.Key.RequestID, doc.Key.Digest, "wrong-base", time.Now().Add(time.Minute)); err == nil {
		t.Fatal("retained replacement accepted changed base")
	}
	if err := f.store.ReleaseReplan(ctx, f.record.SessionID, doc.Key.RequestID, doc.Key.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE playback_v3_replans SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE session_id=$1::uuid`, f.record.SessionID); err != nil {
		t.Fatal(err)
	}
	lease, err := f.store.BeginBoundReplan(ctx, f.authority, f.record.SessionID, doc.Key.RequestID, doc.Key.Digest, "", time.Now().Add(time.Minute))
	if err != nil || lease.State != playback.ReplanLeaseInFlightV3 {
		t.Fatalf("expired retained lease replaced: %+v %v", lease, err)
	}
	if err := f.store.CompleteBoundReplan(ctx, f.authority, f.record.SessionID, doc.Key.RequestID, doc.Key.LeaseToken, "", doc.Response, doc.Next); !errors.Is(err, playback.ErrReplanSupersededV3) {
		t.Fatalf("projection bypass: %v", err)
	}
	key2 := doc.Key
	key2.RequestID = uuid.NewString()
	lease, err = f.store.BeginBoundReplan(ctx, f.authority, f.record.SessionID, key2.RequestID, key2.Digest, "", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	key2.LeaseToken = lease.LeaseToken
	competing := doc
	competing.Key = key2
	competing.Next.CurrentReplanRequestID = key2.RequestID
	if _, err := f.store.StageBoundRouteReplacement(ctx, f.binding, competing); err == nil {
		t.Fatal("second candidate admitted")
	}
	cancelled, err := f.store.CancelBoundRouteReplacement(ctx, f.binding, doc.Key)
	if err != nil || cancelled.Phase != playback.RouteReplacementCancelledV3 {
		t.Fatalf("cancel %v %v", cancelled, err)
	}
	if err := f.store.ReleaseReplan(ctx, f.record.SessionID, doc.Key.RequestID, doc.Key.LeaseToken); err != nil {
		t.Fatal(err)
	}
	retained, err := f.store.ReadBoundRouteReplacement(ctx, f.binding, doc.Key)
	if err != nil || retained.Phase != playback.RouteReplacementCancelledV3 {
		t.Fatal("cancelled cleanup identity erased")
	}
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, f.request); err != nil {
		t.Fatalf("cancel changed predecessor: %v", err)
	}
}

func TestRouteReplacementStopWithNoCurrentRoute(t *testing.T) {
	f, doc := replacementFixture(t)
	ctx := t.Context()
	stageReadyReplacement(t, f, doc)
	request := f.request
	request.Duration = 50 * time.Millisecond
	grant, err := f.store.IssueAttemptGrant(ctx, f.authority, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.BeginBoundRouteRetirement(ctx, f.binding, doc.Key); err != nil {
		t.Fatal(err)
	}
	stopID := uuid.NewString()
	stopping, err := f.store.BeginBoundStop(ctx, f.binding, stopID)
	if err != nil {
		t.Fatal(err)
	}
	if stopping.DrainNotBefore.Before(grant.NotAfter) {
		t.Fatal("stop lost aggregate")
	}
	retained, err := f.store.ReadBoundRouteReplacement(ctx, f.binding, doc.Key)
	if err != nil || retained.PreviousRoute != doc.PreviousRoute || retained.Route != doc.Route || retained.PreviousLocator != doc.PreviousLocator || retained.Locator != doc.Locator {
		t.Fatalf("cleanup identities unavailable: %+v %v", retained, err)
	}
	if _, err := f.store.CompleteBoundRouteReplacement(ctx, f.binding, doc.Key); err == nil {
		t.Fatal("cutover after stop")
	}
	if _, err := f.store.IssueAttemptGrant(ctx, f.authority, request); err == nil {
		t.Fatal("grant after stop")
	}
	terminal := terminalInitialReceipt(t, f, stopID)
	receipt := f.receipt(t, terminal)
	waitInitialDatabaseTime(t, f, stopping.DrainNotBefore)
	if _, err := f.store.CompleteBoundStop(ctx, f.binding, stopID, receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CompleteBoundStop(ctx, f.binding, stopID, receipt); err != nil {
		t.Fatalf("terminal replay: %v", err)
	}
	after, err := f.store.ReadBoundRouteReplacement(ctx, f.binding, doc.Key)
	if err != nil || !reflect.DeepEqual(after, retained) {
		t.Fatal("terminal stop erased replacement")
	}
}

func TestRouteReplacementCapturedIdentity(t *testing.T) {
	for _, variation := range []string{"owner", "admission", "base", "plan", "route", "locator", "readiness", "progress mode", "response fields"} {
		t.Run(variation, func(t *testing.T) {
			f, doc := replacementFixture(t)
			binding := f.binding
			switch variation {
			case "response fields":
				var data map[string]any
				if err := json.Unmarshal(doc.Response, &data); err != nil {
					t.Fatal(err)
				}
				data["unretained_field"] = "changed"
				doc.Response, _ = json.Marshal(data)
			case "progress mode":
				doc.Next.NormalizedRequest.ProgressPersistence = playback.ProgressPersistenceClientV3
			case "owner":
				binding.Fence.OwnerID = uuid.NewString()
			case "admission":
				binding.AdmissionID = uuid.NewString()
			case "base":
				doc.Key.BaseReplanID = "wrong"
			case "plan":
				doc.PreviousPlanID = "wrong"
			case "route":
				doc.PreviousRoute.TransportID = uuid.NewString()
			case "locator":
				doc.PreviousLocator.Digest = strings.Repeat("c", 64)
			}
			if variation == "readiness" {
				if _, err := f.store.StageBoundRouteReplacement(t.Context(), binding, doc); err != nil {
					t.Fatal(err)
				}
				ready := playback.RouteReplacementReadyReceiptV3{Route: doc.Route, Locator: doc.Locator, ReceiptID: "receipt"}
				ready.Route.TransportID = uuid.NewString()
				if _, err := f.store.AcknowledgeBoundRouteReplacement(t.Context(), binding, doc.Key, ready); err == nil {
					t.Fatal("wrong readiness accepted")
				}
				return
			}
			if _, err := f.store.StageBoundRouteReplacement(t.Context(), binding, doc); err == nil {
				t.Fatal("stale captured identity accepted")
			}
		})
	}
}

func TestRouteReplacementStopCompetesWithCutover(t *testing.T) {
	f, doc := replacementFixture(t)
	ctx := t.Context()
	stageReadyReplacement(t, f, doc)
	if _, err := f.store.BeginBoundRouteRetirement(ctx, f.binding, doc.Key); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	cutover := make(chan error, 1)
	stop := make(chan error, 1)
	go func() {
		<-start
		_, err := f.store.CompleteBoundRouteReplacement(ctx, f.binding, doc.Key)
		cutover <- err
	}()
	go func() { <-start; _, err := f.store.BeginBoundStop(ctx, f.binding, uuid.NewString()); stop <- err }()
	close(start)
	cutoverErr := <-cutover
	if err := <-stop; err != nil {
		t.Fatal(err)
	}
	retained, err := f.store.ReadBoundRouteReplacement(ctx, f.binding, doc.Key)
	if err != nil {
		t.Fatal(err)
	}
	if cutoverErr == nil && retained.Phase != playback.RouteReplacementCommittedV3 {
		t.Fatal("committed cutover missing")
	}
	if cutoverErr != nil && retained.Phase != playback.RouteReplacementRetiringV3 {
		t.Fatalf("stop winner erased retirement: %v %+v", cutoverErr, retained)
	}
	if _, err := f.store.CompleteBoundRouteReplacement(ctx, f.binding, doc.Key); err == nil {
		t.Fatal("stopped authority allowed cutover")
	}
	for _, route := range []playback.AttemptGrantRouteV3{doc.PreviousRoute, doc.Route} {
		request := f.request
		request.Executor = route.Executor
		request.TransportID = route.TransportID
		if route == doc.Route {
			request.PlanID = doc.Next.CurrentPlanID
		}
		if _, err := f.store.IssueAttemptGrant(ctx, f.authority, request); err == nil {
			t.Fatal("stop left grant issuance open")
		}
	}
}

func TestRouteReplacementExpiryRetainsCleanupIdentity(t *testing.T) {
	f, doc := replacementFixture(t)
	ctx := t.Context()
	if _, err := f.store.StageBoundRouteReplacement(ctx, f.binding, doc); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CancelBoundRouteReplacement(ctx, f.binding, doc.Key); err == nil {
		t.Fatal("expired owner changed replacement")
	}
	retained, err := f.store.ReadBoundRouteReplacement(ctx, f.binding, doc.Key)
	if err != nil || retained.Route != doc.Route || retained.PreviousLocator != doc.PreviousLocator {
		t.Fatalf("expired owner cleanup unavailable: %+v %v", retained, err)
	}
}

func TestRouteReplacementRechecksOwnerAfterReplanLockWait(t *testing.T) {
	f, doc := replacementFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var expiry time.Time
	if err := f.pool.QueryRow(ctx, `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()+interval '300 milliseconds' WHERE playback_attempt_id=$1 RETURNING control_lease_expires_at`, f.authority.PlaybackAttemptID).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackAuthority(blocker)
	if _, err := blocker.Exec(ctx, `SELECT 1 FROM playback_v3_replans WHERE session_id=$1::uuid FOR UPDATE`, f.record.SessionID); err != nil {
		t.Fatal(err)
	}
	staged := make(chan error, 1)
	go func() { _, err := f.store.StageBoundRouteReplacement(ctx, f.binding, doc); staged <- err }()
	for {
		var waiting bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE 'SELECT request_digest,base_replan_request_id,lease_owner,state,lease_expires_at,route_replacement%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		runtime.Gosched()
	}
	waitInitialDatabaseTime(t, f, expiry)
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-staged; err == nil {
		t.Fatal("owner expired during replan lock wait but staged candidate")
	}
}

// Stage(A) holds the attempt while waiting for its replan row. Projection-only
// Complete(B) must inspect pending candidates after acquiring that attempt lock.
func TestRouteReplacementConcurrentProjectionCannotBypassStage(t *testing.T) {
	f, doc := replacementFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	otherID := uuid.NewString()
	lease, err := f.store.BeginBoundReplan(ctx, f.authority, f.record.SessionID, otherID, "other-digest", "", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	other := doc.Next
	other.CurrentReplanRequestID = otherID
	blocker, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackAuthority(blocker)
	if _, err := blocker.Exec(ctx, `SELECT 1 FROM playback_v3_replans WHERE session_id=$1::uuid AND replan_request_id=$2 FOR UPDATE`, f.record.SessionID, doc.Key.RequestID); err != nil {
		t.Fatal(err)
	}
	waitQuery := func(fragment string) {
		t.Helper()
		for {
			var waiting bool
			if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1)`, "%"+fragment+"%").Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				return
			}
			runtime.Gosched()
		}
	}
	staged := make(chan error, 1)
	go func() { _, err := f.store.StageBoundRouteReplacement(ctx, f.binding, doc); staged <- err }()
	waitQuery("SELECT request_digest,base_replan_request_id,lease_owner,state,lease_expires_at,route_replacement")
	completed := make(chan error, 1)
	go func() {
		completed <- f.store.CompleteBoundReplan(ctx, f.authority, f.record.SessionID, otherID, lease.LeaseToken, "", doc.Response, other)
	}()
	// Both the old UPDATE and corrected explicit lock identify the attempt here.
	waitQuery("playback_v3_attempts")
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-staged; err != nil {
		t.Fatal(err)
	}
	if err := <-completed; !errors.Is(err, playback.ErrReplanSupersededV3) {
		t.Fatalf("projection bypassed staged candidate: %v", err)
	}
	stored, err := f.store.GetAttemptByPlaybackAttemptID(ctx, f.authority.PlaybackAttemptID)
	if err != nil || stored.CurrentPlanID != doc.PreviousPlanID || stored.CurrentReplanRequestID != "" {
		t.Fatalf("predecessor projection changed: %+v %v", stored, err)
	}
	candidate, err := f.store.ReadBoundRouteReplacement(ctx, f.binding, doc.Key)
	if err != nil || candidate.Phase != playback.RouteReplacementStagedV3 || candidate.Key != doc.Key {
		t.Fatalf("candidate not intact: %+v %v", candidate, err)
	}
}
