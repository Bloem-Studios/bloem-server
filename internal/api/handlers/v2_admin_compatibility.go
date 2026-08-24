package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/go-chi/chi/v5"
)

// The Compatibility Applications admin surface. It is read-and-decide only:
// it lists lifecycle state, creates enrollments, and enables/disables/
// rotates/revokes through the application lifecycle service. Bloem never
// controls Docker — the surface displays exact operator commands and performs
// no container mutation.

// CompatibilityAPIRange is the private-API version range an application
// negotiated.
type CompatibilityAPIRange struct {
	Min string `json:"min"`
	Max string `json:"max"`
}

// CompatibilityApplication is the read-only administrative view of one
// enrolled application instance.
type CompatibilityApplication struct {
	InstanceID     string                `json:"instance_id"`
	Kind           string                `json:"kind"`
	State          string                `json:"state"`
	Enabled        bool                  `json:"enabled"`
	Revoked        bool                  `json:"revoked"`
	Healthy        bool                  `json:"healthy"`
	Version        string                `json:"version"`
	ImageDigest    string                `json:"image_digest"`
	APIRange       CompatibilityAPIRange `json:"api_range"`
	LastContactAt  *time.Time            `json:"last_contact_at"`
	ActiveSessions int                   `json:"active_sessions"`
	Capabilities   []string              `json:"capabilities"`
	Revision       int64                 `json:"revision"`
}

// CompatibilityEnrollment is a one-use enrollment secret. The raw secret
// appears exactly once, in the creating response; only digests are stored.
type CompatibilityEnrollment struct {
	Kind      string    `json:"kind"`
	Secret    string    `json:"secret"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CompatibilityCredential is a freshly rotated service credential, returned
// exactly once.
type CompatibilityCredential struct {
	Secret    string    `json:"secret"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ErrCompatibilityApplicationNotFound reports an unknown application
// instance.
var ErrCompatibilityApplicationNotFound = errors.New("compatibility application not found")

// ErrCompatibilityKindUnknown reports an application kind outside the
// reviewed set.
var ErrCompatibilityKindUnknown = errors.New("compatibility application kind unknown")

// ErrCompatibilityCapabilityUnknown reports a requested capability outside
// the reviewed set.
var ErrCompatibilityCapabilityUnknown = errors.New("compatibility capability unknown")

// CompatibilityRevisionError reports an optimistic-concurrency conflict on an
// application mutation and carries the revision to retry against.
type CompatibilityRevisionError struct {
	CurrentRevision int64
}

func (e *CompatibilityRevisionError) Error() string {
	return fmt.Sprintf("compatibility application revision changed (current %d)", e.CurrentRevision)
}

// CompatibilityApplicationService is the narrow local view of the application
// lifecycle service. Handlers call the service instead of writing application
// tables; the enrollment/trust package satisfies this interface at wiring
// time. Implementations should read the auditing actor installed by
// tenancy.AdminMutationActorFromContext.
type CompatibilityApplicationService interface {
	ListApplications(ctx context.Context) ([]CompatibilityApplication, error)
	CreateEnrollment(ctx context.Context, kind string, capabilities []string) (CompatibilityEnrollment, error)
	SetApplicationEnabled(ctx context.Context, instanceID string, enabled bool, expectedRevision int64) (CompatibilityApplication, error)
	RotateApplicationCredential(ctx context.Context, instanceID string, expectedRevision int64) (CompatibilityCredential, CompatibilityApplication, error)
	RevokeApplication(ctx context.Context, instanceID string, expectedRevision int64) (CompatibilityApplication, error)
}

const (
	compatKindJellyfin       = "jellyfin"
	compatKindAudiobookshelf = "audiobookshelf"

	compatFieldKind             = "kind"
	compatFieldExpectedRevision = "expected_revision"

	compatKindValidationMessage     = "must be jellyfin or audiobookshelf"
	compatRevisionValidationMessage = "must be a positive revision"

	// compatCodeStateChanged mirrors the platform admin surface's
	// optimistic-concurrency conflict code.
	compatCodeStateChanged = "authorization_state_changed"
)

// compatibilityKinds is the reviewed application set; anything else fails
// closed before the service is consulted.
var compatibilityKinds = map[string]bool{
	compatKindJellyfin:       true,
	compatKindAudiobookshelf: true,
}

// V2AdminCompatibilityHandler serves the platform-scoped Compatibility
// Applications administration endpoints.
type V2AdminCompatibilityHandler struct {
	service   CompatibilityApplicationService
	publicURL string
}

// NewV2AdminCompatibilityHandler builds the handler. publicURL is the
// canonical externally reachable origin used for client URLs.
func NewV2AdminCompatibilityHandler(service CompatibilityApplicationService, publicURL string) *V2AdminCompatibilityHandler {
	return &V2AdminCompatibilityHandler{service: service, publicURL: strings.TrimSuffix(publicURL, "/")}
}

type compatibilityCommands struct {
	Install  string `json:"install"`
	Update   string `json:"update"`
	Rollback string `json:"rollback"`
	Remove   string `json:"remove"`
}

type compatibilityApplicationView struct {
	CompatibilityApplication
	CanonicalURL string                `json:"canonical_url"`
	Commands     compatibilityCommands `json:"commands"`
}

func (h *V2AdminCompatibilityHandler) HandleListApplications(w http.ResponseWriter, r *http.Request) {
	if !h.requirePlatformScope(w, r) {
		return
	}
	applications, err := h.service.ListApplications(r.Context())
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	views := make([]compatibilityApplicationView, 0, len(applications))
	for _, application := range applications {
		views = append(views, compatibilityApplicationView{
			CompatibilityApplication: application,
			CanonicalURL:             h.canonicalURL(application.Kind),
			Commands:                 operatorCommands(application.Kind),
		})
	}
	writeJSON(w, http.StatusOK, struct {
		Applications []compatibilityApplicationView `json:"applications"`
	}{views})
}

func (h *V2AdminCompatibilityHandler) HandleCreateEnrollment(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	var request struct {
		Kind         string   `json:"kind"`
		Capabilities []string `json:"capabilities"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	if !compatibilityKinds[request.Kind] {
		writeAdminValidation(w, map[string]string{compatFieldKind: compatKindValidationMessage})
		return
	}
	enrollment, err := h.service.CreateEnrollment(adminMutationRequestContext(r, claims), request.Kind, request.Capabilities)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	// The raw secret exists in exactly one response; it must never be cached
	// or logged along the way.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, struct {
		Enrollment CompatibilityEnrollment `json:"enrollment"`
	}{enrollment})
}

func (h *V2AdminCompatibilityHandler) HandleEnableApplication(w http.ResponseWriter, r *http.Request) {
	h.handleSetEnabled(w, r, true)
}

func (h *V2AdminCompatibilityHandler) HandleDisableApplication(w http.ResponseWriter, r *http.Request) {
	h.handleSetEnabled(w, r, false)
}

func (h *V2AdminCompatibilityHandler) handleSetEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	instanceID := chi.URLParam(r, "instance_id")
	var request struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	if request.ExpectedRevision <= 0 {
		writeAdminValidation(w, map[string]string{compatFieldExpectedRevision: compatRevisionValidationMessage})
		return
	}
	application, err := h.service.SetApplicationEnabled(adminMutationRequestContext(r, claims), instanceID, enabled, request.ExpectedRevision)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Application CompatibilityApplication `json:"application"`
	}{application})
}

func (h *V2AdminCompatibilityHandler) HandleRotateCredential(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	instanceID := chi.URLParam(r, "instance_id")
	var request struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	if request.ExpectedRevision <= 0 {
		writeAdminValidation(w, map[string]string{compatFieldExpectedRevision: compatRevisionValidationMessage})
		return
	}
	credential, application, err := h.service.RotateApplicationCredential(adminMutationRequestContext(r, claims), instanceID, request.ExpectedRevision)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Credential  CompatibilityCredential  `json:"credential"`
		Application CompatibilityApplication `json:"application"`
	}{credential, application})
}

func (h *V2AdminCompatibilityHandler) HandleRevokeApplication(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	instanceID := chi.URLParam(r, "instance_id")
	var request struct {
		ExpectedRevision int64 `json:"expected_revision"`
		Confirmed        bool  `json:"confirmed"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	fields := map[string]string{}
	if request.ExpectedRevision <= 0 {
		fields[compatFieldExpectedRevision] = compatRevisionValidationMessage
	}
	if !request.Confirmed {
		fields["confirmed"] = "must explicitly confirm revocation"
	}
	if len(fields) > 0 {
		writeAdminValidation(w, fields)
		return
	}
	application, err := h.service.RevokeApplication(adminMutationRequestContext(r, claims), instanceID, request.ExpectedRevision)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Application CompatibilityApplication `json:"application"`
	}{application})
}

func (h *V2AdminCompatibilityHandler) requirePlatformScope(w http.ResponseWriter, r *http.Request) bool {
	claims, ok := middleware.GetAdminContextClaims(r.Context())
	if !ok || claims.Scope != auth.AdminScopePlatform || claims.AccountID <= 0 {
		writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required")
		return false
	}
	if h == nil || h.service == nil {
		writeError(w, http.StatusServiceUnavailable, "compatibility_admin_unavailable", "Compatibility administration is unavailable")
		return false
	}
	return true
}

func (h *V2AdminCompatibilityHandler) requirePlatformMutation(w http.ResponseWriter, r *http.Request) (auth.AdminContextClaims, bool) {
	if !h.requirePlatformScope(w, r) {
		return auth.AdminContextClaims{}, false
	}
	claims, _ := middleware.GetAdminContextClaims(r.Context())
	return claims, true
}

func (h *V2AdminCompatibilityHandler) writeServiceError(w http.ResponseWriter, err error) {
	var revision *CompatibilityRevisionError
	switch {
	case errors.Is(err, ErrCompatibilityApplicationNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Compatibility application not found")
	case errors.As(err, &revision):
		writeJSON(w, http.StatusConflict, struct {
			Error           string `json:"error"`
			Message         string `json:"message"`
			CurrentRevision int64  `json:"current_revision"`
		}{compatCodeStateChanged, "Application state changed; reload and retry", revision.CurrentRevision})
	case errors.Is(err, ErrCompatibilityKindUnknown):
		writeAdminValidation(w, map[string]string{compatFieldKind: compatKindValidationMessage})
	case errors.Is(err, ErrCompatibilityCapabilityUnknown):
		writeAdminValidation(w, map[string]string{"capabilities": "must name reviewed capabilities"})
	default:
		writeError(w, http.StatusServiceUnavailable, "compatibility_admin_unavailable", "Compatibility administration is unavailable")
	}
}

// canonicalURL is the address a client of this application should use. Both
// applications share Bloem's canonical public origin.
func (h *V2AdminCompatibilityHandler) canonicalURL(kind string) string {
	base := h.publicURL
	if base == "" {
		base = "https://vondel.example"
	}
	if kind == compatKindAudiobookshelf {
		return base + "/audiobookshelf"
	}
	return base + "/"
}

// operatorCommands are the exact commands an operator (or their deployment
// controller) runs. Bloem displays them and never executes them: it has no
// Docker socket and performs no container mutation.
func operatorCommands(kind string) compatibilityCommands {
	overlay := fmt.Sprintf("docker-compose.%s.yml", kind)
	service := "vondel-" + kind
	compose := fmt.Sprintf("docker compose -f docker-compose.yml -f %s", overlay)
	return compatibilityCommands{
		Install:  compose + " up -d",
		Update:   fmt.Sprintf("%s pull %s && %s up -d %s", compose, service, compose, service),
		Rollback: fmt.Sprintf("Pin the previous image digest for %s in %s, then run: %s up -d %s", service, overlay, compose, service),
		// The state volume carries the Compose project prefix
		// (<project>_<service>-state), so a bare `docker volume rm
		// <service>-state` fails as typed. --filter name= is an unanchored
		// substring match, so it is anchored here — otherwise a host running
		// two Compose projects removes both — and piped through xargs -r so
		// no match removes nothing instead of erroring on an empty argument.
		Remove: fmt.Sprintf("%s rm -sf %s  # discard its disposable protocol state: docker volume ls -q --filter name='_%s-state$' | xargs -r docker volume rm", compose, service, service),
	}
}
