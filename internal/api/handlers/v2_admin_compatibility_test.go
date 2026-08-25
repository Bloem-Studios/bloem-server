package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
)

// compatServiceStub is the test double behind the narrow application
// lifecycle interface. The handler must call the service; it never touches
// application tables itself.
type compatServiceStub struct {
	applications []CompatibilityApplication
	enrollment   CompatibilityEnrollment
	credential   CompatibilityCredential
	err          error

	listCalls    int
	enrollKind   string
	enrollCaps   []string
	enabledCalls []compatEnableCall
	rotateCalls  []compatRevisionCall
	revokeCalls  []compatRevisionCall
	actor        tenancy.AdminMutationActor
	actorSeen    bool
}

type compatEnableCall struct {
	instanceID string
	enabled    bool
	revision   int64
}

type compatRevisionCall struct {
	instanceID string
	revision   int64
}

func (s *compatServiceStub) captureActor(ctx context.Context) {
	s.actor, s.actorSeen = tenancy.AdminMutationActorFromContext(ctx)
}

func (s *compatServiceStub) ListApplications(context.Context) ([]CompatibilityApplication, error) {
	s.listCalls++
	return s.applications, s.err
}

func (s *compatServiceStub) CreateEnrollment(ctx context.Context, kind string, capabilities []string) (CompatibilityEnrollment, error) {
	s.captureActor(ctx)
	s.enrollKind = kind
	s.enrollCaps = capabilities
	return s.enrollment, s.err
}

func (s *compatServiceStub) SetApplicationEnabled(ctx context.Context, instanceID string, enabled bool, expectedRevision int64) (CompatibilityApplication, error) {
	s.captureActor(ctx)
	s.enabledCalls = append(s.enabledCalls, compatEnableCall{instanceID, enabled, expectedRevision})
	if len(s.applications) > 0 {
		return s.applications[0], s.err
	}
	return CompatibilityApplication{}, s.err
}

func (s *compatServiceStub) RotateApplicationCredential(ctx context.Context, instanceID string, expectedRevision int64) (CompatibilityCredential, CompatibilityApplication, error) {
	s.captureActor(ctx)
	s.rotateCalls = append(s.rotateCalls, compatRevisionCall{instanceID, expectedRevision})
	if len(s.applications) > 0 {
		return s.credential, s.applications[0], s.err
	}
	return s.credential, CompatibilityApplication{}, s.err
}

func (s *compatServiceStub) RevokeApplication(ctx context.Context, instanceID string, expectedRevision int64) (CompatibilityApplication, error) {
	s.captureActor(ctx)
	s.revokeCalls = append(s.revokeCalls, compatRevisionCall{instanceID, expectedRevision})
	if len(s.applications) > 0 {
		return s.applications[0], s.err
	}
	return CompatibilityApplication{}, s.err
}

func compatTestApplication() CompatibilityApplication {
	contact := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	return CompatibilityApplication{
		InstanceID:     "inst-jelly-1",
		Kind:           "jellyfin",
		State:          "enabled",
		Enabled:        true,
		Healthy:        true,
		Version:        "1.4.2",
		ImageDigest:    "sha256:abcdef0123",
		APIRange:       CompatibilityAPIRange{Min: "1.0", Max: "1.3"},
		LastContactAt:  &contact,
		ActiveSessions: 3,
		Capabilities:   []string{"identity.exchange", "catalog.read"},
		Revision:       7,
	}
}

func newCompatRouter(service CompatibilityApplicationService) chi.Router {
	handler := NewV2AdminCompatibilityHandler(service, "https://bloem.example")
	r := chi.NewRouter()
	r.Get("/applications", handler.HandleListApplications)
	r.Post("/enrollments", handler.HandleCreateEnrollment)
	r.Post("/applications/{instance_id}/enable", handler.HandleEnableApplication)
	r.Post("/applications/{instance_id}/disable", handler.HandleDisableApplication)
	r.Post("/applications/{instance_id}/rotate-credential", handler.HandleRotateCredential)
	r.Post("/applications/{instance_id}/revoke", handler.HandleRevokeApplication)
	return r
}

func compatRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	return req.WithContext(apimw.SetAdminContextClaims(req.Context(), auth.AdminContextClaims{
		AccountID: 7,
		Scope:     auth.AdminScopePlatform,
	}))
}

func TestCompatibilityAdminRequiresPlatformAuthority(t *testing.T) {
	service := &compatServiceStub{applications: []CompatibilityApplication{compatTestApplication()}}
	router := newCompatRouter(service)

	targets := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/applications", ""},
		{http.MethodPost, "/enrollments", `{"kind":"jellyfin"}`},
		{http.MethodPost, "/applications/inst-jelly-1/enable", `{"expected_revision":7}`},
		{http.MethodPost, "/applications/inst-jelly-1/disable", `{"expected_revision":7}`},
		{http.MethodPost, "/applications/inst-jelly-1/rotate-credential", `{"expected_revision":7}`},
		{http.MethodPost, "/applications/inst-jelly-1/revoke", `{"expected_revision":7,"confirmed":true}`},
	}
	for _, target := range targets {
		var req *http.Request
		if target.body == "" {
			req = httptest.NewRequest(target.method, target.path, nil)
		} else {
			req = httptest.NewRequest(target.method, target.path, strings.NewReader(target.body))
		}
		// Organization-scoped claims are not enough: applications are
		// deployment-level state.
		req = req.WithContext(apimw.SetAdminContextClaims(req.Context(), auth.AdminContextClaims{
			AccountID: 7,
			Scope:     auth.AdminScopeOrganization,
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s: got %d, want 403", target.method, target.path, rec.Code)
		}
	}
	if service.listCalls != 0 || service.actorSeen {
		t.Fatal("an unauthorized request must never reach the service")
	}
}

func TestCompatibilityAdminListsReadOnlyState(t *testing.T) {
	service := &compatServiceStub{applications: []CompatibilityApplication{compatTestApplication()}}
	router := newCompatRouter(service)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodGet, "/applications", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Applications []struct {
			CompatibilityApplication
			CanonicalURL string `json:"canonical_url"`
			Commands     struct {
				Install  string `json:"install"`
				Update   string `json:"update"`
				Rollback string `json:"rollback"`
				Remove   string `json:"remove"`
			} `json:"commands"`
		} `json:"applications"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Applications) != 1 {
		t.Fatalf("expected one application, got %d", len(response.Applications))
	}
	row := response.Applications[0]
	if row.InstanceID != "inst-jelly-1" || row.Version != "1.4.2" || row.ImageDigest != "sha256:abcdef0123" {
		t.Fatalf("identity/version/digest missing: %+v", row)
	}
	if row.APIRange.Min != "1.0" || row.APIRange.Max != "1.3" {
		t.Fatalf("API range missing: %+v", row.APIRange)
	}
	if row.ActiveSessions != 3 || row.LastContactAt == nil || row.Revision != 7 {
		t.Fatalf("sessions/contact/revision missing: %+v", row)
	}
	// The Jellyfin canonical client URL is the same-origin public address.
	if row.CanonicalURL != "https://bloem.example/" {
		t.Fatalf("canonical url %q", row.CanonicalURL)
	}
	// Administration shows exact operator commands; Bloem never runs them.
	for name, command := range map[string]string{
		"install": row.Commands.Install, "update": row.Commands.Update,
		"rollback": row.Commands.Rollback, "remove": row.Commands.Remove,
	} {
		if !strings.Contains(command, "docker compose") || !strings.Contains(command, "docker-compose.jellyfin.yml") {
			t.Fatalf("%s command %q must be an exact operator compose command", name, command)
		}
	}
	// The state volume is Compose-project-prefixed, so a literal
	// "docker volume rm bloem-jellyfin-state" fails as typed. The command
	// must resolve the real name through `docker volume ls` instead.
	if strings.Contains(row.Commands.Remove, "docker volume rm bloem-jellyfin-state") {
		t.Fatalf("remove command %q names an unprefixed volume that does not exist", row.Commands.Remove)
	}
	if !strings.Contains(row.Commands.Remove, "docker volume ls") ||
		!strings.Contains(row.Commands.Remove, "bloem-jellyfin-state") {
		t.Fatalf("remove command %q must resolve the project-prefixed state volume via docker volume ls", row.Commands.Remove)
	}
	// --filter name= is an unanchored substring match: unanchored it selects
	// every Compose project's copy, and it yields an empty argument when
	// nothing matches. The filter must be anchored and the result piped
	// through xargs -r so no match removes nothing.
	if !strings.Contains(row.Commands.Remove, "_bloem-jellyfin-state$") {
		t.Fatalf("remove command %q must anchor the volume filter", row.Commands.Remove)
	}
	if !strings.Contains(row.Commands.Remove, "xargs -r docker volume rm") {
		t.Fatalf("remove command %q must pipe through xargs -r so an empty match is a no-op", row.Commands.Remove)
	}
	if service.actorSeen {
		t.Fatal("a read must not record a mutation actor")
	}
}

func TestCompatibilityAdminAudiobookshelfCanonicalURL(t *testing.T) {
	app := compatTestApplication()
	app.Kind = "audiobookshelf"
	app.InstanceID = "inst-abs-1"
	service := &compatServiceStub{applications: []CompatibilityApplication{app}}
	router := newCompatRouter(service)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodGet, "/applications", ""))
	var response struct {
		Applications []struct {
			CanonicalURL string `json:"canonical_url"`
		} `json:"applications"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Applications[0].CanonicalURL != "https://bloem.example/audiobookshelf" {
		t.Fatalf("canonical url %q", response.Applications[0].CanonicalURL)
	}
}

func TestCompatibilityAdminEnrollmentReturnsOneTimeSecret(t *testing.T) {
	service := &compatServiceStub{enrollment: CompatibilityEnrollment{
		Kind:      "audiobookshelf",
		Secret:    "vce_one_time_secret",
		ExpiresAt: time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC),
	}}
	router := newCompatRouter(service)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/enrollments",
		`{"kind":"audiobookshelf","capabilities":["identity.exchange"]}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201: %s", rec.Code, rec.Body.String())
	}
	// One-time secrets must never be cached anywhere along the way.
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("enrollment response must be no-store, got %q", rec.Header().Get("Cache-Control"))
	}
	var response struct {
		Enrollment CompatibilityEnrollment `json:"enrollment"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Enrollment.Secret != "vce_one_time_secret" {
		t.Fatal("the one-time enrollment secret must be returned exactly once")
	}
	if service.enrollKind != "audiobookshelf" || len(service.enrollCaps) != 1 {
		t.Fatalf("service saw kind=%q caps=%v", service.enrollKind, service.enrollCaps)
	}
	if !service.actorSeen || service.actor.AccountID != 7 {
		t.Fatalf("enrollment must be audited with the acting administrator, got %+v", service.actor)
	}
}

func TestCompatibilityAdminEnrollmentRejectsUnknownKind(t *testing.T) {
	service := &compatServiceStub{}
	router := newCompatRouter(service)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/enrollments", `{"kind":"plex"}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422", rec.Code)
	}
	if service.enrollKind != "" {
		t.Fatal("an unknown kind must not reach the service")
	}
}

func TestCompatibilityAdminMutationsAreRevisionGuarded(t *testing.T) {
	service := &compatServiceStub{applications: []CompatibilityApplication{compatTestApplication()}}
	router := newCompatRouter(service)

	// A mutation without a positive expected revision is refused before the
	// service is consulted.
	for _, path := range []string{
		"/applications/inst-jelly-1/enable",
		"/applications/inst-jelly-1/disable",
		"/applications/inst-jelly-1/rotate-credential",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, compatRequest(t, http.MethodPost, path, `{}`))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s without a revision: got %d, want 422", path, rec.Code)
		}
	}
	if len(service.enabledCalls)+len(service.rotateCalls) != 0 {
		t.Fatal("revision-less mutations must not reach the service")
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-jelly-1/enable", `{"expected_revision":7}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: got %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-jelly-1/disable", `{"expected_revision":7}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: got %d: %s", rec.Code, rec.Body.String())
	}
	if len(service.enabledCalls) != 2 ||
		service.enabledCalls[0] != (compatEnableCall{"inst-jelly-1", true, 7}) ||
		service.enabledCalls[1] != (compatEnableCall{"inst-jelly-1", false, 7}) {
		t.Fatalf("service calls %+v", service.enabledCalls)
	}
	if !service.actorSeen || service.actor.AccountID != 7 {
		t.Fatalf("mutations must be audited with the acting administrator, got %+v", service.actor)
	}
}

func TestCompatibilityAdminRevisionConflict(t *testing.T) {
	service := &compatServiceStub{
		applications: []CompatibilityApplication{compatTestApplication()},
		err:          &CompatibilityRevisionError{CurrentRevision: 9},
	}
	router := newCompatRouter(service)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-jelly-1/enable", `{"expected_revision":7}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Error           string `json:"error"`
		CurrentRevision int64  `json:"current_revision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Error != "authorization_state_changed" || response.CurrentRevision != 9 {
		t.Fatalf("conflict body %+v", response)
	}
}

func TestCompatibilityAdminRevokeRequiresConfirmation(t *testing.T) {
	service := &compatServiceStub{applications: []CompatibilityApplication{compatTestApplication()}}
	router := newCompatRouter(service)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-jelly-1/revoke", `{"expected_revision":7}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unconfirmed revoke: got %d, want 422", rec.Code)
	}
	if len(service.revokeCalls) != 0 {
		t.Fatal("an unconfirmed revocation must not reach the service")
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-jelly-1/revoke", `{"expected_revision":7,"confirmed":true}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("confirmed revoke: got %d: %s", rec.Code, rec.Body.String())
	}
	if len(service.revokeCalls) != 1 || service.revokeCalls[0] != (compatRevisionCall{"inst-jelly-1", 7}) {
		t.Fatalf("revoke calls %+v", service.revokeCalls)
	}
}

func TestCompatibilityAdminRotateReturnsOneTimeCredential(t *testing.T) {
	service := &compatServiceStub{
		applications: []CompatibilityApplication{compatTestApplication()},
		credential: CompatibilityCredential{
			Secret:    "vcc_rotated_secret",
			ExpiresAt: time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC),
		},
	}
	router := newCompatRouter(service)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-jelly-1/rotate-credential", `{"expected_revision":7}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential responses must be no-store")
	}
	var response struct {
		Credential CompatibilityCredential `json:"credential"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Credential.Secret != "vcc_rotated_secret" {
		t.Fatal("the rotated credential must be returned exactly once")
	}
	if len(service.rotateCalls) != 1 || service.rotateCalls[0] != (compatRevisionCall{"inst-jelly-1", 7}) {
		t.Fatalf("rotate calls %+v", service.rotateCalls)
	}
}

func TestCompatibilityAdminUnknownApplication(t *testing.T) {
	service := &compatServiceStub{err: ErrCompatibilityApplicationNotFound}
	router := newCompatRouter(service)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-missing/enable", `{"expected_revision":1}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}

func TestCompatibilityAdminUnavailableService(t *testing.T) {
	router := newCompatRouter(nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodGet, "/applications", ""))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
}
