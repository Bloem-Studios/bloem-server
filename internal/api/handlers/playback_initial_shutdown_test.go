package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

type initialShutdownClock struct{}

func (initialShutdownClock) Now() (time.Duration, error) { return time.Second, nil }

type initialShutdownManager struct {
	*playback.SessionManager
	entered chan struct{}
	release chan struct{}
}

func (m *initialShutdownManager) DiscardInitialSession(ctx context.Context, _ playback.InitialActivationBindingV3) error {
	close(m.entered)
	select {
	case <-m.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestInitialPlaybackShutdownJoinsLateOwnerCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	flow := &InitialPlaybackFlowV3{Context: ctx}
	flow.startShutdownJoin()
	if !flow.beginWork() {
		t.Fatal("start was refused")
	}
	manager := &initialShutdownManager{SessionManager: playback.NewSessionManager(0, 0), entered: make(chan struct{}), release: make(chan struct{})}
	defer close(manager.release)
	h := NewPlaybackHandler(manager)
	h.initialFlow = flow
	a := playback.AttemptAuthorityV3{PlaybackAttemptID: uuid.NewString(), Incarnation: uuid.NewString(), OwnerID: uuid.NewString(), Epoch: 1, State: playback.AttemptActiveV3}
	p := playback.RuntimeGrantPolicyV3{MaxDuration: 10 * time.Second, SafetyMargin: time.Second, RenewBefore: 3 * time.Second, PollInterval: time.Millisecond}
	owner, err := playback.AcquireRuntimeOwnerLeaseV3(ctx, func(_ context.Context, a playback.AttemptAuthorityV3, d time.Duration) (playback.AttemptLeaseV3, error) {
		now := time.Now()
		a.LeaseExpiresAt = now.Add(d)
		return playback.AttemptLeaseV3{Authority: a, IssuedAt: now}, nil
	}, initialShutdownClock{}, p, a)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	cancel()
	if flow.beginWork() {
		t.Fatal("start accepted after cancellation")
	}
	// A start already in progress may discover an uncertain publication after
	// cancellation. Its cleanup still belongs to the shutdown barrier.
	stage := &playback.Session{ID: uuid.NewString()}
	h.retainInitialOwnerV3(playback.InitialActivationBindingV3{}, stage, owner, playback.AttemptRecordV3{})
	flow.work.Done()
	select {
	case <-manager.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("owner cleanup did not begin")
	}
	select {
	case <-h.InitialPlaybackShutdownDone():
		t.Fatal("shutdown ignored pending cleanup")
	default:
	}
	manager.release <- struct{}{}
	select {
	case <-h.InitialPlaybackShutdownDone():
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not join cleanup")
	}
	if _, ok := flow.owners.Load(stage.ID); ok {
		t.Fatal("owner retained after shutdown")
	}
	if _, ok := flow.pending.Load(stage.ID); ok {
		t.Fatal("pending runtime retained after shutdown")
	}
}

func TestInitialPlaybackShutdownJoinsGrantAcquisition(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	flow := &InitialPlaybackFlowV3{Context: ctx, AcquireGrant: func(ctx context.Context, _ string, _ playback.ExecutorNamespaceV3, _ playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return nil, ctx.Err()
	}}
	flow.startShutdownJoin()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = flow.AcquireGrant(t.Context(), "", playback.ExecutorNamespaceV3{}, playback.AttemptGrantServeV3)
	}()
	<-entered
	cancel()
	select {
	case <-flow.shutdownDone:
		t.Fatal("shutdown escaped acquisition")
	default:
	}
	release <- struct{}{}
	select {
	case <-flow.shutdownDone:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not join acquisition")
	}
	<-finished
	if _, err := flow.AcquireGrant(t.Context(), "", playback.ExecutorNamespaceV3{}, playback.AttemptGrantServeV3); err == nil {
		t.Fatal("grant accepted after shutdown")
	}
}

func TestInitialPlaybackShutdownCancelsAndJoinsIssuedGrant(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a := playback.AttemptAuthorityV3{PlaybackAttemptID: uuid.NewString(), Incarnation: uuid.NewString(), OwnerID: uuid.NewString(), Epoch: 1, State: playback.AttemptActiveV3}
	ns := playback.ExecutorNamespaceV3{Incarnation: a.Incarnation, Epoch: 1, ExecutorID: uuid.NewString()}
	policy := playback.RuntimeGrantPolicyV3{MaxDuration: 10 * time.Second, SafetyMargin: time.Second, RenewBefore: 3 * time.Second, PollInterval: time.Millisecond}
	flow := &InitialPlaybackFlowV3{Context: ctx, AcquireGrant: func(ctx context.Context, transport string, ns playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
		req := playback.AttemptGrantRequestV3{Executor: ns, SessionID: uuid.NewString(), PlanID: uuid.NewString(), TransportID: transport, Purpose: purpose, Duration: 10 * time.Second}
		return playback.AcquireRuntimeGrantV3(ctx, func(_ context.Context, a playback.AttemptAuthorityV3, r playback.AttemptGrantRequestV3) (playback.AttemptGrantV3, error) {
			now := time.Now()
			a.LeaseExpiresAt = now.Add(r.Duration)
			return playback.AttemptGrantV3{Authority: a, Request: r, IssuedAt: now, NotAfter: now.Add(r.Duration)}, nil
		}, initialShutdownClock{}, policy, a, req)
	}}
	flow.startShutdownJoin()
	grant, err := flow.AcquireGrant(t.Context(), uuid.NewString(), ns, playback.AttemptGrantServeV3)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	cancel()
	select {
	case <-flow.shutdownDone:
	case <-time.After(3 * time.Second):
		t.Fatal("issued grant held shutdown")
	}
	select {
	case <-grant.Done():
	default:
		t.Fatal("shutdown did not join grant supervisors")
	}
	if grant.Check() == nil {
		t.Fatal("shutdown grant retained authority")
	}
}

func TestInitialPlaybackShutdownJoinsTransferCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	flow := &InitialPlaybackFlowV3{Context: ctx, OpenOutputTransfer: func(context.Context, string, playback.ExecutorNamespaceV3) (string, func(), error) {
		return "permit", func() { calls++; close(entered); <-release }, nil
	}}
	flow.startShutdownJoin()
	_, cleanup, err := flow.OpenOutputTransfer(t.Context(), "transport", playback.ExecutorNamespaceV3{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	go cleanup()
	<-entered
	select {
	case <-flow.shutdownDone:
		t.Fatal("shutdown escaped pending transfer cleanup")
	default:
	}
	close(release)
	select {
	case <-flow.shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join transfer cleanup")
	}
	cleanup()
	if calls != 1 {
		t.Fatal("transfer cleanup repeated")
	}
	if _, _, err := flow.OpenOutputTransfer(t.Context(), "transport", playback.ExecutorNamespaceV3{}); err == nil {
		t.Fatal("shutdown opened transfer permit")
	}
}

func TestInitialPlaybackShutdownJoinsAuxiliaryAcquisition(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	flow := &InitialPlaybackFlowV3{Context: ctx, AcquireAuxiliary: func(ctx context.Context, _ string, _ playback.ExecutorNamespaceV3, _ string) (*playback.RuntimeGrantV3, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return nil, ctx.Err()
	}}
	flow.startShutdownJoin()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = flow.AcquireAuxiliary(t.Context(), "", playback.ExecutorNamespaceV3{}, "signed-permit")
	}()
	<-entered
	cancel()
	select {
	case <-flow.shutdownDone:
		t.Fatal("shutdown escaped acquisition")
	default:
	}
	release <- struct{}{}
	select {
	case <-flow.shutdownDone:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not join acquisition")
	}
	<-finished
	if _, err := flow.AcquireAuxiliary(t.Context(), "", playback.ExecutorNamespaceV3{}, "signed-permit"); err == nil {
		t.Fatal("grant accepted after shutdown")
	}
}

func TestInitialPlaybackShutdownCancelsAndJoinsAuxiliaryGrant(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a := playback.AttemptAuthorityV3{PlaybackAttemptID: uuid.NewString(), Incarnation: uuid.NewString(), OwnerID: uuid.NewString(), Epoch: 1, State: playback.AttemptActiveV3}
	ns := playback.ExecutorNamespaceV3{Incarnation: a.Incarnation, Epoch: 1, ExecutorID: uuid.NewString()}
	policy := playback.RuntimeGrantPolicyV3{MaxDuration: 10 * time.Second, SafetyMargin: time.Second, RenewBefore: 3 * time.Second, PollInterval: time.Millisecond}
	flow := &InitialPlaybackFlowV3{Context: ctx, AcquireAuxiliary: func(ctx context.Context, transport string, ns playback.ExecutorNamespaceV3, _ string) (*playback.RuntimeGrantV3, error) {
		req := playback.AttemptGrantRequestV3{Executor: ns, SessionID: uuid.NewString(), PlanID: uuid.NewString(), TransportID: transport, Purpose: playback.AttemptGrantAuxiliaryV3, AuxiliaryTransferID: uuid.NewString(), EgressNodeID: 2, Duration: 10 * time.Second}
		return playback.AcquireRuntimeGrantV3(ctx, func(_ context.Context, a playback.AttemptAuthorityV3, r playback.AttemptGrantRequestV3) (playback.AttemptGrantV3, error) {
			now := time.Now()
			a.LeaseExpiresAt = now.Add(r.Duration)
			return playback.AttemptGrantV3{Authority: a, Request: r, IssuedAt: now, NotAfter: now.Add(r.Duration)}, nil
		}, initialShutdownClock{}, policy, a, req)
	}}
	flow.startShutdownJoin()
	grant, err := flow.AcquireAuxiliary(t.Context(), uuid.NewString(), ns, "signed-permit")
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	cancel()
	select {
	case <-flow.shutdownDone:
	case <-time.After(3 * time.Second):
		t.Fatal("issued grant held shutdown")
	}
	select {
	case <-grant.Done():
	default:
		t.Fatal("shutdown did not join grant supervisors")
	}
	if grant.Check() == nil {
		t.Fatal("shutdown grant retained authority")
	}
}
