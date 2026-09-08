package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

// The serialized activation outlives each handler, while pending publication
// state does not. Unimplemented embedded methods panic if recovery tries to
// reserve, grant, restore a source, or consult a live owner.
type abortedStartStore struct {
	InitialPlaybackControlV3
	snapshot  []byte
	digest    string
	accountID int
	profileID string
	attemptID string
}

func (s *abortedStartStore) LookupInitialRecovery(_ context.Context, q playback.InitialRecoveryLookupV3) (playback.InitialActivationV3, error) {
	if q.AccountID != s.accountID || q.ProfileID != s.profileID || q.AttemptID != s.attemptID {
		return playback.InitialActivationV3{}, playback.ErrSessionNotFound
	}
	if q.RequestDigest != s.digest {
		return playback.InitialActivationV3{}, playback.ErrIdempotencyKeyReusedV3
	}
	var state playback.InitialActivationV3
	err := json.Unmarshal(s.snapshot, &state)
	return state, err
}
func (s *abortedStartStore) BeginOwnerLossRecovery(context.Context, playback.InitialActivationBindingV3) (playback.InitialActivationV3, error) {
	return playback.InitialActivationV3{}, playback.ErrInitialActivationConflictV3
}

func abortedStartFixture(t *testing.T) (*abortedStartStore, PlaybackCaller, playback.StartRequestV3, context.Context) {
	t.Helper()
	req := v3HandlerStartRequest()
	req.ProfileID = uuid.NewString()
	req.PlaybackAttemptID = uuid.NewString()
	caller := PlaybackCaller{UserID: 7, ProfileID: req.ProfileID, InstallationID: uuid.NewString(), DeviceID: "original-device"}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	b := playback.InitialActivationBindingV3{Source: userstore.PlaybackSourceRef{Backend: "postgres", AccountID: caller.UserID, SourceID: uuid.NewString(), SelectionGeneration: 1}, IntentID: uuid.NewString(), AdmissionID: uuid.NewString(), Scope: userstore.PlaybackProgressScope{ProfileID: req.ProfileID, SessionID: uuid.NewString(), MediaItemID: "item"}, Fence: userstore.PlaybackProgressFence{AttemptID: req.PlaybackAttemptID, Incarnation: uuid.NewString(), OwnerID: uuid.NewString(), Epoch: 1}}
	abortID := uuid.NewString()
	receipt := userstore.PlaybackProgressState{Version: 1, Scope: b.Scope, Fence: b.Fence}
	change, err := userstore.PreparePlaybackStop(&receipt, userstore.StopPlaybackProgressRequest{Scope: b.Scope, Fence: b.Fence, StopID: abortID})
	if err != nil {
		t.Fatal(err)
	}
	receipt = change.Result.State
	state := playback.InitialActivationV3{Binding: b, Phase: playback.InitialActivationAbortedV3, AbortID: abortID, Terminal: &receipt}
	snapshot, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	store := &abortedStartStore{snapshot: snapshot, digest: newPlaybackStartRequestDigestsV3(body, caller.DeviceID).current, accountID: caller.UserID, profileID: caller.ProfileID, attemptID: req.PlaybackAttemptID}
	ctx := apimw.SetProfileID(apimw.SetClaims(t.Context(), &auth.Claims{UserID: caller.UserID}), caller.ProfileID)
	return store, caller, req, ctx
}

func TestOrdinaryInitialAbortExactStartReplayAfterRestart(t *testing.T) {
	store, caller, req, ctx := abortedStartFixture(t)
	original := string(store.snapshot)
	var first any
	for i := range 2 {
		h := &PlaybackHandler{initialFlow: &InitialPlaybackFlowV3{InstallationID: caller.InstallationID, Control: store}}
		status, body, handled, err := h.RecoverInitialPlaybackStart(ctx, caller, req)
		if err != nil || !handled || status != http.StatusCreated {
			t.Fatalf("boot%d status=%d handled=%v err=%v", i, status, handled, err)
		}
		decision, ok := body.(playback.DecisionResponseV3)
		if !ok || decision.Terminal == nil || decision.Terminal.Reason != "playback_start_aborted" || decision.Terminal.Retryable || decision.SessionID != "" || decision.PlaybackPlan != nil {
			t.Fatalf("not a terminal refusal: %+v", body)
		}
		if i == 0 {
			first = body
		} else if !reflect.DeepEqual(first, body) {
			t.Fatal("replay changed")
		}
	}
	if string(store.snapshot) != original {
		t.Fatal("durable receipt changed")
	}
}

func TestOrdinaryInitialAbortRejectsChangedIdentity(t *testing.T) {
	for _, kind := range []string{"body", "device", "account", "profile", "installation"} {
		t.Run(kind, func(t *testing.T) {
			store, caller, req, ctx := abortedStartFixture(t)
			h := &PlaybackHandler{initialFlow: &InitialPlaybackFlowV3{InstallationID: caller.InstallationID, Control: store}}
			switch kind {
			case "body":
				req.FileID++
			case "device":
				caller.DeviceID = "another-device"
			case "account":
				caller.UserID++
			case "profile":
				caller.ProfileID = uuid.NewString()
			case "installation":
				caller.InstallationID = uuid.NewString()
			}
			status, body, _, err := h.RecoverInitialPlaybackStart(ctx, caller, req)
			if err == nil || status == 201 || body != nil {
				t.Fatalf("changed %s accepted: %d %+v %v", kind, status, body, err)
			}
		})
	}
}

func TestOrdinaryInitialAbortRequiresTerminalReceipt(t *testing.T) {
	for _, kind := range []string{"missing", "foreign_fence", "foreign_stop", "accepted_sample"} {
		t.Run(kind, func(t *testing.T) {
			store, caller, req, ctx := abortedStartFixture(t)
			var state playback.InitialActivationV3
			if err := json.Unmarshal(store.snapshot, &state); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing":
				state.Terminal = nil
			case "foreign_fence":
				state.Terminal.Fence.Epoch++
			case "foreign_stop":
				state.Terminal.Stop.StopID = uuid.NewString()
			case "accepted_sample":
				state.Terminal.Last = &userstore.PlaybackProgressReceipt{Fence: state.Binding.Fence}
			}
			store.snapshot, _ = json.Marshal(state)
			h := &PlaybackHandler{initialFlow: &InitialPlaybackFlowV3{InstallationID: caller.InstallationID, Control: store}}
			status, _, handled, err := h.RecoverInitialPlaybackStart(ctx, caller, req)
			if !handled || err == nil || status == 201 {
				t.Fatalf("unproven %s terminal: %d %v", kind, status, err)
			}
		})
	}
}

var _ playback.OwnerLossRecoveryStoreV3 = (*abortedStartStore)(nil)

// These doubles exercise reconciliation orchestration. The source receipt is
// produced by the real sink reducer; storage deadline enforcement is an explicit
// boundary, not a test clock pretending the database has drained.
type abortRecoverySink struct {
	userstore.PlaybackProgressSink
	source  userstore.PlaybackSourceRef
	receipt userstore.PlaybackProgressState
	stops   int
}

func (s *abortRecoverySink) Source() userstore.PlaybackSourceRef { return s.source }
func (s *abortRecoverySink) Close() error                        { return nil }
func (s *abortRecoverySink) ReadPlaybackProgress(context.Context, userstore.PlaybackProgressScope) (userstore.PlaybackProgressState, error) {
	return s.receipt, nil
}
func (s *abortRecoverySink) StopPlaybackProgress(_ context.Context, q userstore.StopPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
	s.stops++
	change, err := userstore.PreparePlaybackStop(&s.receipt, q)
	if err != nil {
		return userstore.PlaybackProgressResult{}, err
	}
	s.receipt = change.Result.State
	return userstore.PlaybackProgressResult{}, errors.New("lost reply after commit")
}

type abortRecoverySource struct{ sink *abortRecoverySink }

func (s abortRecoverySource) OpenPlaybackSink(_ context.Context, ref userstore.PlaybackSourceRef) (userstore.PlaybackSinkHandle, error) {
	if ref != s.sink.source {
		return nil, userstore.ErrPlaybackSourceMismatch
	}
	return s.sink, nil
}

type drainingAbortStore struct {
	*abortedStartStore
	drained   bool
	completed int
}

func (s *drainingAbortStore) CompleteInitialAbort(_ context.Context, b playback.InitialActivationBindingV3, id string, observed playback.InitialActivationReceiptV3) (playback.InitialActivationV3, error) {
	s.completed++
	if !s.drained {
		return playback.InitialActivationV3{}, playback.ErrPlaybackRecoveryDrainingV3
	}
	var state playback.InitialActivationV3
	if err := json.Unmarshal(s.snapshot, &state); err != nil {
		return state, err
	}
	if state.Binding != b || state.AbortID != id {
		return state, playback.ErrInitialActivationConflictV3
	}
	receipt, err := observed.StateFor(b)
	if err != nil {
		return state, err
	}
	if err = playback.ValidateInitialTerminalV3(b, receipt); err != nil {
		return state, err
	}
	state.Phase = playback.InitialActivationAbortedV3
	state.Terminal = &receipt
	s.snapshot, err = json.Marshal(state)
	return state, err
}
func TestOrdinaryInitialAbortDrainAndLostStopReply(t *testing.T) {
	base, caller, req, ctx := abortedStartFixture(t)
	var state playback.InitialActivationV3
	if err := json.Unmarshal(base.snapshot, &state); err != nil {
		t.Fatal(err)
	}
	receipt := *state.Terminal
	receipt.Stop = nil
	state.Terminal = nil
	state.Phase = playback.InitialActivationAbortingV3
	base.snapshot, _ = json.Marshal(state)
	sink := &abortRecoverySink{source: state.Binding.Source, receipt: receipt}
	store := &drainingAbortStore{abortedStartStore: base}
	for _, drained := range []bool{false, true, true} {
		store.drained = drained
		h := &PlaybackHandler{initialFlow: &InitialPlaybackFlowV3{InstallationID: caller.InstallationID, Control: store, Sources: abortRecoverySource{sink}}}
		status, _, handled, err := h.RecoverInitialPlaybackStart(ctx, caller, req)
		if !handled {
			t.Fatal("abort not handled")
		}
		if !drained && (err == nil || status == 201 || status == 202) {
			t.Fatal("draining abort published terminal/pending success")
		}
		if drained && (err != nil || status != 201) {
			t.Fatalf("drained: %d %v", status, err)
		}
	}
	if sink.stops != 1 || store.completed != 2 {
		t.Fatalf("stop=%d complete=%d", sink.stops, store.completed)
	}
	if sink.receipt.Last != nil || sink.receipt.Stop.Accepted != nil {
		t.Fatal("invented Last")
	}
}
func TestOrdinaryInitialAbortNeverRestoresChangedSource(t *testing.T) {
	for _, kind := range []string{"source", "fence", "absent"} {
		t.Run(kind, func(t *testing.T) {
			base, caller, req, ctx := abortedStartFixture(t)
			var state playback.InitialActivationV3
			_ = json.Unmarshal(base.snapshot, &state)
			sink := &abortRecoverySink{source: state.Binding.Source, receipt: *state.Terminal}
			state.Terminal = nil
			state.Phase = playback.InitialActivationAbortingV3
			base.snapshot, _ = json.Marshal(state)
			switch kind {
			case "source":
				sink.source.SelectionGeneration++
			case "fence":
				sink.receipt.Fence.Epoch++
			case "absent":
				sink.receipt = userstore.PlaybackProgressState{}
			}
			store := &drainingAbortStore{abortedStartStore: base, drained: true}
			h := &PlaybackHandler{initialFlow: &InitialPlaybackFlowV3{InstallationID: caller.InstallationID, Control: store, Sources: abortRecoverySource{sink}}}
			status, _, handled, err := h.RecoverInitialPlaybackStart(ctx, caller, req)
			if !handled || err == nil || status == 201 || sink.stops != 0 || store.completed != 0 {
				t.Fatalf("changed authority accepted: %d %v", status, err)
			}
		})
	}
}
