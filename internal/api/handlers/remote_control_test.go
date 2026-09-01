package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/remote"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// remoteTestConn is the fake session socket: it records every command frame
// the server writes.
type remoteTestConn struct {
	frames []playback.CommandEnvelope
}

func (c *remoteTestConn) WriteJSON(v any) error {
	if env, ok := v.(playback.CommandEnvelope); ok {
		c.frames = append(c.frames, env)
	}
	return nil
}

// remoteTestStore answers the household primary-profile check; every other
// store method is unused by these tests.
type remoteTestStore struct {
	userstore.UserStore
	primary bool
}

func (s remoteTestStore) GetProfile(_ context.Context, id string) (*userstore.Profile, error) {
	return &userstore.Profile{ID: id, IsPrimary: s.primary}, nil
}

type remoteTestHarness struct {
	handler  *RemoteControlHandler
	playback *PlaybackHandler
	service  *remote.Service
	store    *remote.MemoryStore
	sessions *playback.SessionManager
	hub      *playback.RealtimeHub
	session  *playback.Session
	conn     *remoteTestConn
}

func newRemoteTestHarness(t *testing.T, connect bool) *remoteTestHarness {
	t.Helper()
	sessionMgr := playback.NewSessionManager(0, 0)
	session, err := sessionMgr.StartSession(7, "profile-1", 100, playback.PlayDirect, false)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := sessionMgr.SetDeviceID(session.ID, "device-1"); err != nil {
		t.Fatalf("SetDeviceID: %v", err)
	}
	hub := playback.NewRealtimeHub()
	tracker := playback.NewCommandTracker()
	t.Cleanup(tracker.Close)
	playbackHandler := NewPlaybackHandler(sessionMgr)
	playbackHandler.RealtimeHub = hub
	playbackHandler.CommandTracker = tracker
	playbackHandler.CommandDispatcher = playback.NewCommandDispatcher(sessionMgr, hub, tracker)

	store := remote.NewMemoryStore()
	service := remote.NewService(store, NewPlaybackRemoteSender(playbackHandler), nil, remote.DefaultConfig())
	playbackHandler.RemoteObserver = service
	profiles := NewProfileHandler(testUserStoreProvider{store: remoteTestStore{primary: true}})
	handler := NewRemoteControlHandler(service, playbackHandler, profiles, testUserStoreProvider{store: remoteTestStore{primary: true}})

	h := &remoteTestHarness{handler: handler, playback: playbackHandler, service: service, store: store, sessions: sessionMgr, hub: hub, conn: &remoteTestConn{}}
	if connect {
		registration := hub.Register(session.ID, h.conn)
		if registration == nil {
			t.Fatal("register fake socket")
		}
		t.Cleanup(func() { hub.Unregister(registration) })
		hello, _ := json.Marshal(playback.HelloEnvelope{Type: playback.RealtimeMessageTypeHello, SessionID: session.ID,
			Client:       playback.HelloClientInfo{Name: "bloem-android", Version: "3.0.0"},
			Capabilities: playback.HelloCapabilities{Commands: []playback.CommandName{playback.CommandPause, playback.CommandSeek, playback.CommandStop, playback.CommandTerminate, playback.CommandDisplayMessage}}})
		if err := playbackHandler.handleRealtimeClientMessage(session.ID, hello); err != nil {
			t.Fatalf("hello: %v", err)
		}
	}
	current, err := sessionMgr.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.session = current
	return h
}

func (h *remoteTestHarness) adminRequest(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/remote/sessions/"+h.session.ID+"/commands", strings.NewReader(body))
	req = withPlaybackRouteParam(req, "session_id", h.session.ID)
	req = req.WithContext(apimw.SetClaims(req.Context(), &auth.Claims{UserID: 1, Role: "admin", TokenType: auth.TokenTypeAccess}))
	rr := httptest.NewRecorder()
	h.handler.HandleAdminSendSessionCommand(rr, req)
	return rr
}

func (h *remoteTestHarness) householdRequest(t *testing.T, userID int, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/household/sessions/"+h.session.ID+"/commands", strings.NewReader(body))
	req = withPlaybackRouteParam(req, "session_id", h.session.ID)
	ctx := apimw.SetClaims(req.Context(), &auth.Claims{UserID: userID, Role: "user", TokenType: auth.TokenTypeAccess})
	ctx = apimw.SetProfileID(ctx, "profile-primary")
	rr := httptest.NewRecorder()
	h.handler.HandleHouseholdSendSessionCommand(rr, req.WithContext(ctx))
	return rr
}

func decodeRemoteCommand(t *testing.T, rr *httptest.ResponseRecorder) remote.Command {
	t.Helper()
	var command remote.Command
	if err := json.NewDecoder(rr.Body).Decode(&command); err != nil {
		t.Fatalf("decode command: %v (%s)", err, rr.Body.String())
	}
	return command
}

func TestRemoteControlHelloPersistsDeviceCapabilities(t *testing.T) {
	h := newRemoteTestHarness(t, true)
	capability, err := h.service.DeviceCapability(context.Background(), 7, "profile-1", "device-1")
	if err != nil || capability == nil {
		t.Fatalf("capability after hello = %+v err = %v", capability, err)
	}
	if !capability.Supports(playback.CommandPause) || capability.Supports(remote.CommandReplan) {
		t.Fatalf("hello commands not persisted: %+v", capability.Commands)
	}
}

func TestRemoteControlAdminSendsAndTracksAckResultOverFakeSocket(t *testing.T) {
	h := newRemoteTestHarness(t, true)
	rr := h.adminRequest(t, `{"name":"seek","payload":{"position_ms":90000},"reason":"skip recap"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	command := decodeRemoteCommand(t, rr)
	if command.State != remote.StateSent || command.IssuedBy != "user:1" || command.TargetDeviceID != "device-1" {
		t.Fatalf("command = %+v", command)
	}
	if len(h.conn.frames) != 1 || h.conn.frames[0].CommandID != command.ID || h.conn.frames[0].Name != playback.CommandSeek || string(h.conn.frames[0].Payload) != `{"position_ms":90000}` {
		t.Fatalf("socket frames = %+v", h.conn.frames)
	}

	ack, _ := json.Marshal(playback.AckEnvelope{Type: playback.RealtimeMessageTypeAck, CommandID: command.ID, SessionID: h.session.ID, Status: playback.RealtimeAckStatusAccepted})
	if err := h.playback.handleRealtimeClientMessage(h.session.ID, ack); err != nil {
		t.Fatalf("ack: %v", err)
	}
	got, _ := h.service.Get(context.Background(), command.ID)
	if got.State != remote.StateAccepted {
		t.Fatalf("after ack = %s", got.State)
	}
	result, _ := json.Marshal(playback.ResultEnvelope{Type: playback.RealtimeMessageTypeResult, CommandID: command.ID, SessionID: h.session.ID, Status: playback.RealtimeResultStatusCompleted})
	if err := h.playback.handleRealtimeClientMessage(h.session.ID, result); err != nil {
		t.Fatalf("result: %v", err)
	}
	got, _ = h.service.Get(context.Background(), command.ID)
	if got.State != remote.StateDone {
		t.Fatalf("after result = %s", got.State)
	}

	// GET /admin/remote/commands/{id}
	req := withPlaybackRouteParam(httptest.NewRequest(http.MethodGet, "/api/v1/admin/remote/commands/"+command.ID, nil), "command_id", command.ID)
	statusRR := httptest.NewRecorder()
	h.handler.HandleGetCommand(statusRR, req)
	if statusRR.Code != http.StatusOK || decodeRemoteCommand(t, statusRR).State != remote.StateDone {
		t.Fatalf("status endpoint = %d %s", statusRR.Code, statusRR.Body.String())
	}

	// A rejected result lands as rejected with the client's error.
	rr = h.adminRequest(t, `{"name":"pause"}`)
	command = decodeRemoteCommand(t, rr)
	rejected, _ := json.Marshal(playback.ResultEnvelope{Type: playback.RealtimeMessageTypeResult, CommandID: command.ID, SessionID: h.session.ID, Status: playback.RealtimeResultStatusRejected, Error: "not_playing"})
	if err := h.playback.handleRealtimeClientMessage(h.session.ID, rejected); err != nil {
		t.Fatalf("rejected result: %v", err)
	}
	got, _ = h.service.Get(context.Background(), command.ID)
	if got.State != remote.StateRejected || got.Error != "not_playing" {
		t.Fatalf("rejected = %+v", got)
	}

	// Audit lists both.
	auditRR := httptest.NewRecorder()
	h.handler.HandleListAudit(auditRR, httptest.NewRequest(http.MethodGet, "/api/v1/admin/remote/audit?session_id="+h.session.ID, nil))
	var audit remoteAuditResponse
	if err := json.NewDecoder(auditRR.Body).Decode(&audit); err != nil || len(audit.Commands) != 2 {
		t.Fatalf("audit = %s err = %v", auditRR.Body.String(), err)
	}
}

func TestRemoteControlStatusCodes(t *testing.T) {
	h := newRemoteTestHarness(t, true)
	cases := []struct {
		body string
		code int
		err  string
	}{
		{`{"name":"set_volume","payload":{"level":50}}`, http.StatusCreated, ""}, // unsupported by device → 201 rejected_unsupported
		{`{"name":"terminate"}`, http.StatusBadRequest, "reason_required"},
		{`{"name":"seek","payload":{"position_ms":-5}}`, http.StatusBadRequest, "invalid_payload"},
		{`{"name":"play_media","payload":{}}`, http.StatusBadRequest, "unknown_command"},
		{`{"name":"reboot"}`, http.StatusBadRequest, "unknown_command"},
		{`{"name":"replan","payload":{"overrides":{"transcode":"force"}},"reason":"x"}`, http.StatusCreated, ""}, // device did not list replan
		{`not json`, http.StatusBadRequest, "bad_request"},
	}
	for _, tc := range cases {
		rr := h.adminRequest(t, tc.body)
		if rr.Code != tc.code {
			t.Fatalf("%s: status = %d body = %s", tc.body, rr.Code, rr.Body.String())
		}
		if tc.err != "" {
			var resp errorResponse
			_ = json.NewDecoder(rr.Body).Decode(&resp)
			if resp.Error != tc.err {
				t.Fatalf("%s: error = %q want %q", tc.body, resp.Error, tc.err)
			}
		} else if command := decodeRemoteCommand(t, rr); command.State != remote.StateRejectedUnsupported {
			t.Fatalf("%s: state = %s want rejected_unsupported", tc.body, command.State)
		}
	}
	if len(h.conn.frames) != 0 {
		t.Fatalf("rejected commands reached the socket: %+v", h.conn.frames)
	}

	// Unknown session → 404.
	req := withPlaybackRouteParam(httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"pause"}`)), "session_id", "missing")
	req = req.WithContext(apimw.SetClaims(req.Context(), &auth.Claims{UserID: 1, Role: "admin"}))
	rr := httptest.NewRecorder()
	h.handler.HandleAdminSendSessionCommand(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing session status = %d", rr.Code)
	}
}

func TestRemoteControlSessionNotConnectedIs409(t *testing.T) {
	h := newRemoteTestHarness(t, false)
	// The device advertised support out of band; only the socket is missing.
	if err := h.service.AdvertiseDevice(context.Background(), 7, "profile-1", "device-1", 1, []playback.CommandName{playback.CommandPause}); err != nil {
		t.Fatal(err)
	}
	rr := h.adminRequest(t, `{"name":"pause"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	var resp errorResponse
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Error != "session_not_connected" {
		t.Fatalf("error = %q", resp.Error)
	}
}

func TestRemoteControlHouseholdScopeAndReducedSet(t *testing.T) {
	h := newRemoteTestHarness(t, true)
	rr := h.householdRequest(t, 7, `{"name":"pause"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("own household pause = %d %s", rr.Code, rr.Body.String())
	}
	if command := decodeRemoteCommand(t, rr); command.IssuerKind != remote.IssuerHousehold || command.IssuedBy != "profile:profile-primary" {
		t.Fatalf("household command = %+v", command)
	}
	if rr := h.householdRequest(t, 8, `{"name":"pause"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("other household = %d %s", rr.Code, rr.Body.String())
	}
	if rr := h.householdRequest(t, 7, `{"name":"terminate","reason":"no"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("household terminate = %d %s", rr.Code, rr.Body.String())
	}
	if rr := h.householdRequest(t, 7, `{"name":"replan","payload":{"overrides":{"transcode":"force"}}}`); rr.Code != http.StatusForbidden {
		t.Fatalf("household replan = %d %s", rr.Code, rr.Body.String())
	}
	// A non-primary profile cannot control the household at all.
	h.handler.storeProv = testUserStoreProvider{store: remoteTestStore{primary: false}}
	if rr := h.householdRequest(t, 7, `{"name":"pause"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("non-primary profile = %d %s", rr.Code, rr.Body.String())
	}
}

func TestRemoteControlAdminTenantScope(t *testing.T) {
	h := newRemoteTestHarness(t, true)
	tenant := uuid.New()
	send := func(scope uuid.UUID) *httptest.ResponseRecorder {
		req := withPlaybackRouteParam(httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"pause"}`)), "session_id", h.session.ID)
		ctx := apimw.SetClaims(req.Context(), &auth.Claims{UserID: 1, Role: "admin"})
		ctx = withAdminResourceOrganization(ctx, scope)
		rr := httptest.NewRecorder()
		h.handler.HandleAdminSendSessionCommand(rr, req.WithContext(ctx))
		return rr
	}
	if rr := send(tenant); rr.Code != http.StatusForbidden {
		t.Fatalf("session outside tenant scope = %d %s", rr.Code, rr.Body.String())
	}
	if rr := send(uuid.Nil); rr.Code != http.StatusCreated {
		t.Fatalf("global scope = %d %s", rr.Code, rr.Body.String())
	}
	// Listing hides sessions outside the tenant scope too.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/remote/sessions", nil)
	rr := httptest.NewRecorder()
	h.handler.HandleListSessions(rr, req.WithContext(withAdminResourceOrganization(req.Context(), tenant)))
	var scoped remoteSessionsResponse
	_ = json.NewDecoder(rr.Body).Decode(&scoped)
	if len(scoped.Sessions) != 0 {
		t.Fatalf("scoped listing leaked %d sessions", len(scoped.Sessions))
	}
	rr = httptest.NewRecorder()
	h.handler.HandleListSessions(rr, req)
	var all remoteSessionsResponse
	if err := json.NewDecoder(rr.Body).Decode(&all); err != nil || len(all.Sessions) != 1 {
		t.Fatalf("listing = %s err = %v", rr.Body.String(), err)
	}
	row := all.Sessions[0]
	if !row.RemoteControl.Connected || !row.RemoteControl.Controllable || row.RemoteControl.DeviceID != "device-1" || len(row.RemoteControl.Commands) != 5 {
		t.Fatalf("remote_control block = %+v", row.RemoteControl)
	}
	if row.PlanSummary.PlayMethod != string(playback.PlayDirect) {
		t.Fatalf("plan summary = %+v", row.PlanSummary)
	}
}

func TestRemoteControlReplanEmitsPlanInvalidatedAndCompletesOnPlanFetch(t *testing.T) {
	h := newRemoteTestHarness(t, true)
	ctx := context.Background()
	if err := h.service.AdvertiseDevice(ctx, 7, "profile-1", "device-1", 1, []playback.CommandName{playback.CommandPause, remote.CommandReplan}); err != nil {
		t.Fatal(err)
	}
	// No negotiated plan_invalidated support → 409 replan_unavailable.
	rr := h.adminRequest(t, `{"name":"replan","payload":{"overrides":{"transcode":"force","max_bitrate_kbps":3000}},"reason":"buffering"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("replan without a negotiated attempt = %d %s", rr.Code, rr.Body.String())
	}

	planStore := playback.NewMemoryPlanStoreV3()
	h.playback.PlanStoreV3 = planStore
	record := playback.AttemptRecordV3{SessionID: h.session.ID, PlaybackAttemptID: "attempt-1", CurrentPlanID: "plan-old", ExpiresAt: time.Now().Add(time.Hour)}
	record.NormalizedRequest.ClientFeatures = []string{playback.FeaturePlanInvalidatedV3}
	if err := planStore.SaveAttempt(ctx, record); err != nil {
		t.Fatalf("seed attempt: %v", err)
	}

	rr = h.adminRequest(t, `{"name":"replan","payload":{"overrides":{"transcode":"force","max_bitrate_kbps":3000}},"reason":"buffering"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("replan = %d %s", rr.Code, rr.Body.String())
	}
	command := decodeRemoteCommand(t, rr)
	if command.State != remote.StateSent {
		t.Fatalf("replan state = %s", command.State)
	}
	if len(h.conn.frames) != 1 || h.conn.frames[0].Name != playback.CommandPlanInvalidated || h.conn.frames[0].CommandID != command.ID {
		t.Fatalf("frames = %+v", h.conn.frames)
	}
	var payload playback.PlanInvalidatedPayload
	_ = json.Unmarshal(h.conn.frames[0].Payload, &payload)
	if payload.PlanID != "plan-old" || payload.Reason != PlanInvalidatedAdminReplan {
		t.Fatalf("plan_invalidated payload = %+v", payload)
	}
	overrides, ok := h.playback.remotePlanOverridesV3(h.session.ID)
	if !ok || overrides.Transcode != "force" || overrides.MaxBitrateKbps != 3000 {
		t.Fatalf("pinned overrides = %+v ok=%v", overrides, ok)
	}
	if !h.playback.remoteForceTranscodeV3(h.session.ID) {
		t.Fatal("force transcode not pinned")
	}
	narrowed := h.playback.applyRemotePlanOverridesV3(h.session.ID, playback.StartRequestV3{})
	if narrowed.BandwidthCapKbps == nil || *narrowed.BandwidthCapKbps != 3000 {
		t.Fatalf("replan request not narrowed: %+v", narrowed.BandwidthCapKbps)
	}

	// The client's plan fetch completes the command.
	h.playback.notifyRemoteSessionReplanned(ctx, h.session.ID, &playback.PlanV3{PlanID: "plan-new"})
	got, _ := h.service.Get(ctx, command.ID)
	if got.State != remote.StateDone || !strings.Contains(string(got.Result), `"plan_id":"plan-new"`) {
		t.Fatalf("replan after plan fetch = %+v", got)
	}

	// Session end clears the pin.
	_ = h.playback.stopPlaybackSessionByID(ctx, h.session.ID, true)
	if _, ok := h.playback.remotePlanOverridesV3(h.session.ID); ok {
		t.Fatal("pinned overrides survived session stop")
	}
}

func TestRemoteControlAdvertiseDeviceEndpoint(t *testing.T) {
	h := newRemoteTestHarness(t, false)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/devices/device-9/remote-control", strings.NewReader(`{"version":1,"commands":["pause","seek","replan","collect_diagnostics"]}`))
	req = withPlaybackRouteParam(req, "device_id", "device-9")
	ctx := apimw.SetClaims(req.Context(), &auth.Claims{UserID: 7, Role: "user"})
	ctx = apimw.SetProfileID(ctx, "profile-1")
	rr := httptest.NewRecorder()
	h.handler.HandleAdvertiseDevice(rr, req.WithContext(ctx))
	if rr.Code != http.StatusOK {
		t.Fatalf("advertise = %d %s", rr.Code, rr.Body.String())
	}
	var resp remoteCapabilityResponse
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.DeviceID != "device-9" || resp.Version != 1 || len(resp.Commands) != 4 {
		t.Fatalf("advertise response = %+v", resp)
	}
	bad := httptest.NewRequest(http.MethodPut, "/api/v1/devices/device-9/remote-control", strings.NewReader(`{"commands":["reboot"]}`))
	bad = withPlaybackRouteParam(bad, "device_id", "device-9")
	rr = httptest.NewRecorder()
	h.handler.HandleAdvertiseDevice(rr, bad.WithContext(ctx))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown command advertise = %d", rr.Code)
	}
}
