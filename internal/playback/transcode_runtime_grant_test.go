package playback

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type executorGrantTestClock struct{ elapsed atomic.Int64 }

func (c *executorGrantTestClock) Now() (time.Duration, error) {
	return time.Duration(c.elapsed.Load()), nil
}

func executorGrantTestProvider(clock *executorGrantTestClock) ExecutorGrantProviderV3 {
	if clock == nil {
		clock = new(executorGrantTestClock)
	}
	return func(ctx context.Context, transportID string, executor ExecutorNamespaceV3, purpose AttemptGrantPurposeV3) (*RuntimeGrantV3, error) {
		authority := AttemptAuthorityV3{PlaybackAttemptID: "attempt", OwnerID: "owner", Incarnation: executor.Incarnation, Epoch: executor.Epoch, State: AttemptActiveV3}
		request := AttemptGrantRequestV3{Executor: executor, SessionID: transportID, PlanID: "plan", TransportID: transportID, Purpose: purpose, Duration: time.Minute}
		return AcquireRuntimeGrantV3(ctx, func(_ context.Context, a AttemptAuthorityV3, r AttemptGrantRequestV3) (AttemptGrantV3, error) {
			now := time.Now()
			a.LeaseExpiresAt = now.Add(2 * r.Duration)
			return AttemptGrantV3{Authority: a, Request: r, IssuedAt: now, NotAfter: now.Add(r.Duration)}, nil
		}, clock, RuntimeGrantPolicyV3{MaxDuration: time.Minute, SafetyMargin: time.Second, RenewBefore: 10 * time.Second, PollInterval: time.Millisecond}, authority, request)
	}
}

func TestBoundTranscodeGrantExpiresAfterRequestDisconnect(t *testing.T) {
	namespace := executorFixture()
	output, _ := namespace.OutputDir(t.TempDir())
	clock := new(executorGrantTestClock)
	ctx, disconnect := context.WithCancel(t.Context())
	session, err := StartTranscode(ctx, TranscodeOpts{SessionID: "logical-session", TranscodeTransportID: "transport", Executor: &namespace, ExecuteGrants: func(ctx context.Context, transport string, ns ExecutorNamespaceV3, purpose AttemptGrantPurposeV3) (*RuntimeGrantV3, error) {
		if transport != "transport" {
			return nil, errors.New("logical session used as transport")
		}
		return executorGrantTestProvider(clock)(ctx, transport, ns, purpose)
	}, OutputDir: output, FFmpegPath: executorTestBinary(t), FastStart: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.WaitForManifest(time.Second); err != nil {
		t.Fatal(err)
	}
	disconnect()
	if !session.IsRunning() {
		t.Fatal("request disconnect stopped adopted process")
	}
	clock.elapsed.Store(int64(2 * time.Minute))
	select {
	case <-session.done:
	case <-time.After(4 * time.Second):
		t.Fatal("expired grant did not terminate FFmpeg")
	}
	if session.IsRunning() {
		t.Fatal("expired process still running")
	}
}

func TestBoundTranscodeMissingGrantHasNoFilesystemEffects(t *testing.T) {
	namespace := executorFixture()
	output, _ := namespace.OutputDir(t.TempDir())
	if _, err := StartTranscode(t.Context(), TranscodeOpts{SessionID: "transport", Executor: &namespace, OutputDir: output}); err == nil {
		t.Fatal("missing provider accepted")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unauthorized output created: %v", err)
	}
}

func TestBoundTranscodeRejectsForeignGrantBeforeClaim(t *testing.T) {
	namespace := executorFixture()
	output, _ := namespace.OutputDir(t.TempDir())
	provider := executorGrantTestProvider(nil)
	foreign := executorFixture()
	_, err := StartTranscode(t.Context(), TranscodeOpts{SessionID: "transport", Executor: &namespace, OutputDir: output,
		ExecuteGrants: func(ctx context.Context, transport string, _ ExecutorNamespaceV3, purpose AttemptGrantPurposeV3) (*RuntimeGrantV3, error) {
			return provider(ctx, transport, foreign, purpose)
		}})
	if !errors.Is(err, ErrExecutorNamespaceMismatch) {
		t.Fatalf("foreign grant accepted: %v", err)
	}
	if err := claimExecutorOutput(output, namespace); err != nil {
		t.Fatalf("rejected grant consumed namespace: %v", err)
	}
}

func TestBoundSessionReconstructionUsesAuthoritativeRecipe(t *testing.T) {
	namespace := executorFixture()
	card := NewRecipeCard(1, "profile", 3, "", TranscodeOpts{SessionID: "transport", Executor: &namespace})
	manager := NewTranscodeManager()
	manager.Sessions = NewSessionManager(0, 0)
	manager.ExecuteGrants = executorGrantTestProvider(nil)
	if got := manager.ReconstructSession(t.Context(), card.SessionID, 1, card); got != nil {
		t.Fatal("reconstructed bound metadata without authoritative recipe")
	}
	authoritative := card
	authoritative.MediaFileID = 17
	manager.ResolveExecutorRecipe = func(context.Context, string, ExecutorNamespaceV3) (*RecipeCard, error) {
		return &authoritative, nil
	}
	got := manager.ReconstructSession(t.Context(), card.SessionID, 1, card)
	if got == nil || got.MediaFileID != 17 || MatchExecutorNamespace(got.Executor, &namespace) != nil {
		t.Fatalf("authoritative metadata not retained: %+v", got)
	}
}

func TestBoundMetadataSessionRequiresReferenceAndGrant(t *testing.T) {
	namespace := executorFixture()
	session := &Session{ID: "transport", UserID: 1, Executor: &namespace}
	manager := NewTranscodeManager()
	get := func(string) (*Session, error) { return session, nil }
	card := &RecipeCard{SessionID: session.ID, Executor: &namespace}
	for _, reference := range []*RecipeCard{nil, card} {
		if _, status, _ := manager.LoadOrReconstructSessionDetail(t.Context(), get, session.ID, 1, reference); status != SessionLoadFailed {
			t.Fatalf("ungranted metadata accepted: %v", status)
		}
	}
	manager.ExecuteGrants = executorGrantTestProvider(nil)
	if _, status, _ := manager.LoadOrReconstructSessionDetail(t.Context(), get, session.ID, 1, card); status != SessionLoaded {
		t.Fatalf("valid metadata grant rejected: %v", status)
	}
	other := executorFixture()
	card.Executor = &other
	if _, status, _ := manager.LoadOrReconstructSessionDetail(t.Context(), get, session.ID, 1, card); status != SessionLoadFailed {
		t.Fatalf("substituted reference accepted: %v", status)
	}
}

func TestBoundReconstructionRollsBackWhenSecondGrantFails(t *testing.T) {
	namespace := executorFixture()
	card := NewRecipeCard(1, "profile", 3, "", TranscodeOpts{SessionID: "transport", Executor: &namespace})
	sessions := NewSessionManager(1, 1)
	manager := NewTranscodeManager()
	manager.Sessions = sessions
	manager.ResolveExecutorRecipe = func(context.Context, string, ExecutorNamespaceV3) (*RecipeCard, error) {
		return &card, nil
	}
	provider := executorGrantTestProvider(nil)
	calls := 0
	revoked := errors.New("attempt draining")
	manager.ExecuteGrants = func(ctx context.Context, transport string, ns ExecutorNamespaceV3, purpose AttemptGrantPurposeV3) (*RuntimeGrantV3, error) {
		calls++
		if calls == 2 {
			if _, err := sessions.GetSession(card.SessionID); err != nil {
				t.Fatalf("first grant did not create provisional session: %v", err)
			}
			return nil, revoked
		}
		return provider(ctx, transport, ns, purpose)
	}
	result := manager.doLoadOrReconstructTranscode(t.Context(), sessions.GetSession, card.SessionID, 1, -1, &card)
	if result.status != SessionLoadFailed || !errors.Is(result.err, revoked) || calls != 2 {
		t.Fatalf("load result=%+v, grant calls=%d", result, calls)
	}
	if _, err := sessions.GetSession(card.SessionID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("failed reconstruction retained provisional admission: %v", err)
	}
}

func TestCompleteTranscodeLoadRejectsReplacedExecutorMetadata(t *testing.T) {
	first, second := executorFixture(), executorFixture()
	session := &Session{ID: "transport", Executor: &first}
	replacement := &Session{ID: session.ID, Executor: &second}
	runtime := &TranscodeSession{opts: TranscodeOpts{Executor: &first}}
	manager := NewTranscodeManager()
	result := manager.completeTranscodeLoad(NewSessionManager(0, 0), func(string) (*Session, error) {
		return replacement, nil
	}, session, nil, runtime)
	if result.status != SessionLoadFailed || !errors.Is(result.err, ErrExecutorNamespaceMismatch) || result.runtime != nil {
		t.Fatalf("returned mismatched replacement metadata: %+v", result)
	}
}
