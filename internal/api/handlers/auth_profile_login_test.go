package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/auth/passwordhash"
)

func TestDirectProfileLoginReturnsOnlyProfileBoundSession(t *testing.T) {
	t.Parallel()
	login := &profileLoginStub{subject: auth.SessionSubject{
		AccountID:          17,
		ProfileID:          "profile-reader",
		OrganizationID:     "organization-reader",
		MembershipID:       "membership-reader",
		PolicyRevision:     4,
		SecurityRevision:   9,
		CredentialRevision: 3,
		AuthMethod:         auth.AuthMethodDirectProfile,
	}}
	handler := &AuthHandler{profileLogin: login}
	req := httptest.NewRequest(http.MethodPost, "/auth/profile-login", strings.NewReader(`{"email":"reader@example.test","password":"profile-password","device_id":"tablet-17"}`))
	req.Header.Set("User-Agent", "Reader Tablet")
	rec := httptest.NewRecorder()

	handler.HandleProfileLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if login.email != "reader@example.test" || login.password != "profile-password" || login.device.ID != "tablet-17" || login.device.Name != "Reader Tablet" {
		t.Fatalf("login input = %#v, want request credentials and device", login)
	}
	// The response is a frozen contract: decode raw and assert the exact key
	// set with every value. Decoding into the production struct would keep
	// passing if a field stopped being populated — and could never notice an
	// account or sibling-profile field being newly exposed.
	var response map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := map[string]string{
		"access_token":        `"access"`,
		"refresh_token":       `"refresh"`,
		"expires_in":          `300`,
		"profile_id":          `"profile-reader"`,
		"organization_id":     `"organization-reader"`,
		"membership_id":       `"membership-reader"`,
		"policy_revision":     `4`,
		"security_revision":   `9`,
		"credential_revision": `3`,
	}
	for key, value := range want {
		got, ok := response[key]
		if !ok {
			t.Fatalf("response is missing %q: %v", key, rec.Body.String())
		}
		if string(got) != value {
			t.Fatalf("response[%q] = %s, want %s", key, got, value)
		}
	}
	for key := range response {
		if _, ok := want[key]; !ok {
			t.Fatalf("response exposes unexpected field %q — a direct profile login returns the profile-bound session and nothing else", key)
		}
	}
}

// A server too busy to check the password says so: the password is neither
// right nor wrong, so the client retries instead of reporting bad credentials.
func TestDirectProfileLoginAnswersBusyHashingWithRetry(t *testing.T) {
	t.Parallel()
	handler := &AuthHandler{profileLogin: &profileLoginStub{err: fmt.Errorf("compare: %w", passwordhash.ErrBusy)}}
	req := httptest.NewRequest(http.MethodPost, "/auth/profile-login", strings.NewReader(`{"email":"reader@example.test","password":"profile-password","device_id":"tablet-17"}`))
	rec := httptest.NewRecorder()

	handler.HandleProfileLogin(rec, req)

	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("status = %d, Retry-After = %q, want 503 and 1: %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || body.Error != "service_unavailable" {
		t.Fatalf("body = %+v, %v; want service_unavailable", body, err)
	}
}

type profileLoginStub struct {
	email, password string
	device          auth.DeviceClaim
	subject         auth.SessionSubject
	err             error
}

func (s *profileLoginStub) LoginProfile(_ context.Context, email, password string, device auth.DeviceClaim) (*auth.TokenPair, auth.SessionSubject, error) {
	s.email, s.password, s.device = email, password, device
	if s.err != nil {
		return nil, auth.SessionSubject{}, s.err
	}
	return &auth.TokenPair{AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 300}, s.subject, nil
}
