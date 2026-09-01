package remote

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
)

type fakeSender struct {
	sessions    map[string]*SessionInfo
	dispatched  []playback.CommandEnvelope
	replans     []playback.PlanOverridesV3
	dispatchErr error
	replanErr   error
}

func (f *fakeSender) Session(id string) (*SessionInfo, error) {
	s, ok := f.sessions[id]
	if !ok {
		return nil, playback.ErrSessionNotFound
	}
	cp := *s
	return &cp, nil
}

func (f *fakeSender) Dispatch(_ context.Context, command playback.CommandEnvelope) error {
	if f.dispatchErr != nil {
		return f.dispatchErr
	}
	f.dispatched = append(f.dispatched, command)
	return nil
}

func (f *fakeSender) Replan(_ context.Context, _, _ string, overrides playback.PlanOverridesV3, _ string) (string, error) {
	if f.replanErr != nil {
		return "", f.replanErr
	}
	f.replans = append(f.replans, overrides)
	return "plan-old", nil
}

func newTestService(t *testing.T) (*Service, *MemoryStore, *fakeSender) {
	t.Helper()
	store := NewMemoryStore()
	sender := &fakeSender{sessions: map[string]*SessionInfo{
		"s1": {ID: "s1", UserID: 7, ProfileID: "p1", DeviceID: "dev-1", TenantID: "tenant-a", Connected: true},
		"s2": {ID: "s2", UserID: 8, ProfileID: "p2", DeviceID: "dev-2", TenantID: "tenant-b", Connected: false},
		"s3": {ID: "s3", UserID: 9, ProfileID: "p3", DeviceID: "", Connected: true},
	}}
	if err := store.UpsertDeviceCapability(context.Background(), DeviceCapability{UserID: 7, ProfileID: "p1", DeviceID: "dev-1",
		Commands: []playback.CommandName{playback.CommandPause, playback.CommandSeek, playback.CommandTerminate, playback.CommandDisplayMessage, CommandReplan}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDeviceCapability(context.Background(), DeviceCapability{UserID: 8, ProfileID: "p2", DeviceID: "dev-2",
		Commands: []playback.CommandName{playback.CommandPause}}); err != nil {
		t.Fatal(err)
	}
	return NewService(store, sender, nil, DefaultConfig()), store, sender
}

func adminInput(session string, name playback.CommandName, payload string, reason string) SendInput {
	return SendInput{SessionID: session, Name: name, Payload: json.RawMessage(payload), Reason: reason, IssuerKind: IssuerAdmin, IssuedBy: "admin:1"}
}

func TestValidateSessionPayloadPerCommand(t *testing.T) {
	cases := []struct {
		name    playback.CommandName
		payload string
		reason  string
		wantErr error
	}{
		{playback.CommandPause, ``, "", nil},
		{playback.CommandPause, `{"x":1}`, "", ErrInvalidPayload},
		{playback.CommandSeek, `{"position_ms":1500}`, "", nil},
		{playback.CommandSeek, `{"position_ms":-1}`, "", ErrInvalidPayload},
		{playback.CommandSeek, `{}`, "", nil},
		{playback.CommandSetVolume, `{"level":50}`, "", nil},
		{playback.CommandSetVolume, `{"level":101}`, "", ErrInvalidPayload},
		{playback.CommandStop, `{"reason":"bedtime"}`, "", nil},
		{playback.CommandTerminate, `{}`, "", ErrReasonRequired},
		{playback.CommandTerminate, `{}`, "abuse", nil},
		{playback.CommandSetAudioTrack, `{"track_id":"a2"}`, "", nil},
		{playback.CommandSetAudioTrack, `{"off":true}`, "", ErrInvalidPayload},
		{playback.CommandSetSubtitleTrack, `{"off":true}`, "", nil},
		{playback.CommandSetSubtitleTrack, `{"track_id":"s1","off":true}`, "", ErrInvalidPayload},
		{playback.CommandSetSubtitleTrack, `{}`, "", ErrInvalidPayload},
		{playback.CommandDisplayMessage, `{"title":"Hi","body":"Dinner","severity":"info","timeout_ms":5000}`, "", nil},
		{playback.CommandDisplayMessage, `{"body":"x","severity":"loud"}`, "", ErrInvalidPayload},
		{playback.CommandDisplayMessage, `{"title":"no body"}`, "", ErrInvalidPayload},
		{CommandReplan, `{"overrides":{"transcode":"force","max_bitrate_kbps":4000},"reason":"buffering"}`, "", nil},
		{CommandReplan, `{"overrides":{"transcode":"sometimes"}}`, "", ErrInvalidPayload},
		{CommandReplan, `{"overrides":{"transcode":"direct","max_bitrate_kbps":4000}}`, "", ErrInvalidPayload},
		{CommandReplan, `{"overrides":{"transcode":"auto","video_codec":"h264;drop"}}`, "", ErrInvalidPayload},
		{playback.CommandPlayMedia, `{}`, "", ErrScopeMismatch},
		{playback.CommandPlanInvalidated, `{}`, "", ErrScopeMismatch},
		{"reboot", `{}`, "", ErrUnknownCommand},
	}
	for _, tc := range cases {
		_, err := ValidateSessionPayload(tc.name, json.RawMessage(tc.payload), tc.reason)
		if tc.wantErr == nil && err != nil {
			t.Errorf("%s %s: unexpected error %v", tc.name, tc.payload, err)
		}
		if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
			t.Errorf("%s %s: error %v, want %v", tc.name, tc.payload, err, tc.wantErr)
		}
	}
}

func TestSendToSessionDeliversAndTracksAckResult(t *testing.T) {
	svc, _, sender := newTestService(t)
	ctx := context.Background()
	cmd, err := svc.SendToSession(ctx, adminInput("s1", playback.CommandSeek, `{"position_ms":1500}`, "skip intro"))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if cmd.State != StateSent || cmd.SentAt == nil {
		t.Fatalf("state = %s, want sent", cmd.State)
	}
	if len(sender.dispatched) != 1 || sender.dispatched[0].CommandID != cmd.ID || sender.dispatched[0].Name != playback.CommandSeek {
		t.Fatalf("dispatched = %+v", sender.dispatched)
	}
	if sender.dispatched[0].IssuedBy == nil || sender.dispatched[0].IssuedBy.Kind != "admin" || sender.dispatched[0].Reason != "skip intro" {
		t.Fatalf("envelope issued_by/reason = %+v", sender.dispatched[0])
	}
	svc.OnAck(ctx, cmd.ID)
	got, _ := svc.Get(ctx, cmd.ID)
	if got.State != StateAccepted || got.AckedAt == nil {
		t.Fatalf("after ack state = %s", got.State)
	}
	svc.OnResult(ctx, cmd.ID, true, "")
	got, _ = svc.Get(ctx, cmd.ID)
	if got.State != StateDone || got.FinishedAt == nil || string(got.Result) != `{"status":"completed"}` {
		t.Fatalf("after result = %+v", got)
	}
	// A late duplicate never reopens a terminal row.
	svc.OnResult(ctx, cmd.ID, false, "late")
	got, _ = svc.Get(ctx, cmd.ID)
	if got.State != StateDone {
		t.Fatalf("terminal row reopened: %s", got.State)
	}
}

func TestSendToSessionRejectedResultAndExpiry(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	cmd, err := svc.SendToSession(ctx, adminInput("s1", playback.CommandPause, ``, ""))
	if err != nil {
		t.Fatal(err)
	}
	svc.OnResult(ctx, cmd.ID, false, "not_playing")
	got, _ := svc.Get(ctx, cmd.ID)
	if got.State != StateRejected || got.Error != "not_playing" {
		t.Fatalf("rejected = %+v", got)
	}

	base := time.Now()
	svc.SetClock(func() time.Time { return base })
	cmd, err = svc.SendToSession(ctx, adminInput("s1", playback.CommandPause, ``, ""))
	if err != nil {
		t.Fatal(err)
	}
	svc.SetClock(func() time.Time { return base.Add(2 * time.Minute) })
	got, _ = svc.Get(ctx, cmd.ID)
	if got.State != StateExpired {
		t.Fatalf("unanswered command after TTL = %s, want expired", got.State)
	}
}

func TestSendToSessionUnsupportedByDevice(t *testing.T) {
	svc, _, sender := newTestService(t)
	ctx := context.Background()
	cmd, err := svc.SendToSession(ctx, adminInput("s1", playback.CommandSetVolume, `{"level":10}`, ""))
	if err != nil {
		t.Fatal(err)
	}
	if cmd.State != StateRejectedUnsupported || len(sender.dispatched) != 0 {
		t.Fatalf("state = %s, dispatched = %d", cmd.State, len(sender.dispatched))
	}
	// No remote_control block at all: not controllable.
	cmd, err = svc.SendToSession(ctx, adminInput("s3", playback.CommandPause, ``, ""))
	if err != nil {
		t.Fatal(err)
	}
	if cmd.State != StateRejectedUnsupported {
		t.Fatalf("device without block state = %s", cmd.State)
	}
}

func TestSendToSessionNotConnected(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, err := svc.SendToSession(context.Background(), adminInput("s2", playback.CommandPause, ``, ""))
	if !errors.Is(err, ErrSessionNotConnected) {
		t.Fatalf("err = %v, want session_not_connected", err)
	}
	svc2, store, sender := newTestService(t)
	sender.dispatchErr = playback.ErrRealtimeConnectionNotFound
	_, err = svc2.SendToSession(context.Background(), adminInput("s1", playback.CommandPause, ``, ""))
	if !errors.Is(err, ErrSessionNotConnected) {
		t.Fatalf("socket write failure err = %v", err)
	}
	rows, _ := store.ListAudit(context.Background(), AuditQuery{SessionID: "s1"})
	if len(rows) != 1 || rows[0].State != StateFailed || rows[0].Error != "session_not_connected" {
		t.Fatalf("audit after failed write = %+v", rows)
	}
	_, err = svc.SendToSession(context.Background(), adminInput("missing", playback.CommandPause, ``, ""))
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("missing session err = %v", err)
	}
}

func TestHouseholdScopeAndReducedSet(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	household := func(session string, name playback.CommandName, user int) SendInput {
		return SendInput{SessionID: session, Name: name, IssuerKind: IssuerHousehold, IssuedBy: "profile:p1", HouseholdUserID: user, Reason: "r"}
	}
	if _, err := svc.SendToSession(ctx, household("s1", playback.CommandPause, 7)); err != nil {
		t.Fatalf("own household pause: %v", err)
	}
	if _, err := svc.SendToSession(ctx, household("s1", playback.CommandPause, 8)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other household err = %v", err)
	}
	if _, err := svc.SendToSession(ctx, household("s1", playback.CommandTerminate, 7)); !errors.Is(err, ErrNotHouseholdCommand) {
		t.Fatalf("terminate from household err = %v", err)
	}
	if _, err := svc.SendToSession(ctx, household("s1", CommandReplan, 7)); !errors.Is(err, ErrNotHouseholdCommand) {
		t.Fatalf("replan from household err = %v", err)
	}
}

func TestAdminTenantScope(t *testing.T) {
	svc, _, _ := newTestService(t)
	in := adminInput("s1", playback.CommandPause, ``, "")
	in.TenantScope = "tenant-b"
	if _, err := svc.SendToSession(context.Background(), in); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant err = %v", err)
	}
	in.TenantScope = "tenant-a"
	if _, err := svc.SendToSession(context.Background(), in); err != nil {
		t.Fatalf("same tenant: %v", err)
	}
}

func TestRateLimitPerIssuer(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		if _, err := svc.SendToSession(ctx, adminInput("s1", playback.CommandPause, ``, "")); err != nil {
			t.Fatalf("command %d: %v", i, err)
		}
	}
	if _, err := svc.SendToSession(ctx, adminInput("s1", playback.CommandPause, ``, "")); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("31st command err = %v, want rate_limited", err)
	}
	other := adminInput("s1", playback.CommandPause, ``, "")
	other.IssuedBy = "admin:2"
	if _, err := svc.SendToSession(ctx, other); err != nil {
		t.Fatalf("other issuer: %v", err)
	}
}

func TestReplanPinsOverridesAndCompletesOnPlanFetch(t *testing.T) {
	svc, _, sender := newTestService(t)
	ctx := context.Background()
	cmd, err := svc.SendToSession(ctx, adminInput("s1", CommandReplan, `{"overrides":{"transcode":"force","max_bitrate_kbps":4000,"video_codec":"H264"},"reason":"buffering"}`, ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(sender.replans) != 1 || len(sender.dispatched) != 0 {
		t.Fatalf("replans = %d dispatched = %d", len(sender.replans), len(sender.dispatched))
	}
	if got := sender.replans[0]; got.Transcode != "force" || got.MaxBitrateKbps != 4000 || got.VideoCodec != "h264" {
		t.Fatalf("overrides = %+v", got)
	}
	svc.OnAck(ctx, cmd.ID)
	svc.OnSessionReplanned(ctx, "s1", "plan-new")
	got, _ := svc.Get(ctx, cmd.ID)
	if got.State != StateDone || string(got.Result) != `{"plan_id":"plan-new","status":"completed"}` {
		t.Fatalf("replan after plan fetch = %+v", got)
	}
	// Terminate needs a reason (§F).
	if _, err := svc.SendToSession(ctx, adminInput("s1", playback.CommandTerminate, `{}`, "")); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("terminate without reason err = %v", err)
	}
	if _, err := svc.SendToSession(ctx, adminInput("s1", playback.CommandTerminate, `{}`, "policy")); err != nil {
		t.Fatalf("terminate with reason: %v", err)
	}
	if sender.dispatched[len(sender.dispatched)-1].Reason != "policy" {
		t.Fatalf("terminate reason not on the envelope")
	}
}

func TestAdvertiseDeviceRejectsUnknownNames(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	if err := svc.AdvertiseDevice(ctx, 7, "p1", "dev-9", 1, []playback.CommandName{playback.CommandPause, "collect_diagnostics"}); err != nil {
		t.Fatalf("advertise: %v", err)
	}
	if err := svc.AdvertiseDevice(ctx, 7, "p1", "dev-9", 1, []playback.CommandName{"reboot"}); !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("unknown name err = %v", err)
	}
	svc.OnHello(ctx, SessionInfo{ID: "s", UserID: 7, ProfileID: "p1", DeviceID: "dev-10"}, nil)
	if c, _ := svc.DeviceCapability(ctx, 7, "p1", "dev-10"); c != nil {
		t.Fatalf("empty hello list must not create a capability row")
	}
	svc.OnHello(ctx, SessionInfo{ID: "s", UserID: 7, ProfileID: "p1", DeviceID: "dev-10"}, []playback.CommandName{playback.CommandPause})
	if c, _ := svc.DeviceCapability(ctx, 7, "p1", "dev-10"); !c.Supports(playback.CommandPause) {
		t.Fatalf("hello list not persisted")
	}
}

func TestOpenReplanOverridesReadsNewestLiveRowFromStore(t *testing.T) {
	svc, store, _ := newTestService(t)
	ctx := context.Background()
	if _, ok := svc.OpenReplanOverrides(ctx, "s1"); ok {
		t.Fatal("pin without any replan")
	}
	first, err := svc.SendToSession(ctx, adminInput("s1", CommandReplan, `{"overrides":{"transcode":"force","max_bitrate_kbps":2000}}`, "buffering"))
	if err != nil {
		t.Fatal(err)
	}
	// A second service over the same store (another instance) sees the pin.
	other := NewService(store, &fakeSender{}, nil, DefaultConfig())
	if got, ok := other.OpenReplanOverrides(ctx, "s1"); !ok || got.MaxBitrateKbps != 2000 || !got.ForceTranscode() {
		t.Fatalf("other instance pin = %+v ok=%v", got, ok)
	}
	// Completion keeps the pin; a newer replan replaces it.
	svc.OnSessionReplanned(ctx, "s1", "plan-2")
	if got, ok := svc.OpenReplanOverrides(ctx, "s1"); !ok || got.MaxBitrateKbps != 2000 {
		t.Fatalf("pin after done = %+v ok=%v", got, ok)
	}
	second, err := svc.SendToSession(ctx, adminInput("s1", CommandReplan, `{"overrides":{"transcode":"direct"}}`, "try direct"))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := svc.OpenReplanOverrides(ctx, "s1"); !ok || got.Transcode != "direct" || got.MaxBitrateKbps != 0 {
		t.Fatalf("newest replan not pinned: %+v ok=%v", got, ok)
	}
	// A rejected newest replan falls back to the previous live one.
	svc.OnResult(ctx, second.ID, false, "cannot")
	if got, ok := svc.OpenReplanOverrides(ctx, "s1"); !ok || got.MaxBitrateKbps != 2000 {
		t.Fatalf("pin after rejected newest = %+v ok=%v", got, ok)
	}
	// Session end expires whatever is still open and only that.
	third, err := svc.SendToSession(ctx, adminInput("s1", CommandReplan, `{"overrides":{"transcode":"auto","video_codec":"h264"}}`, "pin codec"))
	if err != nil {
		t.Fatal(err)
	}
	svc.OnSessionEnded(ctx, "s1")
	if got, _ := svc.Get(ctx, third.ID); got.State != StateExpired {
		t.Fatalf("open replan at session end = %s", got.State)
	}
	if got, _ := svc.Get(ctx, first.ID); got.State != StateDone {
		t.Fatalf("done replan touched at session end: %s", got.State)
	}
	if got, ok := svc.OpenReplanOverrides(ctx, "s1"); !ok || got.MaxBitrateKbps != 2000 {
		t.Fatalf("pin after session end = %+v ok=%v", got, ok)
	}
	// An unanswered replan past its TTL is not a pin.
	base := time.Now()
	svc.SetClock(func() time.Time { return base })
	if _, err := svc.SendToSession(ctx, adminInput("s1", CommandReplan, `{"overrides":{"transcode":"force","max_bitrate_kbps":500}}`, "late")); err != nil {
		t.Fatal(err)
	}
	svc.SetClock(func() time.Time { return base.Add(2 * time.Minute) })
	if got, ok := svc.OpenReplanOverrides(ctx, "s1"); ok && got.MaxBitrateKbps == 500 {
		t.Fatalf("expired replan still pinned: %+v", got)
	}
}

func TestForgetDeviceDropsCapabilityAndRemoteOnlyNames(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	if err := svc.ForgetDevice(ctx, 7, "p1", "dev-1"); err != nil {
		t.Fatal(err)
	}
	if c, _ := svc.DeviceCapability(ctx, 7, "p1", "dev-1"); c != nil {
		t.Fatal("capability survived forget")
	}
	cmd, err := svc.SendToSession(ctx, adminInput("s1", playback.CommandPause, ``, ""))
	if err != nil || cmd.State != StateRejectedUnsupported {
		t.Fatalf("forgotten device still controllable: %+v err=%v", cmd, err)
	}
	for name, want := range map[playback.CommandName]bool{CommandReplan: true, "collect_diagnostics": true, playback.CommandPause: false, "reboot": false} {
		if got := IsRemoteOnlyCommand(name); got != want {
			t.Errorf("IsRemoteOnlyCommand(%s) = %v want %v", name, got, want)
		}
	}
}
