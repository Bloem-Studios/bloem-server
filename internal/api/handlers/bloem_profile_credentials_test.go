package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type credentialManagerStub struct {
	reads, writes  int
	account        int
	profile, email string
	err            error
	revision       int64
	method         string
}

func (s *credentialManagerStub) Status(_ context.Context, account int, profile string) (auth.ProfileCredentialStatus, error) {
	s.reads++
	if account != 1 || profile != "owned" {
		return auth.ProfileCredentialStatus{}, auth.ErrProfileCredentialNotFound
	}
	return auth.ProfileCredentialStatus{ProfileID: profile, CredentialRevision: 2}, nil
}
func (s *credentialManagerStub) SetAtRevision(_ context.Context, account int, profile, email, _ string, revision int64) error {
	s.writes++
	s.account, s.profile, s.email = account, profile, email
	s.revision, s.method = revision, http.MethodPut
	return s.err
}
func (s *credentialManagerStub) ClearAtRevision(ctx context.Context, account int, profile string, revision int64) error {
	err := s.SetAtRevision(ctx, account, profile, "", "", revision)
	s.method = http.MethodDelete
	return err
}

func TestBloemProfileCredentialsAuthorityAndReauthentication(t *testing.T) {
	store := newEmptyProfileTestStore(t)
	for _, profile := range []userstore.Profile{{ID: "parent", Name: "Parent", IsPrimary: true}, {ID: "child", Name: "Child"}, {ID: "locked", Name: "Locked", IsPrimary: true, PINHash: "locked"}} {
		if err := store.CreateProfile(context.Background(), profile); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name, profile, target, session, method, body string
		tokenType, authMethod                        string
		impersonated, badPassword                    bool
		status, writes                               int
	}{
		{name: "save", profile: "parent", target: "owned", status: 204, writes: 1},
		{name: "clear", profile: "parent", target: "owned", method: "DELETE", status: 204, writes: 1},
		{name: "non-primary", profile: "child", target: "owned", status: 403},
		{name: "unverified PIN", profile: "locked", target: "owned", status: 403},
		{name: "direct-profile", profile: "parent", target: "owned", authMethod: auth.AuthMethodDirectProfile, status: 403},
		{name: "API key", profile: "parent", target: "owned", tokenType: "api_key", status: 403},
		{name: "impersonated", profile: "parent", target: "owned", impersonated: true, status: 403},
		{name: "no login session", profile: "parent", target: "owned", session: "missing", status: 403},
		{name: "foreign profile", profile: "parent", target: "foreign", status: 404},
		{name: "wrong account password", profile: "parent", target: "owned", badPassword: true, status: 403},
		{name: "invalid email", profile: "parent", target: "owned", body: `{"expected_revision":2,"current_password":"current","login_email":"not-an-email","password":"profile-password"}`, status: 422},
		{name: "short password", profile: "parent", target: "owned", body: `{"expected_revision":2,"current_password":"current","login_email":"reader@example.test","password":"x"}`, status: 422},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := &credentialManagerStub{}
			reauth := &adminReauthVerifierStub{allowed: !test.badPassword}
			h := &BloemProfileCredentialsHandler{profiles: NewProfileHandler(testUserStoreProvider{store: store}), credentials: manager, reauth: reauth}
			body := test.body
			if body == "" {
				body = `{"expected_revision":2,"current_password":"current","login_email":"reader@example.test","password":"profile-password"}`
			}
			method := test.method
			if method == "" {
				method = "PUT"
			}
			session := "session"
			if test.session == "missing" {
				session = ""
			}
			req := newAuthorizedProfileRequestWithSession(method, "/profile-credentials/"+test.target, body, "user", test.profile, session)
			claims := apimw.GetClaims(req.Context())
			claims.AuthMethod = test.authMethod
			if test.tokenType != "" {
				claims.TokenType = test.tokenType
			}
			if test.impersonated {
				actor := 99
				claims.ImpersonatorUserID = &actor
			}
			req = withProfileRouteParam(req, "id", test.target)
			rec := httptest.NewRecorder()
			h.HandleChange(rec, req)
			if rec.Code != test.status || manager.writes != test.writes {
				t.Fatalf("status=%d writes=%d body=%s", rec.Code, manager.writes, rec.Body.String())
			}
			if test.writes > 0 && (manager.account != 1 || manager.profile != "owned" || manager.revision != 2 || manager.method != method) {
				t.Fatalf("wrong mutation subject %+v", manager)
			}
			if test.name == "foreign profile" && reauth.calls > 0 {
				t.Fatal("foreign profile reached password verification")
			}
		})
	}
}
func TestBloemProfileCredentialsRequiresLoginForStatus(t *testing.T) {
	h := &BloemProfileCredentialsHandler{}
	rec := httptest.NewRecorder()
	h.HandleGet(rec, httptest.NewRequest(http.MethodGet, "/profile-credentials/owned", nil))
	if rec.Code != 403 {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestBloemProfileCredentialsRequiresExpectedRevision(t *testing.T) {
	store := newEmptyProfileTestStore(t)
	if err := store.CreateProfile(context.Background(), userstore.Profile{ID: "parent", Name: "Parent", IsPrimary: true}); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		for _, revision := range []string{"", `,"expected_revision":null`, `,"expected_revision":0`, `,"expected_revision":-1`, `,"expected_revision":2.5`, `,"expected_revision":"2"`} {
			t.Run(method+revision, func(t *testing.T) {
				manager := &credentialManagerStub{}
				h := &BloemProfileCredentialsHandler{profiles: NewProfileHandler(testUserStoreProvider{store: store}), credentials: manager, reauth: &adminReauthVerifierStub{allowed: true}}
				req := newAuthorizedProfileRequestWithSession(method, "/profile-credentials/owned", `{"current_password":"current","login_email":"reader@example.test","password":"profile-password"`+revision+`}`, "user", "parent", "session")
				req = withProfileRouteParam(req, "id", "owned")
				rec := httptest.NewRecorder()
				h.HandleChange(rec, req)
				want := http.StatusUnprocessableEntity
				if strings.Contains(revision, "2.5") || strings.Contains(revision, `"2"`) {
					want = http.StatusBadRequest
				}
				if rec.Code != want || manager.writes != 0 {
					t.Fatalf("missing revision: status=%d writes=%d", rec.Code, manager.writes)
				}
			})
		}
	}
}

func TestBloemProfileCredentialsWriteErrors(t *testing.T) {
	store := newEmptyProfileTestStore(t)
	if err := store.CreateProfile(context.Background(), userstore.Profile{ID: "parent", Name: "Parent", IsPrimary: true}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"stale revision", auth.ErrProfileCredentialRevisionConflict, 409, "credential_revision_conflict"},
		{"email in use", auth.ErrCredentialEmailInUse, 409, "credential_email_in_use"},
		{"ownership changed", auth.ErrProfileCredentialNotFound, 404, "not_found"},
		{"internal failure", errors.New("private backend detail"), 503, "unavailable"},
	} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			t.Run(test.name+method, func(t *testing.T) {
				manager := &credentialManagerStub{err: test.err}
				reauth := &adminReauthVerifierStub{allowed: true}
				h := &BloemProfileCredentialsHandler{profiles: NewProfileHandler(testUserStoreProvider{store: store}), credentials: manager, reauth: reauth}
				// Status returns revision 2; the service can see a newer revision
				// after reauthentication, so its transactional conflict must win.
				req := newAuthorizedProfileRequestWithSession(method, "/profile-credentials/owned", `{"current_password":"account-secret","login_email":"reader@example.test","password":"profile-secret","expected_revision":2}`, "user", "parent", "session")
				req = withProfileRouteParam(req, "id", "owned")
				rec := httptest.NewRecorder()
				h.HandleChange(rec, req)
				if rec.Code != test.status || manager.writes != 1 || manager.revision != 2 || manager.method != method || reauth.calls != 1 {
					t.Fatalf("status=%d manager=%+v reauth=%d", rec.Code, manager, reauth.calls)
				}
				var body errorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Error != test.code {
					t.Fatalf("missing error code %s: %s", test.code, rec.Body.String())
				}
				for _, secret := range []string{"account-secret", "profile-secret", "reader@example.test", "private backend detail"} {
					if strings.Contains(rec.Body.String(), secret) {
						t.Fatalf("response disclosed %q", secret)
					}
				}
				if test.code == "credential_revision_conflict" && !strings.Contains(rec.Body.String(), "Reload and review") {
					t.Fatal("conflict did not require a new review")
				}
			})
		}
	}
}
