package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/compatapi"
)

type recorded struct {
	method         string
	path           string
	query          string
	authorization  string
	subjectToken   string
	idempotencyKey string
	body           map[string]any
}

func newServer(t *testing.T, status int, response any) (*httptest.Server, *[]recorded) {
	t.Helper()
	var calls []recorded
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entry := recorded{
			method:         r.Method,
			path:           r.URL.Path,
			query:          r.URL.RawQuery,
			authorization:  r.Header.Get("Authorization"),
			subjectToken:   r.Header.Get("X-Vondel-Subject-Token"),
			idempotencyKey: r.Header.Get("Idempotency-Key"),
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&entry.body)
		}
		calls = append(calls, entry)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func TestEnrollPostsTheSecretWithAnIdempotencyKey(t *testing.T) {
	server, calls := newServer(t, http.StatusCreated, map[string]any{
		"application_id":       "app-1",
		"token":                "svc-token",
		"granted_capabilities": []string{"service", "catalog"},
	})
	c := New(server.URL)
	cred, err := c.Enroll(context.Background(), EnrollRequest{
		Secret:                "enroll-secret",
		Kind:                  "audiobookshelf",
		InstanceID:            "inst-1",
		Version:               "0.1.0",
		API:                   compatapi.APIRange{Min: 1, Max: 1},
		RequestedCapabilities: []string{"catalog"},
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if cred.Token != "svc-token" || cred.ApplicationID != "app-1" {
		t.Fatalf("credential = %+v", cred)
	}
	call := (*calls)[0]
	if call.method != http.MethodPost || call.path != "/enroll" {
		t.Fatalf("call = %+v", call)
	}
	if call.idempotencyKey == "" {
		t.Fatal("enroll must send an Idempotency-Key")
	}
	if call.body["secret"] != "enroll-secret" || call.body["kind"] != "audiobookshelf" {
		t.Fatalf("body = %v", call.body)
	}
	if call.authorization != "" {
		t.Fatalf("enroll must not send a bearer, got %q", call.authorization)
	}
}

func TestAuthenticatedCallsCarryServiceAndSubjectTokens(t *testing.T) {
	server, calls := newServer(t, http.StatusOK, map[string]any{
		"items":       []any{map[string]any{"id": "item-1", "kind": "movie", "title": "Tester"}},
		"next_cursor": "cur-2",
	})
	c := New(server.URL, WithServiceToken("svc-token"))
	page, err := c.Items(context.Background(), "subject-token", ItemsQuery{LibraryID: "lib-1", Cursor: "cur-1", Limit: 5})
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "item-1" || page.NextCursor != "cur-2" {
		t.Fatalf("page = %+v", page)
	}
	call := (*calls)[0]
	if call.authorization != "Bearer svc-token" {
		t.Fatalf("authorization = %q", call.authorization)
	}
	if call.subjectToken != "subject-token" {
		t.Fatalf("subject token = %q", call.subjectToken)
	}
	if call.query == "" {
		t.Fatal("items query must carry parameters")
	}
}

func TestMutationsSendIdempotencyKeys(t *testing.T) {
	server, calls := newServer(t, http.StatusOK, map[string]any{"item_id": "item-1", "position_seconds": 42})
	c := New(server.URL, WithServiceToken("svc-token"))
	c.IdempotencyKeys = func() string { return "fixed-key" }
	if _, err := c.SetProgress(context.Background(), "subject-token", "item-1", compatapi.ProgressUpdate{PositionSeconds: 42}); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}
	call := (*calls)[0]
	if call.method != http.MethodPut || call.path != "/state/progress/item-1" {
		t.Fatalf("call = %+v", call)
	}
	if call.idempotencyKey != "fixed-key" {
		t.Fatalf("idempotency key = %q", call.idempotencyKey)
	}
}

func TestErrorEnvelopesDecodeIntoAPIErrors(t *testing.T) {
	server, _ := newServer(t, http.StatusForbidden, map[string]any{
		"error": map[string]any{"code": "capability_missing", "message": "capability not granted", "trace_id": "tr-1"},
	})
	c := New(server.URL, WithServiceToken("svc-token"))
	_, err := c.Libraries(context.Background(), "subject-token")
	if err == nil {
		t.Fatal("expected an error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %T is not an *APIError: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusForbidden || apiErr.Code != "capability_missing" || apiErr.TraceID != "tr-1" {
		t.Fatalf("apiErr = %+v", apiErr)
	}
}

func TestLoginParsesSubjectFacts(t *testing.T) {
	server, calls := newServer(t, http.StatusOK, map[string]any{
		"subject_token": "sub-token",
		"subject": map[string]any{
			"account_id":          7,
			"profile_id":          "prof-1",
			"organization_id":     "org-1",
			"membership_id":       "mem-1",
			"policy_revision":     3,
			"security_revision":   4,
			"credential_revision": 5,
			"auth_method":         "direct_profile",
			"device":              map[string]any{"id": "dev-1"},
		},
	})
	c := New(server.URL, WithServiceToken("svc-token"))
	result, err := c.Login(context.Background(), LoginRequest{
		GrantType: "direct_profile",
		Email:     "reader@example.test",
		Password:  "correct horse",
		Device:    compatapi.DeviceClaim{ID: "dev-1"},
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if result.SubjectToken != "sub-token" {
		t.Fatalf("result = %+v", result)
	}
	s := result.Subject
	if s.AccountID != 7 || s.ProfileID != "prof-1" || s.OrganizationID != "org-1" ||
		s.MembershipID != "mem-1" || s.PolicyRevision != 3 || s.SecurityRevision != 4 ||
		s.CredentialRevision != 5 || s.AuthMethod != "direct_profile" || s.Device.ID != "dev-1" {
		t.Fatalf("subject facts lost: %+v", s)
	}
	call := (*calls)[0]
	if call.idempotencyKey == "" {
		t.Fatal("login is a mutation and must carry an idempotency key")
	}
	if call.body["password"] != "correct horse" {
		t.Fatalf("body = %v", call.body)
	}
}
