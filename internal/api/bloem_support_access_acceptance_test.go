package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// accessFixture is the support fixture plus the people who matter to
// break-glass access: the server Owner, who belongs to no customer
// organization, and a platform administrator who is not the Owner.
type accessFixture struct {
	*supportFixture
	ownerAccount, otherAdmin int
}

func newAccessFixture(t *testing.T) *accessFixture {
	t.Helper()
	f := &accessFixture{supportFixture: newSupportFixture(t)}
	hash, err := bcrypt.GenerateFromPassword([]byte(supportPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := f.pool.QueryRow(ctx, `INSERT INTO users (username,email,password_hash,role,enabled,is_owner) VALUES ('server-owner','server-owner@example.test',$1,'admin',true,true) RETURNING id`, string(hash)).Scan(&f.ownerAccount); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO users (username,email,password_hash,role,enabled) VALUES ('other-platform-admin','other-platform-admin@example.test',$1,'admin',true) RETURNING id`, string(hash)).Scan(&f.otherAdmin); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *accessFixture) login(t *testing.T, username string) string {
	t.Helper()
	login := performJSONRequest(t, f.router, http.MethodPost, "/api/v2/auth/login", `{"username":"`+username+`","password":"`+supportPassword+`"}`, "", nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login %s = %d %s", username, login.Code, login.Body.String())
	}
	return decodeLogin(t, login).AccessToken
}

func (f *accessFixture) mintSupport(t *testing.T, accountToken string, orgIndex int, support string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"scope":"organization","organization_id":"` + f.organizationIDs[orgIndex].String() + `"`
	if support != "" {
		body += `,"support":` + support
	}
	return performJSONRequest(t, f.router, http.MethodPost, NativeAPIPrefix+"/admin/session", body+`}`, accountToken, nil)
}

func (f *accessFixture) accessCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM bloem_support_access`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (f *accessFixture) org(t *testing.T, method, path, body, pass string) *httptest.ResponseRecorder {
	t.Helper()
	return performJSONRequest(t, f.router, method, NativeAPIPrefix+"/admin/organization/"+path, body, pass, nil)
}

const goodReason = `"reason":"Admin is locked out; investigating with them"`

func TestSupportAccessMint(t *testing.T) {
	f := newAccessFixture(t)
	owner := f.login(t, "server-owner")

	// Without a reason the Owner is told what is missing, but only for an
	// organization that exists and is active.
	body := assertError(t, f.mintSupport(t, owner, 0, `{}`), http.StatusUnprocessableEntity, "validation_failed", "no reason")
	if body["fields"].(map[string]any)["support.reason"] == nil {
		t.Errorf("fields = %v", body["fields"])
	}
	assertError(t, f.mintSupport(t, owner, 0, `{"reason":"too short"}`), http.StatusUnprocessableEntity, "validation_failed", "short reason")
	assertError(t, f.mintSupport(t, owner, 0, `{"reason":"`+strings.Repeat("x", 501)+`"}`), http.StatusUnprocessableEntity, "validation_failed", "long reason")
	assertError(t, f.mintSupport(t, owner, 0, `{"reason":"line one\nline two is here"}`), http.StatusUnprocessableEntity, "validation_failed", "control characters")
	if f.accessCount(t) != 0 {
		t.Fatal("a refused opening left a record")
	}
	// Without a support object the answer is today's: not found.
	noSupport := f.mintSupport(t, owner, 0, "")
	assertError(t, noSupport, http.StatusNotFound, "not_found", "no support object")

	// Everyone who may not learns nothing new: an ordinary account, a platform
	// administrator who is not the Owner, and an organization that is not there.
	member := f.login(t, "north-member")
	other := f.login(t, "other-platform-admin")
	for name, response := range map[string]*httptest.ResponseRecorder{
		"ordinary account":                f.mintSupport(t, member, 1, `{`+goodReason+`}`),
		"platform administrator no Owner": f.mintSupport(t, other, 0, `{`+goodReason+`}`),
		"nonexistent organization": performJSONRequest(t, f.router, http.MethodPost, NativeAPIPrefix+"/admin/session",
			`{"scope":"organization","organization_id":"11111111-1111-1111-1111-111111111111","support":{`+goodReason+`}}`, owner, nil),
	} {
		assertError(t, response, http.StatusNotFound, "not_found", name)
		if response.Body.String() != noSupport.Body.String() {
			t.Errorf("%s: %q differs from today's answer %q", name, response.Body.String(), noSupport.Body.String())
		}
	}
	if f.accessCount(t) != 0 {
		t.Fatal("a refused caller left a record")
	}

	// The Owner, with a reason: a pass with no membership.
	response := f.mintSupport(t, owner, 0, `{`+goodReason+`}`)
	opened := assertStatus(t, response, http.StatusOK, "open")
	summary := opened["context"].(map[string]any)
	if summary["access_basis"] != "support" || summary["authority"] != "platform_admin" || summary["support_access_id"] == nil || summary["support_expires_at"] == nil || summary["membership_id"] != nil {
		t.Fatalf("context = %v", summary)
	}
	expires, _ := time.Parse(time.RFC3339, summary["support_expires_at"].(string))
	if until := time.Until(expires); until > 4*time.Hour+time.Minute || until < 3*time.Hour+55*time.Minute {
		t.Errorf("access lifetime = %s, want 4h", until)
	}
	pass := opened["access_token"].(string)
	accessID := summary["support_access_id"].(string)
	if f.accessCount(t) != 1 {
		t.Fatalf("access records = %d", f.accessCount(t))
	}

	// A member administrator's mint is unchanged and ignores a support object.
	adminToken := f.login(t, "north-second-admin")
	memberMint := assertStatus(t, f.mintSupport(t, adminToken, 0, `{`+goodReason+`}`), http.StatusOK, "member administrator with support")
	if memberMint["context"].(map[string]any)["access_basis"] != "membership" || f.accessCount(t) != 1 {
		t.Errorf("a member's mint changed: %v, records %d", memberMint["context"], f.accessCount(t))
	}

	// Renewal: the same login session, with the access id and no reason.
	renewed := assertStatus(t, f.mintSupport(t, owner, 0, `{"access_id":"`+accessID+`"}`), http.StatusOK, "renew")
	if renewed["context"].(map[string]any)["support_access_id"] != accessID || f.accessCount(t) != 1 {
		t.Errorf("renewal opened a new access: %v", renewed["context"])
	}
	// ... which is refused to another login session, another operator, another
	// organization, an unknown id, and an id sent with a new reason.
	ownerAgain := f.login(t, "server-owner")
	for name, response := range map[string]*httptest.ResponseRecorder{
		"another login session": f.mintSupport(t, ownerAgain, 0, `{"access_id":"`+accessID+`"}`),
		"another organization":  f.mintSupport(t, owner, 1, `{"access_id":"`+accessID+`"}`),
		"unknown id":            f.mintSupport(t, owner, 0, `{"access_id":"22222222-2222-2222-2222-222222222222"}`),
		"id and reason":         f.mintSupport(t, owner, 0, `{"access_id":"`+accessID+`",`+goodReason+`}`),
	} {
		assertError(t, response, http.StatusNotFound, "not_found", name)
	}

	// The pass is an organization pass: it works on organization routes and is
	// refused on platform routes.
	assertStatus(t, f.org(t, http.MethodGet, "overview", "", pass), http.StatusOK, "overview")
	platform := performJSONRequest(t, f.router, http.MethodGet, NativeAPIPrefix+"/admin/platform/organizations", "", pass, nil)
	if platform.Code != http.StatusForbidden {
		t.Errorf("platform route with a support pass = %d %s", platform.Code, platform.Body.String())
	}

	// Closing ends the pass and the access.
	assertStatus(t, f.org(t, http.MethodDelete, "support-access", "", pass), http.StatusNoContent, "close")
	assertError(t, f.org(t, http.MethodGet, "overview", "", pass), http.StatusUnauthorized, "authorization_state_stale", "closed pass")
	assertError(t, f.mintSupport(t, owner, 0, `{"access_id":"`+accessID+`"}`), http.StatusNotFound, "not_found", "renew a closed access")
	if rows := f.auditActions(t, "support_access.closed"); len(rows) != 1 {
		t.Errorf("closed audit rows = %v", rows)
	}
	// A member's pass has nothing to close.
	assertError(t, f.org(t, http.MethodDelete, "support-access", "", f.ownerPass), http.StatusNotFound, "not_found", "close without an access")
}

func TestSupportAccessEnds(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	ownerToken := f.login(t, "server-owner")
	open := func() (pass, accessID string) {
		opened := assertStatus(t, f.mintSupport(t, ownerToken, 0, `{`+goodReason+`}`), http.StatusOK, "open")
		return opened["access_token"].(string), opened["context"].(map[string]any)["support_access_id"].(string)
	}

	// Past four hours.
	pass, accessID := open()
	assertStatus(t, f.org(t, http.MethodGet, "overview", "", pass), http.StatusOK, "before expiry")
	for _, statement := range []string{
		`ALTER TABLE bloem_support_access DISABLE TRIGGER bloem_support_access_append_only`,
		`UPDATE bloem_support_access SET opened_at = now() - interval '5 hours', expires_at = now() - interval '1 hour'`,
		`ALTER TABLE bloem_support_access ENABLE TRIGGER bloem_support_access_append_only`,
	} {
		if _, err := f.pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	assertError(t, f.org(t, http.MethodGet, "overview", "", pass), http.StatusUnauthorized, "authorization_state_stale", "expired access")
	assertError(t, f.mintSupport(t, ownerToken, 0, `{"access_id":"`+accessID+`"}`), http.StatusNotFound, "not_found", "renew past four hours")
	// A new reason opens a new access.
	pass, _ = open()
	assertStatus(t, f.org(t, http.MethodGet, "overview", "", pass), http.StatusOK, "new access")

	// The login session it was opened from ends: so does the pass.
	if _, err := f.pool.Exec(ctx, `UPDATE auth_sessions SET revoked_at = now() WHERE user_id = $1`, f.ownerAccount); err != nil {
		t.Fatal(err)
	}
	if response := f.org(t, http.MethodGet, "overview", "", pass); response.Code != http.StatusUnauthorized {
		t.Errorf("pass after its login session ended = %d %s", response.Code, response.Body.String())
	}

	// The operator stops qualifying: demoted, or no longer the Owner.
	ownerToken = f.login(t, "server-owner")
	pass, _ = open()
	if _, err := f.pool.Exec(ctx, `UPDATE users SET is_owner = false WHERE id = $1`, f.ownerAccount); err != nil {
		t.Fatal(err)
	}
	assertError(t, f.org(t, http.MethodGet, "overview", "", pass), http.StatusUnauthorized, "authorization_state_stale", "no longer the Owner")
	if _, err := f.pool.Exec(ctx, `UPDATE users SET is_owner = true WHERE id = $1`, f.ownerAccount); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, f.org(t, http.MethodGet, "overview", "", pass), http.StatusOK, "the Owner again")
	if _, err := f.pool.Exec(ctx, `UPDATE users SET enabled = false WHERE id = $1`, f.otherAdmin); err != nil {
		t.Fatal(err)
	}

	// A suspended organization stays suspended.
	if _, err := f.pool.Exec(ctx, `UPDATE organizations SET status='suspended', policy_revision=policy_revision+1 WHERE id=$1`, f.organizationIDs[1]); err != nil {
		t.Fatal(err)
	}
	assertError(t, f.mintSupport(t, ownerToken, 1, `{`+goodReason+`}`), http.StatusForbidden, "organization_suspended", "suspended organization")
}

func TestSupportAccessActsAsAnAdministratorAndIsVisible(t *testing.T) {
	f := newAccessFixture(t)
	f.setting(t, "server.public_url", "https://watch.example.test")
	ownerToken := f.login(t, "server-owner")
	opened := assertStatus(t, f.mintSupport(t, ownerToken, 0, `{"reason":"Their administrator is locked out"}`), http.StatusOK, "open")
	pass := opened["access_token"].(string)
	accessID := opened["context"].(map[string]any)["support_access_id"].(string)

	// Every organization route an administrator uses works without a membership.
	for _, path := range []string{
		"overview", "groups/", "libraries", "invitations", "activity", "policy-decisions", "entitlement-cohorts",
		"people/", "streams", "support-capabilities", fmt.Sprintf("people/%d/", f.memberID),
		fmt.Sprintf("people/%d/sessions", f.memberID), fmt.Sprintf("people/%d/devices", f.memberID),
	} {
		if response := f.org(t, http.MethodGet, path, "", pass); response.Code != http.StatusOK {
			t.Errorf("GET %s under support access = %d %s", path, response.Code, response.Body.String())
		}
	}

	// It may act on an administrator and on the organization owner, never on a
	// platform account or itself (it is not a person of the organization).
	member := f.memberID
	revision := f.revision(t, member)
	reset := f.org(t, http.MethodPost, fmt.Sprintf("people/%d/password-reset", member), fmt.Sprintf(`{"expected_revision":%d,"delivery":"link"}`, revision), pass)
	body := assertStatus(t, reset, http.StatusCreated, "reset a member under support access")
	if url := body["reset"].(map[string]any)["reset_url"].(string); !strings.HasPrefix(url, "https://watch.example.test/reset-password/") {
		t.Errorf("reset_url = %s", url)
	}
	f.ageAudit(t)
	assertStatus(t, f.org(t, http.MethodPost, fmt.Sprintf("people/%d/password-reset", f.ownerID), fmt.Sprintf(`{"expected_revision":%d,"delivery":"link"}`, f.revision(t, f.ownerID)), pass), http.StatusCreated, "reset the organization owner")
	f.ageAudit(t)
	assertError(t, f.org(t, http.MethodPost, fmt.Sprintf("people/%d/password-reset", f.ownerAccount), `{"expected_revision":1,"delivery":"link"}`, pass), http.StatusNotFound, "not_found", "the operator is no member")
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','user')`, f.organizationIDs[0], f.otherAdmin); err != nil {
		t.Fatal(err)
	}
	// The operator is the server Owner, whom Silo's Owner rule lets act on
	// another platform administrator (a non-Owner is refused; the matrix tests
	// cover that). It may not act on itself.
	assertStatus(t, f.org(t, http.MethodPost, fmt.Sprintf("people/%d/password-reset", f.otherAdmin), fmt.Sprintf(`{"expected_revision":%d,"delivery":"link"}`, f.revision(t, f.otherAdmin)), pass), http.StatusCreated, "the Owner resets another platform administrator")

	// Every write carries the access id.
	var withAccess, without int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE support_access_id = $1::uuid), count(*) FILTER (WHERE support_access_id IS NULL AND action LIKE 'people.%') FROM admin_audit_events WHERE actor_account_id = $2`, accessID, f.ownerAccount).Scan(&withAccess, &without); err != nil {
		t.Fatal(err)
	}
	if withAccess < 4 || without != 0 {
		t.Errorf("events under the access = %d, events of the operator without it = %d", withAccess, without)
	}

	// The organization's administrators see it in Activity: the opening with
	// its reason, the writes made under it, and never the operator's account.
	activity := assertStatus(t, f.org(t, http.MethodGet, "activity", "", f.ownerPass), http.StatusOK, "activity")
	var sawOpened, sawReset bool
	for _, raw := range activity["events"].([]any) {
		event := raw.(map[string]any)
		if event["actor_account_id"] == float64(f.ownerAccount) {
			t.Errorf("the operator's account id reached the customer: %v", event)
		}
		switch event["action"] {
		case "support_access.opened":
			sawOpened = event["actor_authority"] == "platform_support" && event["reason"] == "Their administrator is locked out" && event["support_access_id"] == accessID && event["actor_account_id"] == nil
		case "people.password_reset_issued":
			sawReset = event["actor_authority"] == "platform_support" && event["support_access_id"] == accessID
		}
	}
	if !sawOpened || !sawReset {
		t.Errorf("activity lacks the opening (%v) or the reset under it (%v): %v", sawOpened, sawReset, activity["events"])
	}
	// The organization's own actions stay attributed to its administrator.
	assertStatus(t, f.org(t, http.MethodPost, fmt.Sprintf("people/%d/profiles", f.otherMemberID), `{"name":"Guest"}`, f.ownerPass), http.StatusCreated, "owner adds a profile")
	activity = assertStatus(t, f.org(t, http.MethodGet, "activity", "", f.ownerPass), http.StatusOK, "activity")
	for _, raw := range activity["events"].([]any) {
		event := raw.(map[string]any)
		if event["action"] == "people.profile_created" && (event["actor_authority"] != "organization_admin" || event["actor_account_id"] != float64(f.ownerID)) {
			t.Errorf("an administrator's own event = %v", event)
		}
	}

	// An organization policy change makes the pass stale; a new mint under the
	// same access carries the new revision.
	if _, err := f.pool.Exec(context.Background(), `UPDATE organizations SET policy_revision = policy_revision + 1 WHERE id = $1`, f.organizationIDs[0]); err != nil {
		t.Fatal(err)
	}
	assertError(t, f.org(t, http.MethodGet, "overview", "", pass), http.StatusUnauthorized, "authorization_state_stale", "stale policy revision")
	renewed := assertStatus(t, f.mintSupport(t, ownerToken, 0, `{"access_id":"`+accessID+`"}`), http.StatusOK, "renew after a policy change")
	assertStatus(t, f.org(t, http.MethodGet, "overview", "", renewed["access_token"].(string)), http.StatusOK, "renewed pass")
}

func TestSupportAccessOpeningsAreRateLimited(t *testing.T) {
	f := newAccessFixture(t)
	ownerToken := f.login(t, "server-owner")
	for i := 0; i < 20; i++ {
		assertStatus(t, f.mintSupport(t, ownerToken, 0, fmt.Sprintf(`{"reason":"Investigating ticket number %d"}`, i)), http.StatusOK, "open in budget")
	}
	limited := f.mintSupport(t, ownerToken, 0, `{`+goodReason+`}`)
	assertError(t, limited, http.StatusTooManyRequests, "rate_limited", "the twenty-first opening")
	if limited.Header().Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}
	if f.accessCount(t) != 20 {
		t.Errorf("records = %d, want 20", f.accessCount(t))
	}
}
