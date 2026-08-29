package lifecycleidempotency

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestLifecycleIdempotencyRequiredFinalizerNeedsImmutableThreeClientEvidence(t *testing.T) {
	ctx := context.Background()
	pool := newLifecycleStoreDatabase(t)
	compiledRouteDigest := testDigest(1)
	rollout := NewRollout(pool, compiledRouteDigest)
	initialStatus, err := rollout.Status(ctx)
	if err != nil {
		t.Fatalf("Status() before finalization: %v", err)
	}
	if initialStatus.CurrentRouteDigest != compiledRouteDigest || initialStatus.CurrentSchemaDigest == (Digest{}) {
		t.Fatalf("current release digests = route %x schema %x", initialStatus.CurrentRouteDigest, initialStatus.CurrentSchemaDigest)
	}
	input := FinalizeInput{
		ExpectedRouteDigest:  compiledRouteDigest,
		ExpectedSchemaDigest: initialStatus.CurrentSchemaDigest,
		ProductionWebDigest:  testDigest(3),
	}
	routeMismatch := input
	routeMismatch.ExpectedRouteDigest = testDigest(99)
	if err := rollout.Finalize(ctx, routeMismatch); !errors.Is(err, ErrRolloutDigestMismatch) {
		t.Fatalf("Finalize() route mismatch = %v, want ErrRolloutDigestMismatch", err)
	}
	if err := rollout.Finalize(ctx, input); !errors.Is(err, ErrClientEvidenceIncomplete) {
		t.Fatalf("Finalize() without evidence = %v, want ErrClientEvidenceIncomplete", err)
	}

	for index, client := range []string{"web", "apple", "android"} {
		channel := testDigest(byte(10 + index))
		if client == "web" {
			channel = input.ProductionWebDigest
		}
		err := rollout.RecordClientEvidence(ctx, ClientEvidence{
			Client: client, CommitSHA: "0123456789abcdef0123456789abcdef01234567",
			SuiteDigest: testDigest(byte(20 + index)), ReleasedAt: time.Now().Add(-time.Minute),
			ReleaseChannelDigest: channel,
		})
		if err != nil {
			t.Fatalf("RecordClientEvidence(%s): %v", client, err)
		}
	}
	if err := rollout.RecordClientEvidence(ctx, ClientEvidence{
		Client: "web", CommitSHA: "fedcba9876543210fedcba9876543210fedcba98",
		SuiteDigest: testDigest(30), ReleasedAt: time.Now(), ReleaseChannelDigest: input.ProductionWebDigest,
	}); err == nil {
		t.Fatal("replacing immutable web evidence succeeded")
	}
	if _, err := pool.Exec(ctx, `UPDATE lifecycle_idempotency_client_release_evidence SET commit_sha=commit_sha WHERE client='web'`); err == nil {
		t.Fatal("updating immutable evidence succeeded")
	}

	mismatch := input
	mismatch.ExpectedSchemaDigest = testDigest(99)
	if err := rollout.Finalize(ctx, mismatch); !errors.Is(err, ErrRolloutDigestMismatch) {
		t.Fatalf("Finalize() schema mismatch = %v, want ErrRolloutDigestMismatch", err)
	}
	if err := rollout.Finalize(ctx, input); err != nil {
		t.Fatalf("Finalize() valid evidence: %v", err)
	}
	status, err := rollout.Status(ctx)
	if err != nil {
		t.Fatalf("Status(): %v", err)
	}
	if status.Phase != PhaseRequired || len(status.Evidence) != 3 {
		t.Fatalf("status = %+v, want required with three evidence rows", status)
	}
	if !status.RouteMatchesFinalized || !status.SchemaMatchesFinalized {
		t.Fatalf("finalized digest comparison = route %t schema %t, want both true", status.RouteMatchesFinalized, status.SchemaMatchesFinalized)
	}
	if err := rollout.Finalize(ctx, input); err != nil {
		t.Fatalf("Finalize() second call should be idempotent: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE lifecycle_request_receipts ADD COLUMN release_audit_drift boolean`); err != nil {
		t.Fatalf("alter lifecycle schema fixture: %v", err)
	}
	driftedStatus, err := rollout.Status(ctx)
	if err != nil {
		t.Fatalf("Status() after schema drift: %v", err)
	}
	if driftedStatus.SchemaMatchesFinalized {
		t.Fatal("status did not detect live lifecycle schema drift")
	}
	if err := rollout.Finalize(ctx, input); !errors.Is(err, ErrRolloutDigestMismatch) {
		t.Fatalf("Finalize() after schema drift = %v, want ErrRolloutDigestMismatch", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE lifecycle_idempotency_control SET phase='optional',finalized_at=NULL WHERE singleton`); err == nil {
		t.Fatal("required phase reversed")
	}
}

func TestLifecycleIdempotencyRequiredFinalizerSerializesWithUnkeyedMutation(t *testing.T) {
	ctx := context.Background()
	pool := newLifecycleStoreDatabase(t)
	compiledRouteDigest := testDigest(1)
	rollout := NewRollout(pool, compiledRouteDigest)
	status, err := rollout.Status(ctx)
	if err != nil {
		t.Fatalf("Status(): %v", err)
	}
	input := FinalizeInput{
		ExpectedRouteDigest: compiledRouteDigest, ExpectedSchemaDigest: status.CurrentSchemaDigest,
		ProductionWebDigest: testDigest(3),
	}
	for index, client := range []string{"web", "apple", "android"} {
		channel := testDigest(byte(10 + index))
		if client == "web" {
			channel = input.ProductionWebDigest
		}
		if err := rollout.RecordClientEvidence(ctx, ClientEvidence{
			Client: client, CommitSHA: "0123456789abcdef0123456789abcdef01234567",
			SuiteDigest: testDigest(byte(20 + index)), ReleasedAt: time.Now().Add(-time.Minute),
			ReleaseChannelDigest: channel,
		}); err != nil {
			t.Fatalf("RecordClientEvidence(%s): %v", client, err)
		}
	}

	unkeyed, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin unkeyed mutation: %v", err)
	}
	defer func() { _ = unkeyed.Rollback(ctx) }()
	store := NewPostgresStore(pool)
	if err := store.LockHandoff(ctx, unkeyed); err != nil {
		t.Fatalf("lock unkeyed handoff: %v", err)
	}
	phase, err := store.Phase(ctx, unkeyed)
	if err != nil || phase != PhaseOptional {
		t.Fatalf("unkeyed phase = %q, %v", phase, err)
	}
	if _, err := unkeyed.Exec(ctx, `CREATE TABLE lifecycle_unkeyed_effect (committed boolean NOT NULL)`); err != nil {
		t.Fatalf("write unkeyed effect: %v", err)
	}
	if _, err := unkeyed.Exec(ctx, `INSERT INTO lifecycle_unkeyed_effect VALUES (true)`); err != nil {
		t.Fatalf("insert unkeyed effect: %v", err)
	}

	finalized := make(chan error, 1)
	go func() { finalized <- rollout.Finalize(ctx, input) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND NOT granted`).Scan(&waiting); err != nil {
			t.Fatalf("observe finalizer lock wait: %v", err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finalizer never waited behind admitted unkeyed mutation")
		}
		runtime.Gosched()
	}
	var observedPhase Phase
	if err := pool.QueryRow(ctx, `SELECT phase FROM lifecycle_idempotency_control WHERE singleton`).Scan(&observedPhase); err != nil || observedPhase != PhaseOptional {
		t.Fatalf("phase while unkeyed mutation is in flight = %q, %v", observedPhase, err)
	}
	if err := unkeyed.Commit(ctx); err != nil {
		t.Fatalf("commit admitted unkeyed mutation: %v", err)
	}
	if err := <-finalized; err != nil {
		t.Fatalf("Finalize() after unkeyed commit: %v", err)
	}
	var effect bool
	if err := pool.QueryRow(ctx, `SELECT committed FROM lifecycle_unkeyed_effect`).Scan(&effect); err != nil || !effect {
		t.Fatalf("unkeyed effect committed = %v, %v", effect, err)
	}
	if err := pool.QueryRow(ctx, `SELECT phase FROM lifecycle_idempotency_control WHERE singleton`).Scan(&observedPhase); err != nil || observedPhase != PhaseRequired {
		t.Fatalf("phase after finalizer = %q, %v", observedPhase, err)
	}
}
