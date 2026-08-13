package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
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
	var response profileLoginResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.AccessToken != "access" || response.RefreshToken != "refresh" || response.ProfileID != "profile-reader" || response.OrganizationID != "organization-reader" || response.MembershipID != "membership-reader" {
		t.Fatalf("response = %#v, want profile-bound session only", response)
	}
}

type profileLoginStub struct {
	email, password string
	device          auth.DeviceClaim
	subject         auth.SessionSubject
}

func (s *profileLoginStub) LoginProfile(_ context.Context, email, password string, device auth.DeviceClaim) (*auth.TokenPair, auth.SessionSubject, error) {
	s.email, s.password, s.device = email, password, device
	return &auth.TokenPair{AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 300}, s.subject, nil
}
