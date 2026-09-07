package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// TestAdminTerminateBoundSessionRevokesThenNotifies drives the v2 terminate
// against a real activated initial-flow session on the owned database:
// authority is revoked durably first (stream URL refused, progress refused,
// attempt row stopped), the client is notified only when a lane is open, and
// a repeat converges.
func TestAdminTerminateBoundSessionRevokesThenNotifies(t *testing.T) {
	for _, lane := range []string{"offline", "online"} {
		t.Run(lane, func(t *testing.T) {
			f := newInitialHTTPFixture(t)
			hub := playback.NewRealtimeHub()
			tracker := playback.NewCommandTracker()
			t.Cleanup(tracker.Close)
			f.handler.RealtimeHub = hub
			f.handler.CommandTracker = tracker
			f.handler.CommandDispatcher = playback.NewCommandDispatcher(f.manager, hub, tracker)
			control := NewAdminPlaybackControlHandler(f.handler)
			if !control.AdminTerminateAvailable() {
				t.Fatal("durable revocation seam not wired")
			}

			status, data := f.call(t, http.MethodPost, "/start", f.request)
			if status != http.StatusCreated {
				t.Fatalf("start %d: %s", status, data)
			}
			var start playback.DecisionResponseV3
			if err := json.Unmarshal(data, &start); err != nil || start.PlaybackPlan == nil {
				t.Fatalf("plan: %v %s", err, data)
			}
			sessionID := start.PlaybackPlan.SessionID
			streamURL := start.PlaybackPlan.Stream.URL
			if status, _ := f.call(t, http.MethodGet, streamURL, nil); status != http.StatusOK && status != http.StatusPartialContent {
				t.Fatalf("stream before terminate: %d", status)
			}
			if status, body := f.call(t, http.MethodPost, "/playback/"+sessionID+"/progress", map[string]any{"sequence": 1, "position": 12}); status != http.StatusOK {
				t.Fatalf("progress before terminate: %d %s", status, body)
			}

			var conn *adminPlaybackControlTestConn
			if lane == "online" {
				conn = &adminPlaybackControlTestConn{}
				registration := hub.Register(sessionID, conn)
				if registration == nil {
					t.Fatal("register lane")
				}
				t.Cleanup(func() { hub.Unregister(registration) })
			}

			view, err := control.Terminate(t.Context(), AdminTerminateInput{SessionID: sessionID, ActorID: 2, Reason: "policy"})
			if err != nil {
				t.Fatal(err)
			}
			if !view.AuthorityRevoked || view.AlreadyRevoked {
				t.Fatalf("view = %+v", view)
			}
			if lane == "online" {
				if !view.ClientNotified || view.Delivery != AdminTerminateDeliveryDispatched || len(conn.messages) != 1 {
					t.Fatalf("online view = %+v messages=%d", view, len(conn.messages))
				}
				env, ok := conn.messages[0].(playback.CommandEnvelope)
				if !ok || env.Name != playback.CommandTerminate || env.CommandID != view.CommandID || env.Reason != "policy" || env.IssuedBy == nil || env.IssuedBy.Kind != "admin" {
					t.Fatalf("envelope = %#v", conn.messages[0])
				}
			} else if view.ClientNotified || view.Delivery != AdminTerminateDeliveryUnavailable {
				t.Fatalf("offline view = %+v", view)
			}

			// Durable state: the attempt row is stopped under the administrator
			// stop identity, and the session is gone from the manager.
			var phase, stopID, controlState string
			if err := f.pool.QueryRow(t.Context(), `SELECT control_activation->>'phase', control_activation->>'stop_id', control_state FROM playback_v3_attempts WHERE session_id=$1::uuid`, sessionID).Scan(&phase, &stopID, &controlState); err != nil {
				t.Fatal(err)
			}
			// Revocation is durable at the drain boundary: the row is marked
			// under the administrator stop identity and no longer issues grants;
			// the terminal receipt follows once outstanding serve grants expire.
			if stopID != adminTerminateStopID(sessionID) || !((phase == string(playback.InitialActivationStoppingV3) && controlState == "draining" && view.DurableState == AdminTerminateDurableDraining) || (phase == string(playback.InitialActivationStoppedV3) && controlState == "stopped" && view.DurableState == AdminTerminateDurableStopped)) {
				t.Fatalf("durable state phase=%s stop_id=%s control_state=%s view=%+v", phase, stopID, controlState, view)
			}
			if _, err := f.manager.GetSession(sessionID); !errors.Is(err, playback.ErrSessionNotFound) {
				t.Fatalf("session survived terminate: %v", err)
			}

			// Tokens and progress are refused after revocation.
			if status, body := f.call(t, http.MethodGet, streamURL, nil); status < 400 {
				t.Fatalf("stream served after terminate: %d %s", status, body)
			}
			if status, body := f.call(t, http.MethodPost, "/playback/"+sessionID+"/progress", map[string]any{"sequence": 2, "position": 50}); status < 400 {
				t.Fatalf("progress accepted after terminate: %d %s", status, body)
			}
			if status, body := f.call(t, http.MethodPost, "/start", f.request); status == http.StatusCreated {
				t.Fatalf("attempt replay re-activated after terminate: %d %s", status, body)
			}

			// A repeat converges: revoked stays true, nothing is re-dispatched,
			// and once the drain has elapsed the terminal receipt is committed.
			deadline := time.Now().Add(5 * time.Second)
			for {
				var drained bool
				if err := f.pool.QueryRow(t.Context(), `SELECT control_drain_not_before <= clock_timestamp() FROM playback_v3_attempts WHERE session_id=$1::uuid`, sessionID).Scan(&drained); err != nil {
					t.Fatal(err)
				}
				if drained {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("outstanding grants never drained")
				}
				time.Sleep(20 * time.Millisecond)
			}
			again, err := control.Terminate(t.Context(), AdminTerminateInput{SessionID: sessionID, ActorID: 2})
			if err != nil || !again.AuthorityRevoked || !again.AlreadyRevoked || again.ClientNotified || again.Delivery != AdminTerminateDeliveryNone || again.DurableState != AdminTerminateDurableStopped {
				t.Fatalf("repeat = %+v, %v", again, err)
			}
			if err := f.pool.QueryRow(t.Context(), `SELECT control_activation->>'phase', control_activation->>'stop_id', control_state FROM playback_v3_attempts WHERE session_id=$1::uuid`, sessionID).Scan(&phase, &stopID, &controlState); err != nil {
				t.Fatal(err)
			}
			if phase != string(playback.InitialActivationStoppedV3) || stopID != adminTerminateStopID(sessionID) || controlState != "stopped" {
				t.Fatalf("after repeat phase=%s stop_id=%s control_state=%s", phase, stopID, controlState)
			}
			if conn != nil && len(conn.messages) != 1 {
				t.Fatalf("repeat dispatched: %d messages", len(conn.messages))
			}
			// A third call is the same terminal answer.
			third, err := control.Terminate(t.Context(), AdminTerminateInput{SessionID: sessionID, ActorID: 2})
			if err != nil || third != again {
				t.Fatalf("third = %+v, %v", third, err)
			}
		})
	}
}

// TestAdminTerminateBridgeSessionAndErrors covers a bridge-started session
// (no durable binding), the unknown-session 404, and the unavailable seam.
func TestAdminTerminateBridgeSessionAndErrors(t *testing.T) {
	control, sessionMgr, hub, session := newAdminPlaybackControlTestHandler(t)
	if control.AdminTerminateAvailable() {
		t.Fatal("bridge fixture must not advertise the durable seam")
	}
	if _, err := control.Terminate(context.Background(), AdminTerminateInput{SessionID: session.ID, ActorID: 2}); !errors.Is(err, ErrAdminTerminateUnavailable) {
		t.Fatalf("unwired err = %v", err)
	}

	// Wire only the seam markers: a bridge session never touches the store.
	control.playback.initialFlow = &InitialPlaybackFlowV3{Control: terminateNoStore{}, Sources: terminateNoSources{}}
	if !control.AdminTerminateAvailable() {
		t.Fatal("seam should be available")
	}
	if _, err := control.Terminate(context.Background(), AdminTerminateInput{SessionID: "missing", ActorID: 2}); !errors.Is(err, playback.ErrSessionNotFound) {
		t.Fatalf("missing err = %v", err)
	}
	if _, err := control.Terminate(context.Background(), AdminTerminateInput{SessionID: session.ID, ActorID: 0}); !errors.Is(err, ErrAdminPlaybackCommandInvalid) {
		t.Fatalf("no actor err = %v", err)
	}

	conn := &adminPlaybackControlTestConn{}
	registration := hub.Register(session.ID, conn)
	if registration == nil {
		t.Fatal("register lane")
	}
	defer hub.Unregister(registration)
	view, err := control.Terminate(context.Background(), AdminTerminateInput{SessionID: session.ID, ActorID: 2})
	if err != nil || !view.AuthorityRevoked || view.AlreadyRevoked || !view.ClientNotified || view.Delivery != AdminTerminateDeliveryDispatched || len(conn.messages) != 1 {
		t.Fatalf("bridge view = %+v %v messages=%d", view, err, len(conn.messages))
	}
	if _, err := sessionMgr.GetSession(session.ID); !errors.Is(err, playback.ErrSessionNotFound) {
		t.Fatal("bridge session survived terminate")
	}
	// A bridge session leaves no durable row: once gone it is unknown, exactly
	// as the bridge answers, and nothing is re-dispatched.
	if _, err := control.Terminate(context.Background(), AdminTerminateInput{SessionID: session.ID, ActorID: 2}); !errors.Is(err, playback.ErrSessionNotFound) {
		t.Fatalf("repeat err = %v", err)
	}
	if len(conn.messages) != 1 {
		t.Fatal("repeat dispatched a second terminate")
	}
	waitForPlaybackSessionMissing(t, sessionMgr, session.ID)
}

// terminateNoStore marks the seam as wired for a bridge-session test; any
// call on it is a test failure because a bridge session must never reach it.
type terminateNoStore struct{ InitialPlaybackControlV3 }

func (terminateNoStore) SessionActivationPhase(context.Context, string) (playback.SessionActivationPhaseV3, bool, error) {
	return playback.SessionActivationPhaseV3{}, false, nil
}

type terminateNoSources struct{}

func (terminateNoSources) OpenPlaybackSink(context.Context, userstore.PlaybackSourceRef) (userstore.PlaybackSinkHandle, error) {
	panic("unexpected source use")
}
