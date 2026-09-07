package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

type initialSuccessorFaultControl struct {
	InitialPlaybackControlV3
	BoundReplanStoreV3
	playback.BoundRouteReplacementStoreV3
	after func(string, playback.InitialActivationBindingV3) error
}

type initialUncertainCancellationControl struct {
	InitialPlaybackControlV3
	BoundReplanStoreV3
	playback.BoundRouteReplacementStoreV3
	denyConfirmation bool
}

func (c *initialUncertainCancellationControl) ConfirmBoundRouteCancellation(ctx context.Context, binding playback.InitialActivationBindingV3, key playback.RouteReplacementKeyV3) (playback.RouteReplacementV3, error) {
	if c.denyConfirmation {
		return playback.RouteReplacementV3{}, errors.New("cancellation observation unavailable")
	}
	return c.BoundRouteReplacementStoreV3.ConfirmBoundRouteCancellation(ctx, binding, key)
}

func (c *initialSuccessorFaultControl) AcknowledgeBoundRouteReplacement(ctx context.Context, b playback.InitialActivationBindingV3, k playback.RouteReplacementKeyV3, ready playback.RouteReplacementReadyReceiptV3) (playback.RouteReplacementV3, error) {
	doc, err := c.BoundRouteReplacementStoreV3.AcknowledgeBoundRouteReplacement(ctx, b, k, ready)
	if err == nil {
		err = c.after("ready", b)
	}
	return doc, err
}

func (c *initialSuccessorFaultControl) BeginBoundRouteRetirement(ctx context.Context, b playback.InitialActivationBindingV3, k playback.RouteReplacementKeyV3) (playback.RouteReplacementV3, error) {
	doc, err := c.BoundRouteReplacementStoreV3.BeginBoundRouteRetirement(ctx, b, k)
	if err == nil {
		err = c.after("retiring", b)
	}
	return doc, err
}

func (c *initialSuccessorFaultControl) CompleteBoundRouteReplacement(ctx context.Context, b playback.InitialActivationBindingV3, k playback.RouteReplacementKeyV3) (playback.RouteReplacementV3, error) {
	doc, err := c.BoundRouteReplacementStoreV3.CompleteBoundRouteReplacement(ctx, b, k)
	if err == nil {
		err = c.after("committed", b)
	}
	return doc, err
}

// Inject failure after the real durable write, before its response reaches the
// orchestrator. An exact retry must retain the namespace and project once.
func TestInitialPlaybackSuccessorLostDurableResponse(t *testing.T) {
	for _, phase := range []string{"ready", "retiring", "committed"} {
		t.Run(phase, func(t *testing.T) {
			f := newInitialHTTPFixture(t)
			ctx := initialContextV3(t, f)
			status, data := f.call(t, http.MethodPost, "/start", f.request)
			var started playback.DecisionResponseV3
			if status != http.StatusCreated || json.Unmarshal(data, &started) != nil || started.PlaybackPlan == nil {
				t.Fatalf("start: %d %s", status, data)
			}
			command := replanCommandV3(t, initialReplanRequestV3(started, f.request, "successor-lost-"+phase, 6))
			lostCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			control := &initialSuccessorFaultControl{InitialPlaybackControlV3: f.flow.Control, BoundReplanStoreV3: f.flow.Control.(BoundReplanStoreV3), BoundRouteReplacementStoreV3: f.flow.Control.(playback.BoundRouteReplacementStoreV3)}
			injected := false
			control.after = func(observed string, _ playback.InitialActivationBindingV3) error {
				if observed == phase && !injected {
					injected = true
					cancel()
					return errors.New("lost durable response")
				}
				return nil
			}
			f.flow.Control = control
			_, err := f.handler.ReplanInitialPlayback(lostCtx, initialCallerV3(f), started.SessionID, command)
			requirePlaybackOperationError(t, err, http.StatusServiceUnavailable, "unavailable")
			if !injected {
				t.Fatal("fault did not reach durable phase")
			}
			value, ok := f.flow.pending.Load(started.SessionID)
			if !ok {
				t.Fatal("lost response discarded retained owner")
			}
			pending := value.(*initialPendingPublicationV3)
			pending.mu.Lock()
			executor := pending.successor.document.Route.Executor
			pending.mu.Unlock()
			decision, err := f.handler.ReplanInitialPlayback(ctx, initialCallerV3(f), started.SessionID, command)
			if err != nil || decision.PlaybackPlan == nil {
				t.Fatalf("exact recovery: %+v %v", decision, err)
			}
			session, err := f.manager.GetSession(started.SessionID)
			if err != nil || session.Executor == nil || *session.Executor != executor || session.Position != 6 {
				t.Fatalf("recovery changed candidate: %+v %v", session, err)
			}
			if status, body := f.call(t, http.MethodGet, decision.PlaybackPlan.Stream.URL, nil); status != http.StatusOK || len(body) == 0 {
				t.Fatalf("recovered bytes: %d bytes=%d", status, len(body))
			}
			if status, _ := f.call(t, http.MethodGet, started.PlaybackPlan.Stream.URL, nil); status == http.StatusOK {
				t.Fatal("retired predecessor serves after recovery")
			}
		})
	}
}

func TestInitialPlaybackSuccessorStopDuringRetirement(t *testing.T) {
	f := newInitialHTTPFixture(t)
	ctx := initialContextV3(t, f)
	status, data := f.call(t, http.MethodPost, "/start", f.request)
	var started playback.DecisionResponseV3
	if status != http.StatusCreated || json.Unmarshal(data, &started) != nil || started.PlaybackPlan == nil {
		t.Fatalf("start: %d %s", status, data)
	}
	control := &initialSuccessorFaultControl{InitialPlaybackControlV3: f.flow.Control, BoundReplanStoreV3: f.flow.Control.(BoundReplanStoreV3), BoundRouteReplacementStoreV3: f.flow.Control.(playback.BoundRouteReplacementStoreV3)}
	stopID := uuid.NewString()
	stopped := false
	control.after = func(phase string, binding playback.InitialActivationBindingV3) error {
		if phase == "retiring" {
			if _, err := control.BeginBoundStop(ctx, binding, stopID); err != nil {
				t.Fatal(err)
			}
			stopped = true
			return errors.New("stop won during retirement")
		}
		return nil
	}
	f.flow.Control = control
	command := replanCommandV3(t, initialReplanRequestV3(started, f.request, "successor-stop-retiring", 6))
	_, err := f.handler.ReplanInitialPlayback(ctx, initialCallerV3(f), started.SessionID, command)
	requirePlaybackOperationError(t, err, http.StatusServiceUnavailable, "unavailable")
	if !stopped {
		t.Fatal("stop boundary not exercised")
	}
	if _, err := f.handler.StopInitialPlayback(ctx, initialCallerV3(f), started.SessionID, PlaybackStopCommand{StopID: stopID}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.GetSession(started.SessionID); err == nil {
		t.Fatal("stop retained visible session")
	}
	if _, err := f.handler.ReplanInitialPlayback(ctx, initialCallerV3(f), started.SessionID, command); err == nil {
		t.Fatal("replay projected a stopped candidate")
	}
	if status, _ := f.call(t, http.MethodGet, started.PlaybackPlan.Stream.URL, nil); status == http.StatusOK {
		t.Fatal("stopped predecessor still serves")
	}
}

func TestInitialPlaybackSuccessorPreservesCapturedTimeline(t *testing.T) {
	f := newInitialHTTPFixture(t)
	ctx, caller, manifest := configureInitialTimelineFixture(t, f)
	started, err := f.handler.StartInitialPlayback(ctx, caller, f.request)
	if err != nil || started.ProgressTimeline == nil {
		t.Fatalf("bound start: %+v %v", started, err)
	}
	command := replanCommandV3(t, initialReplanRequestV3(started, f.request, "successor-bound-timeline", 40))
	decision, err := f.handler.ReplanInitialPlayback(ctx, caller, started.SessionID, command)
	if err != nil || decision.ProgressTimeline == nil || *decision.ProgressTimeline != *started.ProgressTimeline || decision.PlaybackPlan.Timeline.PlayerStartSeconds != 40 {
		t.Fatalf("successor changed timeline: %+v %v", decision, err)
	}
	progress, err := f.handler.ApplyInitialProgress(ctx, caller, started.SessionID, PlaybackProgressCommand{TimelineID: manifest.TimelineID, Sequence: 1, Position: 45})
	if err != nil || progress.Accepted == nil || progress.Accepted.Position != 45 || progress.Accepted.ItemPosition == nil || *progress.Accepted.ItemPosition != 645 {
		t.Fatalf("successor progress mapping: %+v %v", progress, err)
	}
	if _, err := f.handler.StopInitialPlayback(ctx, caller, started.SessionID, PlaybackStopCommand{StopID: uuid.NewString(), TimelineID: manifest.TimelineID}); err != nil {
		t.Fatal(err)
	}
}
