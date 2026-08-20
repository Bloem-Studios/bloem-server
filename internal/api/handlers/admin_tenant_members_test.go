package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type tenantMemberServiceStub struct {
	createUser   models.User
	createReplay bool
	createErr    error
	members      []models.User
	member       models.User
	err          error
	requireErr   error
	resetSecret  string
}

func (s *tenantMemberServiceStub) Create(context.Context, uuid.UUID, string, tenancy.CreateMemberInput) (models.User, bool, error) {
	return s.createUser, s.createReplay, s.createErr
}
func (s *tenantMemberServiceStub) List(context.Context, uuid.UUID) ([]models.User, error) {
	return s.members, s.err
}
func (s *tenantMemberServiceStub) Get(context.Context, uuid.UUID, int) (models.User, error) {
	return s.member, s.err
}
func (s *tenantMemberServiceStub) Update(context.Context, uuid.UUID, int, tenancy.UpdateMemberInput) (models.User, error) {
	return s.member, s.err
}
func (s *tenantMemberServiceStub) Suspend(context.Context, uuid.UUID, int) (models.User, error) {
	return s.member, s.err
}
func (s *tenantMemberServiceStub) Resume(context.Context, uuid.UUID, int) (models.User, error) {
	return s.member, s.err
}
func (s *tenantMemberServiceStub) ResetPassword(_ context.Context, _ uuid.UUID, _ int, password string) (models.User, error) {
	s.resetSecret = password
	return s.member, s.err
}
func (s *tenantMemberServiceStub) Delete(context.Context, uuid.UUID, int) error {
	return s.err
}
func (s *tenantMemberServiceStub) RequireMembership(context.Context, uuid.UUID, int) error {
	return s.requireErr
}

func tenantMemberTestRouter(handler *AdminTenantMembersHandler) http.Handler {
	r := chi.NewRouter()
	r.Route("/tenants/{tenant_id}/members", func(r chi.Router) {
		r.Get("/", handler.HandleList)
		r.Post("/", handler.HandleCreate)
		r.Get("/{user_id}", handler.HandleGet)
		r.Put("/{user_id}", handler.HandleUpdate)
		r.Delete("/{user_id}", handler.HandleDelete)
		r.Post("/{user_id}/suspend", handler.HandleSuspend)
		r.Post("/{user_id}/resume", handler.HandleResume)
		r.Post("/{user_id}/reset-password", handler.HandleResetPassword)
		r.Get("/{user_id}/profiles", handler.HandleListProfiles)
		r.Post("/{user_id}/profiles", handler.HandleCreateProfile)
		r.Put("/{user_id}/profiles/{profile_id}", handler.HandleUpdateProfile)
		r.Delete("/{user_id}/profiles/{profile_id}", handler.HandleDeleteProfile)
		r.Get("/{user_id}/devices", handler.HandleListDevices)
		r.Delete("/{user_id}/devices/{device_id}", handler.HandleDeleteDevice)
		r.Get("/{user_id}/auth-sessions", handler.HandleListAuthSessions)
		r.Delete("/{user_id}/auth-sessions/{session_id}", handler.HandleRevokeAuthSession)
		r.Delete("/{user_id}/auth-sessions", handler.HandleRevokeAllAuthSessions)
	})
	return r
}

func tenantMemberRequest(t *testing.T, router http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestAdminTenantMemberRoutesHideCrossTenantMembership(t *testing.T) {
	tenantID := uuid.New()
	stub := &tenantMemberServiceStub{err: tenancy.ErrMemberNotFound, requireErr: tenancy.ErrMemberNotFound}
	router := tenantMemberTestRouter(NewAdminTenantMembersHandler(stub, nil))
	base := "/tenants/" + tenantID.String() + "/members/27"
	cases := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, base, nil},
		{http.MethodPut, base, map[string]any{"username": "changed"}},
		{http.MethodDelete, base, nil},
		{http.MethodPost, base + "/suspend", nil},
		{http.MethodPost, base + "/resume", nil},
		{http.MethodPost, base + "/reset-password", map[string]any{"password": "never-log-me"}},
		{http.MethodGet, base + "/profiles", nil},
		{http.MethodPost, base + "/profiles", map[string]any{"name": "Profile"}},
		{http.MethodPut, base + "/profiles/profile-b", map[string]any{"name": "Changed"}},
		{http.MethodDelete, base + "/profiles/profile-b", nil},
		{http.MethodGet, base + "/devices", nil},
		{http.MethodDelete, base + "/devices/device-b", nil},
		{http.MethodGet, base + "/auth-sessions", nil},
		{http.MethodDelete, base + "/auth-sessions/session-b", nil},
		{http.MethodDelete, base + "/auth-sessions", nil},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := tenantMemberRequest(t, router, tc.method, tc.path, tc.body, nil)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "never-log-me") {
				t.Fatal("password appeared in cross-tenant error")
			}
		})
	}
}

func TestAdminTenantMemberCreateMapsQuotaAndCommandConflicts(t *testing.T) {
	tenantID := uuid.New()
	path := "/tenants/" + tenantID.String() + "/members/"
	body := map[string]any{"username": "member", "email": "member@test", "password": "create-secret"}
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"quota", tenancy.ErrSlotQuotaExceeded, http.StatusUnprocessableEntity},
		{"username", tenancy.ErrUsernameConflict, http.StatusConflict},
		{"idempotency", tenancy.ErrIdempotencyConflict, http.StatusConflict},
		{"tenant", tenancy.ErrMemberNotFound, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &tenantMemberServiceStub{createErr: tc.err}
			router := tenantMemberTestRouter(NewAdminTenantMembersHandler(stub, nil))
			rec := tenantMemberRequest(t, router, http.MethodPost, path, body, map[string]string{"Idempotency-Key": "command-1"})
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "create-secret") {
				t.Fatal("password appeared in create error")
			}
		})
	}
}

func TestAdminTenantMemberResetNeverEchoesPassword(t *testing.T) {
	tenantID := uuid.New()
	stub := &tenantMemberServiceStub{member: models.User{ID: 42, Username: "member", Email: "member@test", Enabled: true}}
	router := tenantMemberTestRouter(NewAdminTenantMembersHandler(stub, nil))
	secret := "correct-horse-private"
	rec := tenantMemberRequest(t, router, http.MethodPost,
		"/tenants/"+tenantID.String()+"/members/42/reset-password",
		map[string]string{"password": secret}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if stub.resetSecret != secret {
		t.Fatalf("service received password = %q", stub.resetSecret)
	}
	if strings.Contains(rec.Body.String(), secret) || strings.Contains(rec.Body.String(), "password_hash") {
		t.Fatalf("secret-bearing response = %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), strconv.Itoa(stub.member.ID)) {
		t.Fatalf("response omitted member id: %s", rec.Body.String())
	}
}

func TestAdminTenantMemberCreateRejectsMissingIdempotencyKey(t *testing.T) {
	stub := &tenantMemberServiceStub{}
	router := tenantMemberTestRouter(NewAdminTenantMembersHandler(stub, nil))
	rec := tenantMemberRequest(t, router, http.MethodPost,
		"/tenants/"+uuid.NewString()+"/members/",
		map[string]string{"username": "member", "email": "member@test", "password": "private"}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !errors.Is(stub.createErr, nil) {
		t.Fatal("unexpected stub state")
	}
}
