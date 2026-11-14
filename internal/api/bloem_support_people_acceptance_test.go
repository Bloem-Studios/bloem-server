package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/Silo-Server/silo-server/internal/mail"
)

// supportFixture is organization North of the two-organization acceptance
// fixture with one of each kind of person: its owner (the fixture's
// administrator), a second administrator, and ordinary members. Every actor
// holds a real administrative context pass minted through
// POST /admin/session, so these tests run the whole middleware stack.
type supportFixture struct {
	*multitenantAdminAcceptanceFixture
	ownerID, adminID, memberID, otherMemberID, foreignID int
	ownerPass, adminPass                                 string
	memberProfile, kidProfile                            string
	groupID                                              int
}

const supportPassword = "correct horse battery staple"

func newSupportFixture(t *testing.T) *supportFixture {
	t.Helper()
	return newSupportFixtureWithMail(t, nil)
}

func newSupportFixtureWithMail(t *testing.T, sender mail.Sender) *supportFixture {
	t.Helper()
	return newSupportFixtureWith(t, BloemDependencies{SupportMail: sender})
}

func newSupportFixtureWith(t *testing.T, bloem BloemDependencies) *supportFixture {
	t.Helper()
	base := newMultitenantAdminAcceptanceFixtureWith(t, bloem)
	f := &supportFixture{multitenantAdminAcceptanceFixture: base}
	ctx := context.Background()
	north := base.organizationIDs[0]
	if err := base.pool.QueryRow(ctx, `SELECT owner_account_id FROM organizations WHERE id=$1`, north).Scan(&f.ownerID); err != nil {
		t.Fatal(err)
	}
	if err := base.pool.QueryRow(ctx, `SELECT id FROM access_groups WHERE organization_id=$1 AND is_default`, north).Scan(&f.groupID); err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(supportPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	add := func(username, role string) int {
		var id int
		if err := base.pool.QueryRow(ctx, `INSERT INTO users (username,email,password_hash,role,enabled) VALUES ($1,$2,$3,'user',true) RETURNING id`, username, username+"@example.test", string(hash)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := base.pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active',$3)`, north, id, role); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.adminID = add("north-second-admin", "admin")
	f.memberID = add("north-member", "user")
	f.otherMemberID = add("north-other-member", "user")
	if err := base.pool.QueryRow(ctx, `SELECT id FROM users WHERE username='canal-only'`).Scan(&f.foreignID); err != nil {
		t.Fatal(err)
	}
	f.memberProfile = f.addProfile(t, f.memberID, "Primary", true)
	f.kidProfile = f.addProfile(t, f.memberID, "Kid", false)
	f.addProfile(t, f.ownerID, "Owner Profile", true)
	f.ownerPass = base.organizationTokens[0]
	f.adminPass = f.mint(t, "north-second-admin")
	return f
}

func (f *supportFixture) addProfile(t *testing.T, accountID int, name string, primary bool) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO user_profiles (id,user_id,name,is_primary,organization_id,access_group_id) VALUES ($1,$2,$3,$4,$5,$6)`, id, accountID, name, primary, f.organizationIDs[0], f.groupID); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *supportFixture) mint(t *testing.T, username string) string {
	t.Helper()
	login := performJSONRequest(t, f.router, http.MethodPost, "/api/v2/auth/login", `{"username":"`+username+`","password":"`+supportPassword+`"}`, "", nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login %s = %d %s", username, login.Code, login.Body.String())
	}
	response := performJSONRequest(t, f.router, http.MethodPost, NativeAPIPrefix+"/admin/session", `{"scope":"organization","organization_id":"`+f.organizationIDs[0].String()+`"}`, decodeLogin(t, login).AccessToken, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("mint %s = %d %s", username, response.Code, response.Body.String())
	}
	var session struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil || session.AccessToken == "" {
		t.Fatalf("decode pass for %s: %v", username, err)
	}
	return session.AccessToken
}

func (f *supportFixture) call(t *testing.T, method, path, body, pass string) *httptest.ResponseRecorder {
	t.Helper()
	return performJSONRequest(t, f.router, method, NativeAPIPrefix+"/admin/organization/people/"+path, body, pass, nil)
}

func (f *supportFixture) revision(t *testing.T, accountID int) int64 {
	t.Helper()
	var revision int64
	if err := f.pool.QueryRow(context.Background(), `SELECT security_revision FROM organization_memberships WHERE account_id=$1`, accountID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func (f *supportFixture) profileRevision(t *testing.T, profileID string) int64 {
	t.Helper()
	var revision int64
	if err := f.pool.QueryRow(context.Background(), `SELECT revision FROM user_profiles WHERE id=$1`, profileID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func (f *supportFixture) auditActions(t *testing.T, action string) []string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `SELECT outcome||' '||after_state::text||' '||before_state::text FROM admin_audit_events WHERE organization_id=$1 AND action=$2 ORDER BY id`, f.organizationIDs[0], action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// ageAudit moves every audit row back by an hour, so the rate-limit windows
// have passed. The immutability trigger is lifted for the update only; the
// database is disposable.
func (f *supportFixture) ageAudit(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, query := range []string{
		`ALTER TABLE admin_audit_events DISABLE TRIGGER admin_audit_events_immutable`,
		`UPDATE admin_audit_events SET created_at = created_at - interval '2 hours'`,
		`ALTER TABLE admin_audit_events ENABLE TRIGGER admin_audit_events_immutable`,
	} {
		if _, err := f.pool.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *supportFixture) setting(t *testing.T, key, value string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO server_settings (key,value) VALUES ($1,$2) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`, key, value); err != nil {
		t.Fatal(err)
	}
}

func assertStatus(t *testing.T, response *httptest.ResponseRecorder, want int, label string) map[string]any {
	t.Helper()
	if response.Code != want {
		t.Fatalf("%s = %d %s, want %d", label, response.Code, response.Body.String(), want)
	}
	if response.Body.Len() == 0 {
		return nil
	}
	var out map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &out)
	return out
}

func assertError(t *testing.T, response *httptest.ResponseRecorder, status int, code, label string) map[string]any {
	t.Helper()
	body := assertStatus(t, response, status, label)
	if body["error"] != code {
		t.Fatalf("%s error = %v, want %s (%s)", label, body["error"], code, response.Body.String())
	}
	return body
}

func TestSupportPasswordResetAcceptance(t *testing.T) {
	f := newSupportFixture(t)
	f.setting(t, "server.public_url", "https://watch.example.test")
	reset := func(pass string, account int, revision int64, delivery string) *httptest.ResponseRecorder {
		return f.call(t, http.MethodPost, fmt.Sprintf("%d/password-reset", account), fmt.Sprintf(`{"expected_revision":%d,"delivery":%q}`, revision, delivery), pass)
	}

	before := f.revision(t, f.memberID)
	response := reset(f.ownerPass, f.memberID, before, "link")
	body := assertStatus(t, response, http.StatusCreated, "owner resets member")
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
	issued := body["reset"].(map[string]any)
	resetURL, _ := issued["reset_url"].(string)
	if !strings.HasPrefix(resetURL, "https://watch.example.test/reset-password/") || issued["delivery_status"] != "not_requested" {
		t.Fatalf("reset = %v", issued)
	}
	expires, _ := time.Parse(time.RFC3339, issued["expires_at"].(string))
	if until := time.Until(expires); until < 3*time.Hour+50*time.Minute || until > 4*time.Hour+time.Minute {
		t.Errorf("link lifetime = %s, want 4h", until)
	}
	if got := f.revision(t, f.memberID); got != before || body["person"].(map[string]any)["security_revision"].(float64) != float64(before) {
		t.Errorf("issuing a link changed the security revision: %d -> %d", before, got)
	}
	token := strings.TrimPrefix(resetURL, "https://watch.example.test/reset-password/")
	var stored int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM password_reset_tokens WHERE user_id=$1`, f.memberID).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("stored links = %d, %v", stored, err)
	}
	// The audit row says who and what, and holds no secret.
	rows := f.auditActions(t, "people.password_reset_issued")
	if len(rows) != 1 || !strings.Contains(rows[0], `"delivery": "link"`) {
		t.Fatalf("audit rows = %v", rows)
	}
	var everything string
	if err := f.pool.QueryRow(context.Background(), `SELECT string_agg(row_to_json(a)::text, ' ') FROM admin_audit_events a`).Scan(&everything); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{token, resetURL, "reset-password", "north-member@example.test"} {
		if strings.Contains(everything, secret) {
			t.Fatalf("audit ledger contains %q", secret)
		}
	}

	// The same target again inside two minutes is rate limited.
	limited := reset(f.ownerPass, f.memberID, before, "link")
	assertError(t, limited, http.StatusTooManyRequests, "rate_limited", "second reset inside the window")
	if limited.Header().Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}

	// Once the window passes, a new link replaces the old one.
	f.ageAudit(t)
	second := assertStatus(t, reset(f.ownerPass, f.memberID, before, "link"), http.StatusCreated, "second reset")
	if second["reset"].(map[string]any)["reset_url"] == resetURL {
		t.Error("the second link is the first")
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM password_reset_tokens WHERE user_id=$1`, f.memberID).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("links after a second issue = %d, %v", stored, err)
	}

	// Delivery that this server cannot make is refused before the link is
	// replaced.
	var digest string
	if err := f.pool.QueryRow(context.Background(), `SELECT token_hash FROM password_reset_tokens WHERE user_id=$1`, f.memberID).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	f.ageAudit(t)
	refused := assertError(t, reset(f.ownerPass, f.memberID, before, "email"), http.StatusConflict, "capability_not_configured", "email without a mail server")
	if refused["capability"] != "email" {
		t.Errorf("capability = %v", refused["capability"])
	}
	var after string
	if err := f.pool.QueryRow(context.Background(), `SELECT token_hash FROM password_reset_tokens WHERE user_id=$1`, f.memberID).Scan(&after); err != nil || after != digest {
		t.Errorf("a refused delivery replaced the live link (%v)", err)
	}

	// Authority: who may reset whom. Refusals are 409, never 403.
	f.ageAudit(t)
	ownerRev := f.revision(t, f.ownerID)
	body = assertError(t, reset(f.adminPass, f.ownerID, ownerRev, "link"), http.StatusConflict, "target_protected", "administrator resets the owner")
	if body["reason"] != "organization_owner" {
		t.Errorf("reason = %v", body["reason"])
	}
	adminRev := f.revision(t, f.adminID)
	body = assertError(t, reset(f.adminPass, f.adminID, adminRev, "link"), http.StatusConflict, "target_protected", "administrator resets themselves")
	if body["reason"] != "self" {
		t.Errorf("reason = %v", body["reason"])
	}
	// Refusals count against the per-person budget, so let the window pass.
	f.ageAudit(t)
	assertStatus(t, reset(f.adminPass, f.otherMemberID, f.revision(t, f.otherMemberID), "link"), http.StatusCreated, "administrator resets a member")
	assertStatus(t, reset(f.ownerPass, f.adminID, adminRev, "link"), http.StatusCreated, "owner resets another administrator")
	assertError(t, reset(f.ownerPass, f.ownerID, ownerRev, "link"), http.StatusConflict, "target_protected", "owner resets themselves")
	// The email without a mail server, the owner and the administrator's own
	// account before the window passed, and the owner's own account after.
	if got := len(f.auditActions(t, "people.password_reset_refused")); got != 4 {
		t.Errorf("refusal audit rows = %d, want 4", got)
	}
}

func TestSupportPasswordResetRequestShape(t *testing.T) {
	f := newSupportFixture(t)
	f.setting(t, "server.public_url", "https://watch.example.test")
	member := f.memberID
	revision := f.revision(t, member)
	path := fmt.Sprintf("%d/password-reset", member)

	assertError(t, f.call(t, http.MethodPost, path, fmt.Sprintf(`{"expected_revision":%d,"delivery":"link","organization_id":"%s"}`, revision, f.organizationIDs[1]), f.ownerPass), http.StatusBadRequest, "invalid_request", "stray organization_id")
	body := assertError(t, f.call(t, http.MethodPost, path, `{"expected_revision":0,"delivery":"smoke"}`, f.ownerPass), http.StatusUnprocessableEntity, "validation_failed", "bad fields")
	fields := body["fields"].(map[string]any)
	if fields["delivery"] == nil || fields["expected_revision"] == nil {
		t.Errorf("fields = %v", fields)
	}
	body = assertError(t, f.call(t, http.MethodPost, path, fmt.Sprintf(`{"expected_revision":%d,"delivery":"link"}`, revision+7), f.ownerPass), http.StatusConflict, "authorization_state_changed", "stale revision")
	if body["current_revision"] != float64(revision) {
		t.Errorf("current_revision = %v, want %d", body["current_revision"], revision)
	}

	// An account of another organization is the same 404 as one that does not exist.
	foreign := f.call(t, http.MethodPost, fmt.Sprintf("%d/password-reset", f.foreignID), `{"expected_revision":1,"delivery":"link"}`, f.ownerPass)
	missing := f.call(t, http.MethodPost, "987654/password-reset", `{"expected_revision":1,"delivery":"link"}`, f.ownerPass)
	assertError(t, foreign, http.StatusNotFound, "not_found", "foreign account")
	if foreign.Body.String() != missing.Body.String() || foreign.Code != missing.Code {
		t.Errorf("foreign %q and missing %q answers differ", foreign.Body.String(), missing.Body.String())
	}

	// A suspended member has nothing to reset.
	if _, err := f.pool.Exec(context.Background(), `UPDATE organization_memberships SET status='suspended', security_revision=security_revision+1 WHERE account_id=$1`, f.otherMemberID); err != nil {
		t.Fatal(err)
	}
	assertError(t, f.call(t, http.MethodPost, fmt.Sprintf("%d/password-reset", f.otherMemberID), fmt.Sprintf(`{"expected_revision":%d,"delivery":"link"}`, f.revision(t, f.otherMemberID)), f.ownerPass), http.StatusConflict, "membership_not_active", "suspended member")
	// An account that signs in through a provider has no password.
	if _, err := f.pool.Exec(context.Background(), `UPDATE users SET local_password_login_enabled=false WHERE id=$1`, f.memberID); err != nil {
		t.Fatal(err)
	}
	f.ageAudit(t)
	assertError(t, f.call(t, http.MethodPost, path, fmt.Sprintf(`{"expected_revision":%d,"delivery":"link"}`, revision), f.ownerPass), http.StatusConflict, "password_login_disabled", "provider account")
	// A link needs the server's public address.
	if _, err := f.pool.Exec(context.Background(), `UPDATE users SET local_password_login_enabled=true WHERE id=$1`, f.memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(context.Background(), `DELETE FROM server_settings WHERE key='server.public_url'`); err != nil {
		t.Fatal(err)
	}
	f.ageAudit(t)
	body = assertError(t, f.call(t, http.MethodPost, path, fmt.Sprintf(`{"expected_revision":%d,"delivery":"link"}`, revision), f.ownerPass), http.StatusConflict, "capability_not_configured", "no public address")
	if body["capability"] != "link" {
		t.Errorf("capability = %v", body["capability"])
	}
}

func (f *supportFixture) addSession(t *testing.T, id string, accountID int, deviceID string, impersonator int) {
	t.Helper()
	var impersonatorID any
	if impersonator > 0 {
		impersonatorID = impersonator
	}
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO auth_sessions (id,user_id,device_name,device_id,ip_address,expires_at,impersonator_user_id,impersonation_started_at) VALUES ($1,$2,'Living room TV',$3,'192.0.2.7',now()+interval '1 day',$4,CASE WHEN $4::int IS NULL THEN NULL ELSE now() END)`, id, accountID, deviceID, impersonatorID); err != nil {
		t.Fatal(err)
	}
}

func (f *supportFixture) foreignProfile(t *testing.T) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM user_profiles WHERE user_id=$1 LIMIT 1`, f.foreignID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *supportFixture) sessionRevoked(t *testing.T, id string) bool {
	t.Helper()
	var revoked bool
	if err := f.pool.QueryRow(context.Background(), `SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id=$1`, id).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	return revoked
}

//nolint:gocyclo // one linear acceptance walk through the session and device routes
func TestSupportSessionsAndDevicesAcceptance(t *testing.T) {
	f := newSupportFixture(t)
	f.addSession(t, "s-tv", f.memberID, "device-tv", 0)
	f.addSession(t, "s-phone", f.memberID, "device-phone", 0)
	f.addSession(t, "s-support", f.memberID, "device-support", f.ownerID)
	f.addSession(t, "s-other", f.otherMemberID, "device-tv", 0)
	member := strconv.Itoa(f.memberID)

	listed := assertStatus(t, f.call(t, http.MethodGet, member+"/sessions", "", f.ownerPass), http.StatusOK, "list sessions")
	sessions := listed["sessions"].([]any)
	if len(sessions) != 2 {
		t.Fatalf("listed sessions = %v, want the two ordinary ones", sessions)
	}
	for _, raw := range sessions {
		session := raw.(map[string]any)
		for key := range session {
			if strings.Contains(key, "ip") || strings.Contains(key, "impersonat") {
				t.Errorf("session exposes %q", key)
			}
		}
	}
	if strings.Contains(f.call(t, http.MethodGet, member+"/sessions", "", f.ownerPass).Body.String(), "192.0.2.7") {
		t.Error("an IP address leaked into the session list")
	}

	// One session: no revision change, the others stay.
	revision := f.revision(t, f.memberID)
	assertStatus(t, f.call(t, http.MethodDelete, member+"/sessions/s-tv", "", f.ownerPass), http.StatusNoContent, "revoke one session")
	if !f.sessionRevoked(t, "s-tv") || f.sessionRevoked(t, "s-phone") || f.revision(t, f.memberID) != revision {
		t.Error("revoking one session touched another session or the revision")
	}
	assertStatus(t, f.call(t, http.MethodDelete, member+"/sessions/s-tv", "", f.ownerPass), http.StatusNoContent, "revoke an ended session")
	assertError(t, f.call(t, http.MethodDelete, member+"/sessions/s-support", "", f.ownerPass), http.StatusNotFound, "not_found", "impersonation session")
	assertError(t, f.call(t, http.MethodDelete, member+"/sessions/s-other", "", f.ownerPass), http.StatusNotFound, "not_found", "another person's session")
	assertError(t, f.call(t, http.MethodDelete, member+"/sessions/nothing", "", f.ownerPass), http.StatusNotFound, "not_found", "unknown session")
	if f.sessionRevoked(t, "s-other") || f.sessionRevoked(t, "s-support") {
		t.Fatal("a refused revoke ended a session")
	}

	// Devices.
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO user_devices (user_id,profile_id,device_id,device_name,device_platform) VALUES ($1,$2,'device-phone','Pixel','android'),($1,$3,'device-phone','Pixel','android')`, f.memberID, f.memberProfile, f.kidProfile); err != nil {
		t.Fatal(err)
	}
	devices := assertStatus(t, f.call(t, http.MethodGet, member+"/devices", "", f.ownerPass), http.StatusOK, "list devices")["devices"].([]any)
	if len(devices) != 1 || len(devices[0].(map[string]any)["profiles"].([]any)) != 2 {
		t.Fatalf("devices = %v", devices)
	}
	unknownDevice := assertError(t, f.call(t, http.MethodDelete, member+"/devices/device-unknown", "", f.ownerPass), http.StatusNotFound, "not_found", "a device nobody has")
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO user_devices (user_id,profile_id,device_id,device_name,device_platform) VALUES ($1,$2,'device-other','Other','android')`, f.foreignID, f.foreignProfile(t)); err != nil {
		t.Fatal(err)
	}
	otherDevice := f.call(t, http.MethodDelete, member+"/devices/device-other", "", f.ownerPass)
	assertError(t, otherDevice, http.StatusNotFound, "not_found", "another person's device")
	if otherDevice.Body.String() != f.call(t, http.MethodDelete, member+"/devices/device-unknown", "", f.ownerPass).Body.String() || unknownDevice["message"] == nil {
		t.Error("a device that belongs to someone else is distinguishable from one that does not exist")
	}
	assertStatus(t, f.call(t, http.MethodDelete, member+"/devices/device-phone", "", f.ownerPass), http.StatusNoContent, "sign out a device")
	var registered int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM user_devices WHERE user_id=$1`, f.memberID).Scan(&registered); err != nil || registered != 0 {
		t.Errorf("device registrations left = %d (%v)", registered, err)
	}
	if !f.sessionRevoked(t, "s-phone") || f.revision(t, f.memberID) != revision {
		t.Error("device sign-out missed its session or raised the revision")
	}

	// Everywhere: needs the current revision, raises it, ends everything.
	assertError(t, f.call(t, http.MethodDelete, member+"/sessions", fmt.Sprintf(`{"expected_revision":%d}`, revision+3), f.ownerPass), http.StatusConflict, "authorization_state_changed", "stale revision")
	f.addSession(t, "s-late", f.memberID, "device-late", 0)
	result := assertStatus(t, f.call(t, http.MethodDelete, member+"/sessions", fmt.Sprintf(`{"expected_revision":%d}`, revision), f.ownerPass), http.StatusOK, "sign out everywhere")
	if got := f.revision(t, f.memberID); got != revision+1 || result["person"].(map[string]any)["security_revision"] != float64(revision+1) {
		t.Errorf("revision after everywhere = %d, want %d", got, revision+1)
	}
	if !f.sessionRevoked(t, "s-late") || !f.sessionRevoked(t, "s-support") {
		t.Error("sign out everywhere left a session alive (impersonation sessions die with it)")
	}
	// Raising the revision records the organization's revocation, which is what
	// ends the member's older account-login sessions at their next profile pick.
	var revocations int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM bloem_org_session_revocations WHERE account_id=$1 AND organization_id=$2`, f.memberID, f.organizationIDs[0]).Scan(&revocations); err != nil || revocations != 1 {
		t.Errorf("revocation records after signing out everywhere = %d (%v), want 1", revocations, err)
	}
	if f.sessionRevoked(t, "s-other") {
		t.Error("sign out everywhere reached another person")
	}
	if got := len(f.auditActions(t, "people.sessions_revoked")); got != 1 {
		t.Errorf("sessions_revoked audit rows = %d", got)
	}

	// Authority: the second administrator may not do this to the owner or to themselves.
	for account, reason := range map[int]string{f.ownerID: "organization_owner", f.adminID: "self"} {
		body := assertError(t, f.call(t, http.MethodDelete, fmt.Sprintf("%d/sessions", account), fmt.Sprintf(`{"expected_revision":%d}`, f.revision(t, account)), f.adminPass), http.StatusConflict, "target_protected", "protected sign-out")
		if body["reason"] != reason {
			t.Errorf("reason = %v, want %s", body["reason"], reason)
		}
	}
}

func TestSupportProfilesAcceptance(t *testing.T) {
	f := newSupportFixture(t)
	member := strconv.Itoa(f.memberID)
	revision := f.revision(t, f.memberID)

	person := assertStatus(t, f.call(t, http.MethodGet, member+"/", "", f.ownerPass), http.StatusOK, "person")["person"].(map[string]any)
	for _, raw := range person["profiles"].([]any) {
		profile := raw.(map[string]any)
		if profile["revision"] == nil || profile["has_pin"] == nil || profile["is_primary"] == nil {
			t.Fatalf("profile summary lacks revision, has_pin or is_primary: %v", profile)
		}
	}

	// Add.
	created := assertStatus(t, f.call(t, http.MethodPost, member+"/profiles", `{"name":"  Guest  "}`, f.ownerPass), http.StatusCreated, "add profile")
	guest := created["profile"].(map[string]any)
	guestID := guest["id"].(string)
	if guest["name"] != "Guest" || guest["is_primary"] != false || int(guest["group_id"].(float64)) != f.groupID {
		t.Fatalf("created profile = %v", guest)
	}
	assertError(t, f.call(t, http.MethodPost, member+"/profiles", `{"name":"guest"}`, f.ownerPass), http.StatusConflict, "name_conflict", "duplicate name")
	assertError(t, f.call(t, http.MethodPost, member+"/profiles", `{"name":""}`, f.ownerPass), http.StatusUnprocessableEntity, "validation_failed", "empty name")
	var foreignGroup int
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM access_groups WHERE organization_id=$1 LIMIT 1`, f.organizationIDs[1]).Scan(&foreignGroup); err != nil {
		t.Fatal(err)
	}
	assertError(t, f.call(t, http.MethodPost, member+"/profiles", fmt.Sprintf(`{"name":"Elsewhere","group_id":%d}`, foreignGroup), f.ownerPass), http.StatusUnprocessableEntity, "validation_failed", "another organization's group")
	if _, err := f.pool.Exec(context.Background(), `UPDATE users SET max_profiles=3 WHERE id=$1`, f.memberID); err != nil {
		t.Fatal(err)
	}
	assertError(t, f.call(t, http.MethodPost, member+"/profiles", `{"name":"Fourth"}`, f.ownerPass), http.StatusUnprocessableEntity, "profile_limit_reached", "profile limit")
	if f.revision(t, f.memberID) != revision {
		t.Error("adding a profile changed the security revision")
	}

	// Rename and clear PIN use the profile's own revision and leave the member signed in.
	f.addSession(t, "s-member", f.memberID, "device-1", 0)
	if _, err := f.pool.Exec(context.Background(), `UPDATE user_profiles SET pin_hash='not-a-real-hash' WHERE id=$1`, f.memberProfile); err != nil {
		t.Fatal(err)
	}
	kidRevision := f.profileRevision(t, f.kidProfile)
	body := assertStatus(t, f.call(t, http.MethodPatch, fmt.Sprintf("%s/profiles/%s/details", member, f.kidProfile), fmt.Sprintf(`{"expected_revision":%d,"name":"Junior"}`, kidRevision), f.ownerPass), http.StatusOK, "rename")
	if body["profile"].(map[string]any)["name"] != "Junior" || f.profileRevision(t, f.kidProfile) != kidRevision+1 {
		t.Errorf("rename result = %v, revision %d", body["profile"], f.profileRevision(t, f.kidProfile))
	}
	stale := assertError(t, f.call(t, http.MethodPatch, fmt.Sprintf("%s/profiles/%s/details", member, f.kidProfile), fmt.Sprintf(`{"expected_revision":%d,"name":"Again"}`, kidRevision), f.ownerPass), http.StatusConflict, "authorization_state_changed", "stale profile revision")
	if stale["current_revision"] != float64(kidRevision+1) {
		t.Errorf("current_revision = %v", stale["current_revision"])
	}
	assertError(t, f.call(t, http.MethodPatch, fmt.Sprintf("%s/profiles/%s/details", member, f.kidProfile), fmt.Sprintf(`{"expected_revision":%d,"name":"primary"}`, kidRevision+1), f.ownerPass), http.StatusConflict, "name_conflict", "rename onto another profile")

	primaryRevision := f.profileRevision(t, f.memberProfile)
	cleared := assertStatus(t, f.call(t, http.MethodDelete, fmt.Sprintf("%s/profiles/%s/pin", member, f.memberProfile), fmt.Sprintf(`{"expected_revision":%d}`, primaryRevision), f.ownerPass), http.StatusOK, "clear the primary profile's PIN")
	if cleared["profile"].(map[string]any)["has_pin"] != false {
		t.Errorf("profile still has a PIN: %v", cleared["profile"])
	}
	// Refusals count against the per-person budget of six, so let the window pass.
	f.ageAudit(t)
	assertStatus(t, f.call(t, http.MethodDelete, fmt.Sprintf("%s/profiles/%s/pin", member, f.memberProfile), fmt.Sprintf(`{"expected_revision":%d}`, f.profileRevision(t, f.memberProfile)), f.ownerPass), http.StatusOK, "clear a PIN twice")
	if rows := f.auditActions(t, "people.profile_pin_cleared"); len(rows) != 2 || !strings.Contains(rows[0], `"primary": true`) || strings.Contains(strings.Join(rows, ""), "not-a-real-hash") {
		t.Errorf("PIN audit rows = %v", rows)
	}
	if f.sessionRevoked(t, "s-member") || f.revision(t, f.memberID) != revision {
		t.Error("renaming a profile or clearing a PIN signed the member out")
	}
	var revocations int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM bloem_org_session_revocations WHERE account_id=$1`, f.memberID).Scan(&revocations); err != nil || revocations != 0 {
		t.Errorf("a rename or a PIN clear recorded an organization revocation (%d, %v)", revocations, err)
	}

	// Remove.
	f.ageAudit(t)
	assertError(t, f.call(t, http.MethodDelete, fmt.Sprintf("%s/profiles/%s", member, f.memberProfile), fmt.Sprintf(`{"expected_revision":%d}`, f.profileRevision(t, f.memberProfile)), f.ownerPass), http.StatusConflict, "primary_profile_protected", "remove the primary profile")
	assertStatus(t, f.call(t, http.MethodDelete, fmt.Sprintf("%s/profiles/%s", member, guestID), fmt.Sprintf(`{"expected_revision":%d}`, f.profileRevision(t, guestID)), f.ownerPass), http.StatusNoContent, "remove a profile")
	var remaining int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM user_profiles WHERE id=$1`, guestID).Scan(&remaining); err != nil || remaining != 0 {
		t.Errorf("removed profile still exists (%d, %v)", remaining, err)
	}
	if got := len(f.auditActions(t, "people.profile_removed")); got != 1 {
		t.Errorf("profile_removed audit rows = %d", got)
	}

	// Other organizations' and other people's profiles are the same 404.
	var foreignProfile string
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM user_profiles WHERE user_id=$1 LIMIT 1`, f.foreignID).Scan(&foreignProfile); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		fmt.Sprintf("%s/profiles/%s/details", member, foreignProfile),
		fmt.Sprintf("%s/profiles/%s/details", member, uuid.NewString()),
	} {
		assertError(t, f.call(t, http.MethodPatch, path, `{"expected_revision":1,"name":"Nope"}`, f.ownerPass), http.StatusNotFound, "not_found", "profile outside the person")
	}

	// Authority: the second administrator may not touch the owner's profile.
	ownerProfile := ""
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM user_profiles WHERE user_id=$1`, f.ownerID).Scan(&ownerProfile); err != nil {
		t.Fatal(err)
	}
	protected := assertError(t, f.call(t, http.MethodPatch, fmt.Sprintf("%d/profiles/%s/details", f.ownerID, ownerProfile), `{"expected_revision":1,"name":"Boss"}`, f.adminPass), http.StatusConflict, "target_protected", "administrator renames the owner's profile")
	if protected["reason"] != "organization_owner" {
		t.Errorf("reason = %v", protected["reason"])
	}
}

func TestSupportRoutesRejectOtherPasses(t *testing.T) {
	f := newSupportFixture(t)
	member := strconv.Itoa(f.memberID)
	// Another organization's administrator reaches nothing of this organization.
	canal := f.organizationTokens[1]
	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, member + "/sessions", ""},
		{http.MethodGet, member + "/devices", ""},
		{http.MethodPost, member + "/password-reset", `{"expected_revision":1,"delivery":"link"}`},
		{http.MethodPost, member + "/profiles", `{"name":"x"}`},
	} {
		response := f.call(t, call.method, call.path, call.body, canal)
		assertError(t, response, http.StatusNotFound, "not_found", call.method+" "+call.path+" from another organization")
	}
	// No pass at all.
	if response := f.call(t, http.MethodGet, member+"/sessions", "", ""); response.Code != http.StatusUnauthorized {
		t.Errorf("no pass = %d", response.Code)
	}
	// A suspended member who held a pass loses it.
	if _, err := f.pool.Exec(context.Background(), `UPDATE organization_memberships SET status='suspended',security_revision=security_revision+1 WHERE account_id=$1`, f.adminID); err != nil {
		t.Fatal(err)
	}
	if response := f.call(t, http.MethodGet, member+"/sessions", "", f.adminPass); response.Code == http.StatusOK {
		t.Errorf("a suspended administrator's pass still worked: %s", response.Body.String())
	}
}

// recordingMail is a mail sender that keeps what it was asked to send.
type recordingMail struct {
	mu     sync.Mutex
	sent   []mail.Message
	fail   error
	signal chan struct{}
}

func (m *recordingMail) Enabled(context.Context) bool { return true }

func (m *recordingMail) Send(_ context.Context, message mail.Message) error {
	m.mu.Lock()
	m.sent = append(m.sent, message)
	fail := m.fail
	m.mu.Unlock()
	if m.signal != nil {
		select {
		case m.signal <- struct{}{}:
		default:
		}
	}
	return fail
}

func (m *recordingMail) messages() []mail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mail.Message(nil), m.sent...)
}

// waitFor blocks until count messages were sent: the notice goes out on a
// background goroutine, so the response can arrive first.
func (m *recordingMail) waitFor(t *testing.T, count int) []mail.Message {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if got := m.messages(); len(got) >= count {
			return got
		}
		select {
		case <-m.signal:
		case <-deadline:
			t.Fatalf("mail sent = %d messages, want %d", len(m.messages()), count)
		}
	}
}

func TestSupportPasswordResetMail(t *testing.T) {
	sender := &recordingMail{signal: make(chan struct{}, 16)}
	f := newSupportFixtureWithMail(t, sender)
	f.setting(t, "server.public_url", "https://watch.example.test")
	member := strconv.Itoa(f.memberID)
	revision := f.revision(t, f.memberID)

	// Email: the member gets the link; the administrator never sees it.
	response := f.call(t, http.MethodPost, member+"/password-reset", fmt.Sprintf(`{"expected_revision":%d,"delivery":"email"}`, revision), f.ownerPass)
	body := assertStatus(t, response, http.StatusCreated, "email reset")
	issued := body["reset"].(map[string]any)
	if issued["delivery_status"] != "sent" || issued["reset_url"] != nil {
		t.Fatalf("email reset = %v", issued)
	}
	messages := sender.waitFor(t, 1)
	if len(messages) != 1 || messages[0].To[0] != "north-member@example.test" || !strings.Contains(messages[0].TextBody, "https://watch.example.test/reset-password/") {
		t.Fatalf("messages = %+v", messages)
	}
	expires, _ := time.Parse(time.RFC3339, issued["expires_at"].(string))
	if until := time.Until(expires); until < 23*time.Hour {
		t.Errorf("email link lifetime = %s, want 24h", until)
	}
	if strings.Contains(response.Body.String(), "reset-password/") {
		t.Error("the response to an email reset carries the link")
	}

	// A link handed over by hand also tells the member, without the link.
	f.ageAudit(t)
	linked := assertStatus(t, f.call(t, http.MethodPost, member+"/password-reset", fmt.Sprintf(`{"expected_revision":%d,"delivery":"link"}`, revision), f.ownerPass), http.StatusCreated, "link reset")
	link := linked["reset"].(map[string]any)["reset_url"].(string)
	messages = sender.waitFor(t, 2)
	notice := messages[1]
	if notice.To[0] != "north-member@example.test" || strings.Contains(notice.TextBody+notice.HTMLBody, "reset-password") || strings.Contains(notice.TextBody+notice.HTMLBody, link) {
		t.Errorf("the notice carries a link or goes to the wrong address: %+v", notice)
	}
	if !strings.Contains(notice.TextBody, "North Sea Media") {
		t.Errorf("the notice does not name the organization: %q", notice.TextBody)
	}

	// A send that fails or is uncertain still leaves a live link and a 201.
	sender.mu.Lock()
	sender.fail = errors.New("smtp timeout")
	sender.mu.Unlock()
	f.ageAudit(t)
	failed := assertStatus(t, f.call(t, http.MethodPost, member+"/password-reset", fmt.Sprintf(`{"expected_revision":%d,"delivery":"email"}`, revision), f.ownerPass), http.StatusCreated, "failed email")
	if failed["reset"].(map[string]any)["delivery_status"] != "failed_or_unknown" {
		t.Errorf("delivery_status = %v", failed["reset"])
	}
	var live int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM password_reset_tokens WHERE user_id=$1 AND expires_at > now()`, f.memberID).Scan(&live); err != nil || live != 1 {
		t.Errorf("live links = %d (%v)", live, err)
	}

	// An account with no address cannot take an emailed link, and its current
	// link is left alone.
	if _, err := f.pool.Exec(context.Background(), `UPDATE users SET email='' WHERE id=$1`, f.otherMemberID); err != nil {
		t.Fatal(err)
	}
	assertError(t, f.call(t, http.MethodPost, fmt.Sprintf("%d/password-reset", f.otherMemberID), fmt.Sprintf(`{"expected_revision":%d,"delivery":"email"}`, f.revision(t, f.otherMemberID)), f.ownerPass), http.StatusConflict, "email_unavailable", "no address")
}

func TestSupportRefusesAccountsSharedBetweenOrganizations(t *testing.T) {
	f := newSupportFixture(t)
	ctx := context.Background()
	// An account belongs to one organization today. If that is ever relaxed, a
	// password reset or a sign-out everywhere must not cross into the member's
	// other organization.
	if _, err := f.pool.Exec(ctx, `ALTER TABLE organization_memberships DROP CONSTRAINT organization_memberships_one_per_account`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','user')`, f.organizationIDs[1], f.memberID); err != nil {
		t.Fatal(err)
	}
	f.setting(t, "server.public_url", "https://watch.example.test")
	f.addSession(t, "s-shared", f.memberID, "device-shared", 0)
	member := strconv.Itoa(f.memberID)
	var revision int64
	if err := f.pool.QueryRow(ctx, `SELECT security_revision FROM organization_memberships WHERE account_id=$1 AND organization_id=$2`, f.memberID, f.organizationIDs[0]).Scan(&revision); err != nil {
		t.Fatal(err)
	}

	assertError(t, f.call(t, http.MethodPost, member+"/password-reset", fmt.Sprintf(`{"expected_revision":%d,"delivery":"link"}`, revision), f.ownerPass), http.StatusConflict, "account_shared", "reset a shared account")
	assertError(t, f.call(t, http.MethodDelete, member+"/sessions", fmt.Sprintf(`{"expected_revision":%d}`, revision), f.ownerPass), http.StatusConflict, "account_shared", "sign a shared account out everywhere")
	assertError(t, f.call(t, http.MethodGet, member+"/sessions", "", f.ownerPass), http.StatusConflict, "account_shared", "list a shared account's sessions")
	assertError(t, f.call(t, http.MethodDelete, member+"/sessions/s-shared", "", f.ownerPass), http.StatusConflict, "account_shared", "revoke a shared account's session")
	if f.sessionRevoked(t, "s-shared") {
		t.Error("a refused sign-out ended a session")
	}
	var links int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM password_reset_tokens WHERE user_id=$1`, f.memberID).Scan(&links); err != nil || links != 0 {
		t.Errorf("a refused reset stored a link (%d, %v)", links, err)
	}
}

func TestSupportWriteAndAuditCommitTogether(t *testing.T) {
	f := newSupportFixture(t)
	ctx := context.Background()
	f.addSession(t, "s-atomic", f.memberID, "device-atomic", 0)
	revision := f.revision(t, f.memberID)
	// Make the audit insert fail after the sign-out has been written.
	for _, statement := range []string{
		`CREATE FUNCTION fail_support_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$`,
		`CREATE TRIGGER fail_support_audit BEFORE INSERT ON admin_audit_events FOR EACH ROW WHEN (NEW.action = 'people.sessions_revoked') EXECUTE FUNCTION fail_support_audit()`,
	} {
		if _, err := f.pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	response := f.call(t, http.MethodDelete, fmt.Sprintf("%d/sessions", f.memberID), fmt.Sprintf(`{"expected_revision":%d}`, revision), f.ownerPass)
	assertError(t, response, http.StatusServiceUnavailable, "tenant_unavailable", "sign-out with a failing audit")
	if f.sessionRevoked(t, "s-atomic") || f.revision(t, f.memberID) != revision {
		t.Error("the sign-out committed without its audit event")
	}
}

func TestSupportPromotionIsSeenByTheNextAction(t *testing.T) {
	f := newSupportFixture(t)
	f.setting(t, "server.public_url", "https://watch.example.test")
	member := f.otherMemberID
	// The second administrator may reset an ordinary member ...
	assertStatus(t, f.call(t, http.MethodPost, fmt.Sprintf("%d/password-reset", member), fmt.Sprintf(`{"expected_revision":%d,"delivery":"link"}`, f.revision(t, member)), f.adminPass), http.StatusCreated, "reset before promotion")
	// ... and the link does not survive the member's promotion to administrator.
	if _, err := f.pool.Exec(context.Background(), `UPDATE organization_memberships SET legacy_role='admin', security_revision=security_revision+1 WHERE account_id=$1`, member); err != nil {
		t.Fatal(err)
	}
	var links int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM password_reset_tokens WHERE user_id=$1`, member).Scan(&links); err != nil || links != 0 {
		t.Fatalf("links after promotion = %d (%v)", links, err)
	}
	f.ageAudit(t)
	body := assertError(t, f.call(t, http.MethodPost, fmt.Sprintf("%d/password-reset", member), fmt.Sprintf(`{"expected_revision":%d,"delivery":"link"}`, f.revision(t, member)), f.adminPass), http.StatusConflict, "target_protected", "reset after promotion")
	if body["reason"] != "organization_admin" {
		t.Errorf("reason = %v", body["reason"])
	}
	// The owner still may, once the refusal's rate window has passed.
	f.ageAudit(t)
	assertStatus(t, f.call(t, http.MethodPost, fmt.Sprintf("%d/password-reset", member), fmt.Sprintf(`{"expected_revision":%d,"delivery":"link"}`, f.revision(t, member)), f.ownerPass), http.StatusCreated, "owner resets the promoted member")
}
