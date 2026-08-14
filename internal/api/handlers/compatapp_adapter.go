package handlers

import (
	"context"
	"errors"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/compatapp"
)

// This file is the seam between the Compatibility Applications admin surface
// and the enrollment/trust service that backs it. The handler declares a
// narrow interface and its own sentinels; internal/compatapp owns application
// state and speaks its own vocabulary. Everything that translates between the
// two lives here, so a drift between them is a compile error in one file
// rather than a runtime surprise — and, as internal/compatapi/compatapp_adapter.go
// records, a seam nobody tests is where a completely broken feature hid for a
// whole review round. The mappings themselves are pinned by
// compatapp_adapter_test.go, and the whole chain down to PostgreSQL by
// compatapp_adapter_integration_test.go.

// The single word the admin surface leads each application with.
const (
	compatStateEnabled  = "enabled"
	compatStateDisabled = "disabled"
	compatStateRevoked  = "revoked"
)

// NewCompatApplicationService adapts the application lifecycle service to the
// admin surface's interface. It returns an untyped nil when there is no
// service, so a database-less deployment leaves the surface unmounted instead
// of mounting one that panics on first use.
func NewCompatApplicationService(service *compatapp.Service) CompatibilityApplicationService {
	if service == nil {
		return nil
	}
	return &compatApplicationAdapter{service: service}
}

type compatApplicationAdapter struct {
	service *compatapp.Service
}

var _ CompatibilityApplicationService = (*compatApplicationAdapter)(nil)

func (a *compatApplicationAdapter) ListApplications(ctx context.Context) ([]CompatibilityApplication, error) {
	applications, err := a.service.ListApplications(ctx)
	if err != nil {
		return nil, mapCompatApplicationError(err)
	}
	views := make([]CompatibilityApplication, 0, len(applications))
	for _, application := range applications {
		views = append(views, adaptCompatibilityApplication(application))
	}
	return views, nil
}

func (a *compatApplicationAdapter) CreateEnrollment(ctx context.Context, kind string, capabilities []string) (CompatibilityEnrollment, error) {
	requested := make([]compatapp.Capability, 0, len(capabilities))
	for _, capability := range capabilities {
		requested = append(requested, compatapp.Capability(capability))
	}
	enrollment, err := a.service.CreateEnrollment(ctx, compatapp.Kind(kind), requested)
	if err != nil {
		return CompatibilityEnrollment{}, mapCompatApplicationError(err)
	}
	// The raw secret passes through untouched and is never stored, logged, or
	// echoed anywhere else; the handler sends it once with Cache-Control:
	// no-store.
	return CompatibilityEnrollment{
		Kind:      string(enrollment.Kind),
		Secret:    enrollment.Secret,
		ExpiresAt: enrollment.ExpiresAt,
	}, nil
}

func (a *compatApplicationAdapter) SetApplicationEnabled(ctx context.Context, instanceID string, enabled bool, expectedRevision int64) (CompatibilityApplication, error) {
	application, err := a.service.SetEnabledByInstance(ctx, instanceID, enabled, expectedRevision)
	if err != nil {
		return CompatibilityApplication{}, mapCompatApplicationError(err)
	}
	return adaptCompatibilityApplication(application), nil
}

func (a *compatApplicationAdapter) RotateApplicationCredential(ctx context.Context, instanceID string, expectedRevision int64) (CompatibilityCredential, CompatibilityApplication, error) {
	credential, application, err := a.service.RotateByInstance(ctx, instanceID, expectedRevision)
	if err != nil {
		return CompatibilityCredential{}, CompatibilityApplication{}, mapCompatApplicationError(err)
	}
	return CompatibilityCredential{
		Secret:    credential.Secret,
		ExpiresAt: credential.ExpiresAt,
	}, adaptCompatibilityApplication(application), nil
}

func (a *compatApplicationAdapter) RevokeApplication(ctx context.Context, instanceID string, expectedRevision int64) (CompatibilityApplication, error) {
	application, err := a.service.RevokeByInstance(ctx, instanceID, expectedRevision)
	if err != nil {
		return CompatibilityApplication{}, mapCompatApplicationError(err)
	}
	return adaptCompatibilityApplication(application), nil
}

// mapCompatApplicationError translates lifecycle-service failures into the
// admin surface's sentinels. Anything left untranslated reaches the handler's
// default branch as an unavailable service, so every refusal the service can
// express deliberately appears here.
func mapCompatApplicationError(err error) error {
	if err == nil {
		return nil
	}
	var mismatch *compatapp.RevisionMismatchError
	var revoked *compatapp.ApplicationRevokedError
	switch {
	case errors.As(err, &mismatch):
		return &CompatibilityRevisionError{CurrentRevision: mismatch.Current}
	case errors.As(err, &revoked):
		// Revocation is terminal state the caller has not seen yet. The
		// surface's only conflict channel is the revision conflict, and the
		// instruction it carries — reload and retry — is the right one: the
		// reloaded row shows the application revoked and the control is gone.
		return &CompatibilityRevisionError{CurrentRevision: revoked.Current}
	case errors.Is(err, compatapp.ErrApplicationNotFound):
		return ErrCompatibilityApplicationNotFound
	case errors.Is(err, compatapp.ErrUnknownKind):
		return ErrCompatibilityKindUnknown
	case errors.Is(err, compatapp.ErrUnknownCapability),
		errors.Is(err, compatapp.ErrNoCapabilities),
		errors.Is(err, compatapp.ErrCapabilityNotGranted):
		return ErrCompatibilityCapabilityUnknown
	}
	return err
}

// adaptCompatibilityApplication renders one trust record as the read-only
// administrative view. It copies named fields rather than the whole record,
// so a field added to the trust record — a fingerprint, a digest, anything
// secret-adjacent — cannot reach an admin response by accident.
func adaptCompatibilityApplication(application compatapp.Application) CompatibilityApplication {
	capabilities := make([]string, 0, len(application.Capabilities))
	for _, capability := range application.Capabilities {
		capabilities = append(capabilities, string(capability))
	}
	return CompatibilityApplication{
		InstanceID:  application.InstanceID,
		Kind:        string(application.Kind),
		State:       compatibilityApplicationState(application),
		Enabled:     application.Enabled,
		Revoked:     application.RevokedAt != nil,
		Healthy:     application.Health == compatapp.HealthHealthy,
		Version:     application.Version,
		ImageDigest: application.ImageDigest,
		APIRange: CompatibilityAPIRange{
			Min: strconv.Itoa(application.APIRangeMin),
			Max: strconv.Itoa(application.APIRangeMax),
		},
		LastContactAt: application.LastContactAt,
		// The trust store keeps no session ledger, and no other component
		// attributes live sessions to a compatibility application yet, so
		// this stays 0 rather than reporting a number nothing measures.
		ActiveSessions: 0,
		Capabilities:   capabilities,
		Revision:       application.Revision,
	}
}

// compatibilityApplicationState is the single word the surface leads with.
// Revocation outranks the reversible off switch: a revoked application must
// never read as enabled, whatever its enabled flag still says.
func compatibilityApplicationState(application compatapp.Application) string {
	switch {
	case application.RevokedAt != nil:
		return compatStateRevoked
	case !application.Enabled:
		return compatStateDisabled
	default:
		return compatStateEnabled
	}
}
