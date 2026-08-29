package lifecycleidempotency

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLifecycleIdempotencyRequiredFinalizerNeedsImmutableThreeClientEvidence(t *testing.T) {
	ctx := context.Background()
	pool := newLifecycleStoreDatabase(t)
	rollout := NewRollout(pool)
	input := FinalizeInput{
		ObservedRouteDigest:  testDigest(1),
		ExpectedRouteDigest:  testDigest(1),
		ObservedSchemaDigest: testDigest(2),
		ExpectedSchemaDigest: testDigest(2),
		ProductionWebDigest:  testDigest(3),
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
	mismatch.ObservedRouteDigest = testDigest(99)
	if err := rollout.Finalize(ctx, mismatch); !errors.Is(err, ErrRolloutDigestMismatch) {
		t.Fatalf("Finalize() route mismatch = %v, want ErrRolloutDigestMismatch", err)
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
	if err := rollout.Finalize(ctx, input); err != nil {
		t.Fatalf("Finalize() second call should be idempotent: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE lifecycle_idempotency_control SET phase='optional',finalized_at=NULL WHERE singleton`); err == nil {
		t.Fatal("required phase reversed")
	}
}
