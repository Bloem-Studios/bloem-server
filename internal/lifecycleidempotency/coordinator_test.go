package lifecycleidempotency

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestLifecycleIdempotencyReplayWinsBeforeTargetLookup(t *testing.T) {
	actorID := 41
	incarnation := uuid.New()
	request := testRequest(actorID, incarnation)
	store := &recordingStore{
		phase: PhaseOptional,
		receipt: &Receipt{
			KeyDigest: testDigest(9),
			Binding:   request.Binding,
			State:     StateCompleted,
			Result:    Result{Status: 204, Body: []byte(`{"deleted":true}`)},
		},
	}
	request.IdempotencyKey = "replay-key-1234567890"
	request.ResolveTargets = func(context.Context, pgx.Tx) ([]TargetBinding, error) {
		t.Fatal("replay resolved a live target")
		return nil, nil
	}

	coordinator := NewCoordinator(store, fixedDigester(testDigest(9)))
	result, err := coordinator.Execute(context.Background(), request, func(context.Context, pgx.Tx, Binding) (Result, error) {
		t.Fatal("replay invoked mutator")
		return Result{}, nil
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if result.Status != 204 || !result.Replayed {
		t.Fatalf("result = %+v, want replayed 204", result)
	}
	if store.findCalls != 1 || store.insertCalls != 0 {
		t.Fatalf("store calls find=%d insert=%d", store.findCalls, store.insertCalls)
	}
}

func TestLifecycleIdempotencyConflictWinsBeforeTargetLookup(t *testing.T) {
	request := testRequest(41, uuid.New())
	request.IdempotencyKey = "conflict-key-12345678"
	stored := request.Binding
	stored.RouteID = "admin.account.update"
	store := &recordingStore{phase: PhaseOptional, receipt: &Receipt{KeyDigest: testDigest(4), Binding: stored, State: StateCompleted, Result: Result{Status: 200}}}
	request.ResolveTargets = func(context.Context, pgx.Tx) ([]TargetBinding, error) {
		t.Fatal("conflict resolved a live target")
		return nil, nil
	}

	_, err := NewCoordinator(store, fixedDigester(testDigest(4))).Execute(context.Background(), request, func(context.Context, pgx.Tx, Binding) (Result, error) {
		t.Fatal("conflict invoked mutator")
		return Result{}, nil
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Execute() error = %v, want ErrConflict", err)
	}
}

func TestLifecycleIdempotencyRequiredRejectsMissingKeyBeforeLookup(t *testing.T) {
	request := testRequest(41, uuid.New())
	request.ResolveTargets = func(context.Context, pgx.Tx) ([]TargetBinding, error) {
		t.Fatal("missing key resolved a target")
		return nil, nil
	}
	store := &recordingStore{phase: PhaseRequired}

	_, err := NewCoordinator(store, fixedDigester(testDigest(1))).Execute(context.Background(), request, func(context.Context, pgx.Tx, Binding) (Result, error) {
		t.Fatal("missing key invoked mutator")
		return Result{}, nil
	})
	if !errors.Is(err, ErrKeyRequired) {
		t.Fatalf("Execute() error = %v, want ErrKeyRequired", err)
	}
	if store.findCalls != 0 {
		t.Fatalf("receipt lookups = %d, want 0", store.findCalls)
	}
}

func TestLifecycleIdempotencyOptionalUnkeyedRunsOnceWithoutReceipt(t *testing.T) {
	request := testRequest(41, uuid.New())
	target := TargetBinding{OrganizationID: uuid.New(), MembershipID: uuid.New(), AccountID: 77, AccountIncarnationID: uuid.New()}
	resolveCalls, mutationCalls := 0, 0
	request.ResolveTargets = func(context.Context, pgx.Tx) ([]TargetBinding, error) {
		resolveCalls++
		return []TargetBinding{target}, nil
	}
	store := &recordingStore{phase: PhaseOptional}

	result, err := NewCoordinator(store, fixedDigester(testDigest(1))).Execute(context.Background(), request, func(_ context.Context, _ pgx.Tx, binding Binding) (Result, error) {
		mutationCalls++
		if len(binding.Targets) != 1 || binding.Targets[0] != target {
			t.Fatalf("mutation binding targets = %+v", binding.Targets)
		}
		return Result{Status: 202}, nil
	})
	if err != nil || result.Status != 202 {
		t.Fatalf("Execute() = %+v, %v", result, err)
	}
	if resolveCalls != 1 || mutationCalls != 1 || store.insertCalls != 0 {
		t.Fatalf("calls resolve=%d mutate=%d insert=%d", resolveCalls, mutationCalls, store.insertCalls)
	}
}

func TestLifecycleIdempotencyPreauthReplayHasNoNumericActorLookup(t *testing.T) {
	request := Request{
		IdempotencyKey: "preauth-key-1234567890",
		Binding: Binding{
			ActorKind: ActorPreauthIntent, ActorSubjectDigest: testDigest(7),
			Method: "POST", RouteID: "auth.invitation.accept",
			RequestHash: testDigest(8), TargetSource: TargetBodyAccount,
		},
		ResolveTargets: func(context.Context, pgx.Tx) ([]TargetBinding, error) {
			t.Fatal("preauth replay resolved consumed invitation")
			return nil, nil
		},
	}
	store := &recordingStore{phase: PhaseOptional, receipt: &Receipt{
		KeyDigest: testDigest(6), Binding: request.Binding, State: StateCompleted,
		Result: Result{Status: 201},
	}}

	result, err := NewCoordinator(store, fixedDigester(testDigest(6))).Execute(context.Background(), request, func(context.Context, pgx.Tx, Binding) (Result, error) {
		t.Fatal("preauth replay invoked mutator")
		return Result{}, nil
	})
	if err != nil || result.Status != 201 || !result.Replayed {
		t.Fatalf("Execute() = %+v, %v", result, err)
	}
}

func TestLifecycleIdempotencyRejectsActorWithoutImmutableIdentityBeforeTargetLookup(t *testing.T) {
	accountID := 41
	request := Request{
		IdempotencyKey: "missing-incarnation-key",
		Binding: Binding{
			ActorKind: ActorAuthenticatedAccount, ActorAccountID: &accountID,
			Method: "DELETE", RouteID: "admin.account.delete",
			RequestHash: testDigest(3), TargetSource: TargetPathAccount,
		},
		ResolveTargets: func(context.Context, pgx.Tx) ([]TargetBinding, error) {
			t.Fatal("invalid actor resolved a target")
			return nil, nil
		},
	}
	store := &recordingStore{phase: PhaseOptional}
	_, err := NewCoordinator(store, fixedDigester(testDigest(5))).Execute(context.Background(), request, func(context.Context, pgx.Tx, Binding) (Result, error) {
		t.Fatal("invalid actor invoked mutator")
		return Result{}, nil
	})
	if !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("Execute() error = %v, want ErrInvalidBinding", err)
	}
	if store.findCalls != 0 {
		t.Fatalf("invalid actor receipt lookups = %d, want 0", store.findCalls)
	}
}

func testRequest(accountID int, incarnation uuid.UUID) Request {
	return Request{Binding: Binding{
		ActorKind: ActorAuthenticatedAccount, ActorAccountID: &accountID,
		ActorAccountIncarnationID: &incarnation, Method: "DELETE",
		RouteID: "admin.account.delete", RequestHash: testDigest(2),
		TargetSource: TargetPathAccount,
	}}
}

func testDigest(first byte) Digest {
	var digest Digest
	digest[0] = first
	return digest
}

func fixedDigester(digest Digest) KeyDigester { return func(string) Digest { return digest } }

type recordingStore struct {
	phase       Phase
	receipt     *Receipt
	findCalls   int
	insertCalls int
}

func (s *recordingStore) InTransaction(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return fn(ctx, nil)
}
func (s *recordingStore) LockHandoff(context.Context, pgx.Tx) error     { return nil }
func (s *recordingStore) Phase(context.Context, pgx.Tx) (Phase, error)  { return s.phase, nil }
func (s *recordingStore) LockKey(context.Context, pgx.Tx, Digest) error { return nil }
func (s *recordingStore) Find(_ context.Context, _ pgx.Tx, _ Digest) (*Receipt, error) {
	s.findCalls++
	return s.receipt, nil
}
func (s *recordingStore) Insert(_ context.Context, _ pgx.Tx, _ Receipt) error {
	s.insertCalls++
	return nil
}
func (s *recordingStore) Complete(context.Context, pgx.Tx, Digest, Result) error { return nil }
