package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// fakeStreamControl stands in for Silo's administrator terminate and
// sequenced command services, which need a live playback stack.
type fakeStreamControl struct {
	mu         sync.Mutex
	terminated []handlers.AdminTerminateInput
	commands   []handlers.AdminPlaybackCommandInput
	terminate  error
	command    error
	replay     bool
	notified   bool
}

func (f *fakeStreamControl) AdminTerminateAvailable() bool        { return true }
func (f *fakeStreamControl) AdminPlaybackCommandsAvailable() bool { return true }

func (f *fakeStreamControl) Terminate(_ context.Context, in handlers.AdminTerminateInput) (handlers.AdminTerminateView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminated = append(f.terminated, in)
	if f.terminate != nil {
		return handlers.AdminTerminateView{}, f.terminate
	}
	return handlers.AdminTerminateView{SessionID: in.SessionID, AuthorityRevoked: true, ClientNotified: f.notified, DurableState: "stopped"}, nil
}

func (f *fakeStreamControl) Command(_ context.Context, in handlers.AdminPlaybackCommandInput) (handlers.AdminPlaybackCommandView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, in)
	if f.command != nil {
		return handlers.AdminPlaybackCommandView{}, f.command
	}
	outcome := handlers.AdminPlaybackCommandApplied
	if f.replay {
		outcome = handlers.AdminPlaybackCommandReplayed
	}
	return handlers.AdminPlaybackCommandView{CommandID: in.CommandID, Sequence: in.Sequence, Outcome: outcome, Delivery: handlers.AdminPlaybackDeliveryDispatched}, nil
}

type streamFixture struct {
	*supportFixture
	control    *fakeStreamControl
	platformID int
}

func newStreamFixture(t *testing.T) *streamFixture {
	t.Helper()
	control := &fakeStreamControl{notified: true}
	base := newSupportFixtureWith(t, BloemDependencies{AdminPlaybackControl: control})
	f := &streamFixture{supportFixture: base, control: control}
	ctx := context.Background()
	// A platform-level account that is also a member of the organization.
	if err := f.pool.QueryRow(ctx, `INSERT INTO users (username,email,password_hash,role,enabled) VALUES ('north-platform','north-platform@example.test','x','admin',true) RETURNING id`).Scan(&f.platformID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','user')`, f.organizationIDs[0], f.platformID); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *streamFixture) addStream(t *testing.T, sessionID string, accountID int, profileID, method string) {
	t.Helper()
	if profileID == "" {
		profileID = f.addProfile(t, accountID, "P-"+sessionID, accountID == f.platformID)
	}
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO playback_sessions_sync (session_id,user_id,profile_id,media_file_id,play_method,reporting_node,started_at,updated_at,last_sync_at,client_ip,client_name,client_version,client_user_agent,device_id,device_platform,transcode_node_url,stream_bitrate_kbps,stream_location,routing_execution,has_websocket)
		VALUES ($1,$2,$3,0,$4,'node-eu-1',now()-interval '5 minutes',now(),now(),'192.0.2.9','Bloem for TV','1.4.2','BloemTV/1.4.2','device-'||$1,'androidtv','https://node.example.test',8000,'remote','prefer_worker',true)`,
		sessionID, accountID, profileID, method); err != nil {
		t.Fatal(err)
	}
}

func (f *streamFixture) streams(t *testing.T, query, pass string) map[string]any {
	t.Helper()
	response := performJSONRequest(t, f.router, http.MethodGet, NativeAPIPrefix+"/admin/organization/streams"+query, "", pass, nil)
	return assertStatus(t, response, http.StatusOK, "list streams")
}

func (f *streamFixture) stream(t *testing.T, method, path, body, pass string) *httptest.ResponseRecorder {
	t.Helper()
	return performJSONRequest(t, f.router, method, NativeAPIPrefix+"/admin/organization/streams/"+path, body, pass, nil)
}

//nolint:funlen,gocyclo // one acceptance walk through the stream routes, in the order a support session takes them
func TestSupportStreamsAcceptance(t *testing.T) {
	f := newStreamFixture(t)
	f.addStream(t, "sess-member", f.memberID, f.memberProfile, "transcode")
	f.addStream(t, "sess-other", f.otherMemberID, "", "direct")
	f.addStream(t, "sess-owner", f.ownerID, "", "remux")
	f.addStream(t, "sess-admin", f.adminID, "", "direct")
	f.addStream(t, "sess-platform", f.platformID, "", "direct")
	var foreignProfile string
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM user_profiles WHERE user_id=$1 LIMIT 1`, f.foreignID).Scan(&foreignProfile); err != nil {
		t.Fatal(err)
	}
	f.addStream(t, "sess-foreign", f.foreignID, foreignProfile, "direct")
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO user_devices (user_id,profile_id,device_id,device_name,device_platform) VALUES ($1,$2,'device-sess-member','Living room TV','androidtv')`, f.memberID, f.memberProfile); err != nil {
		t.Fatal(err)
	}

	// The list: this organization's streams only, support fields only.
	listed := f.streams(t, "", f.ownerPass)
	rows := listed["streams"].([]any)
	if len(rows) != 5 {
		t.Fatalf("streams = %d, want the organization's 5", len(rows))
	}
	allowed := map[string]bool{"session_id": true, "account_id": true, "person_name": true, "profile_id": true, "profile_name": true, "title": true, "series_name": true, "season_number": true, "episode_number": true, "media_type": true, "started_at": true, "updated_at": true, "position_seconds": true, "duration_seconds": true, "is_paused": true, "play_method": true, "video_decision": true, "audio_decision": true, "device": true, "can_stop": true, "can_message": true}
	var member map[string]any
	for _, raw := range rows {
		row := raw.(map[string]any)
		if row["session_id"] == "sess-foreign" {
			t.Fatal("another organization's stream was listed")
		}
		for key := range row {
			if !allowed[key] {
				t.Errorf("stream exposes %q", key)
			}
		}
		if row["session_id"] == "sess-member" {
			member = row
		}
	}
	for _, secret := range []string{"192.0.2.9", "node-eu-1", "node.example.test", "prefer_worker", "BloemTV/1.4.2", "client_ip", "routing", "stream_location", "bitrate"} {
		if strings.Contains(fmt.Sprint(listed), secret) {
			t.Errorf("the stream list contains %q", secret)
		}
	}
	device := member["device"].(map[string]any)
	if member["play_method"] != "transcode" || device["name"] != "Living room TV" || device["platform"] != "androidtv" || device["id"] != "device-sess-member" || member["person_name"] != "north-member" || member["profile_name"] != "Primary" {
		t.Errorf("member stream = %v", member)
	}
	byID := map[string]map[string]any{}
	for _, raw := range rows {
		byID[raw.(map[string]any)["session_id"].(string)] = raw.(map[string]any)
	}
	if byID["sess-owner"]["can_stop"] != false || byID["sess-platform"]["can_stop"] != false || byID["sess-member"]["can_stop"] != true || byID["sess-admin"]["can_message"] != true {
		t.Errorf("can_stop and can_message do not follow the stream rules: owner=%v platform=%v member=%v", byID["sess-owner"]["can_stop"], byID["sess-platform"]["can_stop"], byID["sess-member"]["can_stop"])
	}

	// Pages: every stream once, cursors stay correct across pages.
	var seen []string
	cursor := ""
	for page := 0; page < 10; page++ {
		query := "?limit=2"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		body := f.streams(t, query, f.ownerPass)
		for _, raw := range body["streams"].([]any) {
			seen = append(seen, raw.(map[string]any)["session_id"].(string))
		}
		next, _ := body["next_cursor"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	sort.Strings(seen)
	if strings.Join(seen, ",") != "sess-admin,sess-member,sess-other,sess-owner,sess-platform" {
		t.Errorf("paged streams = %v", seen)
	}
	assertError(t, performJSONRequest(t, f.router, http.MethodGet, NativeAPIPrefix+"/admin/organization/streams?limit=500", "", f.ownerPass, nil), http.StatusUnprocessableEntity, "validation_failed", "limit out of range")
	assertError(t, performJSONRequest(t, f.router, http.MethodGet, NativeAPIPrefix+"/admin/organization/streams?cursor=!!!", "", f.ownerPass, nil), http.StatusUnprocessableEntity, "validation_failed", "bad cursor")

	// Stop: Silo's terminate, after the organization filter.
	stopped := assertStatus(t, f.stream(t, http.MethodPost, "sess-member/stop", ``, f.ownerPass), http.StatusOK, "stop a member's stream")
	if stopped["authority_revoked"] != true || stopped["client_notified"] != true || stopped["already_revoked"] != false {
		t.Errorf("stop receipt = %v", stopped)
	}
	if len(f.control.terminated) != 1 || f.control.terminated[0].SessionID != "sess-member" || f.control.terminated[0].ActorID != f.ownerID {
		t.Fatalf("terminate calls = %+v", f.control.terminated)
	}
	if rows := f.auditActions(t, "stream.stopped"); len(rows) != 1 || !strings.Contains(rows[0], `"authority_revoked": true`) {
		t.Errorf("stream.stopped audit = %v", rows)
	}
	// An administrator may stop the owner's stream: stopping is not account takeover.
	assertStatus(t, f.stream(t, http.MethodPost, "sess-owner/stop", `{"reason":"maintenance"}`, f.adminPass), http.StatusOK, "administrator stops the owner's stream")
	if got := f.control.terminated[len(f.control.terminated)-1].Reason; got != "maintenance" {
		t.Errorf("reason = %q", got)
	}

	// Refused: your own stream, a platform account's stream. Nothing reaches Silo.
	before := len(f.control.terminated)
	for session, reason := range map[string]string{"sess-owner": "self", "sess-platform": "platform_account"} {
		body := assertError(t, f.stream(t, http.MethodPost, session+"/stop", ``, f.ownerPass), http.StatusConflict, "target_protected", "stop "+session)
		if body["reason"] != reason {
			t.Errorf("%s: reason = %v, want %s", session, body["reason"], reason)
		}
	}
	if len(f.control.terminated) != before {
		t.Error("a refused stop reached Silo's terminate")
	}
	if got := len(f.auditActions(t, "stream.stop_refused")); got != 2 {
		t.Errorf("stop refusal audit rows = %d", got)
	}

	// Another organization's session and a session that does not exist are one 404.
	foreign := f.stream(t, http.MethodPost, "sess-foreign/stop", ``, f.ownerPass)
	missing := f.stream(t, http.MethodPost, "sess-none/stop", ``, f.ownerPass)
	assertError(t, foreign, http.StatusNotFound, "not_found", "foreign stream")
	if foreign.Body.String() != missing.Body.String() {
		t.Errorf("foreign %q and missing %q differ", foreign.Body.String(), missing.Body.String())
	}
	assertError(t, f.stream(t, http.MethodPost, "sess-foreign/message", `{"command_id":"3fa85f64-5717-4562-b3fc-2c963f66afa6","sequence":1,"message":"hi"}`, f.ownerPass), http.StatusNotFound, "not_found", "foreign message")

	// Silo's terminate failing to reach the holder: authority is revoked, retry is safe.
	f.control.terminate = handlers.ErrAdminTerminateNoReply
	noReply := f.stream(t, http.MethodPost, "sess-other/stop", ``, f.ownerPass)
	assertError(t, noReply, http.StatusServiceUnavailable, "dependency_unavailable", "terminate without a reply")
	if noReply.Header().Get("Retry-After") != "30" {
		t.Errorf("Retry-After = %q", noReply.Header().Get("Retry-After"))
	}
	f.control.terminate = playback.ErrSessionNotFound
	assertError(t, f.stream(t, http.MethodPost, "sess-other/stop", ``, f.ownerPass), http.StatusNotFound, "not_found", "session gone at Silo")
	f.control.terminate = nil

	// Message: Silo's client-allocated identity.
	f.ageAudit(t)
	const identity = `"command_id":"3fa85f64-5717-4562-b3fc-2c963f66afa6","sequence":7`
	accepted := f.stream(t, http.MethodPost, "sess-other/message", `{`+identity+`,"title":"Hello","message":"Dinner is ready"}`, f.ownerPass)
	receipt := assertStatus(t, accepted, http.StatusAccepted, "message")
	if receipt["outcome"] != "applied" || receipt["sequence"] != float64(7) || receipt["delivery"] != "dispatched" {
		t.Errorf("message receipt = %v", receipt)
	}
	if last := f.control.commands[len(f.control.commands)-1]; last.Name != playback.CommandDisplayMessage || last.Title != "Hello" || last.Message != "Dinner is ready" || last.ActorID != f.ownerID || last.Sequence != 7 {
		t.Errorf("command = %+v", last)
	}
	long := strings.Repeat("x", 600)
	assertStatus(t, f.stream(t, http.MethodPost, "sess-other/message", `{"command_id":"4fa85f64-5717-4562-b3fc-2c963f66afa6","sequence":8,"message":"`+long+`"}`, f.ownerPass), http.StatusAccepted, "long message")
	rows2 := f.auditActions(t, "stream.message_sent")
	if len(rows2) != 2 || strings.Contains(rows2[1], strings.Repeat("x", 300)) || !strings.Contains(rows2[1], strings.Repeat("x", 256)) {
		t.Errorf("message audit rows = %v", rows2)
	}
	f.control.replay = true
	assertStatus(t, f.stream(t, http.MethodPost, "sess-other/message", `{`+identity+`,"message":"Dinner is ready","title":"Hello"}`, f.ownerPass), http.StatusOK, "replayed message")
	if got := len(f.auditActions(t, "stream.message_sent")); got != 2 {
		t.Errorf("a replay was audited as a new message (%d rows)", got)
	}
	f.control.replay = false
	f.control.command = &handlers.AdminPlaybackCommandStaleError{Latest: 12}
	stale := f.stream(t, http.MethodPost, "sess-other/message", `{"command_id":"5fa85f64-5717-4562-b3fc-2c963f66afa6","sequence":2,"message":"late"}`, f.ownerPass)
	assertError(t, stale, http.StatusConflict, "stale_sequence", "stale sequence")
	if stale.Header().Get("X-Silo-Latest-Sequence") != "12" {
		t.Errorf("X-Silo-Latest-Sequence = %q", stale.Header().Get("X-Silo-Latest-Sequence"))
	}
	f.control.command = errors.New("lane closed")
	assertError(t, f.stream(t, http.MethodPost, "sess-other/message", `{"command_id":"6fa85f64-5717-4562-b3fc-2c963f66afa6","sequence":9,"message":"x"}`, f.ownerPass), http.StatusServiceUnavailable, "tenant_unavailable", "unknown lane failure")
	f.control.command = nil
	for _, body := range []string{`{"command_id":"nope","sequence":1,"message":"x"}`, `{"command_id":"3fa85f64-5717-4562-b3fc-2c963f66afa6","sequence":0,"message":"x"}`, `{"command_id":"3fa85f64-5717-4562-b3fc-2c963f66afa6","sequence":1,"message":""}`} {
		assertError(t, f.stream(t, http.MethodPost, "sess-other/message", body, f.ownerPass), http.StatusUnprocessableEntity, "validation_failed", "bad message "+body)
	}
	assertError(t, f.stream(t, http.MethodPost, "sess-owner/message", `{"command_id":"3fa85f64-5717-4562-b3fc-2c963f66afa6","sequence":1,"message":"x"}`, f.ownerPass), http.StatusConflict, "target_protected", "message to yourself")

	// The per-session budget: six actions a minute.
	f.ageAudit(t)
	for i := 0; i < 6; i++ {
		assertStatus(t, f.stream(t, http.MethodPost, "sess-admin/stop", ``, f.ownerPass), http.StatusOK, "stop in budget")
	}
	limited := f.stream(t, http.MethodPost, "sess-admin/stop", ``, f.ownerPass)
	assertError(t, limited, http.StatusTooManyRequests, "rate_limited", "stop over budget")
	if limited.Header().Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}
}

func TestSupportCapabilitiesAndFeatureToken(t *testing.T) {
	f := newStreamFixture(t)
	f.setting(t, "server.public_url", "https://watch.example.test")
	capabilities := assertStatus(t, performJSONRequest(t, f.router, http.MethodGet, NativeAPIPrefix+"/admin/organization/support-capabilities", "", f.ownerPass, nil), http.StatusOK, "support capabilities")
	streams := capabilities["streams"].(map[string]any)
	if streams["list"] != true || streams["stop"] != true || streams["message"] != true {
		t.Errorf("streams = %v", streams)
	}
	reset := capabilities["password_reset"].(map[string]any)
	if reset["link"] != true || reset["email"] != false {
		t.Errorf("password_reset = %v, want a link and no email on a server without mail", reset)
	}
	profiles := capabilities["profiles"].(map[string]any)
	for _, key := range []string{"add", "rename", "clear_pin", "remove"} {
		if profiles[key] != true {
			t.Errorf("profiles.%s = %v", key, profiles[key])
		}
	}
	public := assertStatus(t, performJSONRequest(t, f.router, http.MethodGet, NativeAPIPrefix+"/capabilities", "", "", nil), http.StatusOK, "public capabilities")
	if !strings.Contains(fmt.Sprint(public["feature_tokens"]), "organization_support_v1") {
		t.Errorf("feature_tokens = %v", public["feature_tokens"])
	}
	// Streams without Silo's control service are advertised as unavailable.
	bare := newSupportFixture(t)
	body := assertStatus(t, performJSONRequest(t, bare.router, http.MethodGet, NativeAPIPrefix+"/admin/organization/support-capabilities", "", bare.ownerPass, nil), http.StatusOK, "bare capabilities")
	if bare := body["streams"].(map[string]any); bare["stop"] != false || bare["message"] != false {
		t.Errorf("streams without control = %v", bare)
	}
}
