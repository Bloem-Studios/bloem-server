package compatapi

import (
	"context"
	"crypto/tls"
	"errors"
	"time"

	"github.com/Silo-Server/silo-server/internal/compatapp"
)

// CompatAppServices adapts the real compatapp.Service to this package's
// narrow interfaces. Task 4 was built against the plan-declared shape while
// Task 3 built the service itself on a parallel branch; this file is the one
// place the two meet, so a drift between them is a compile error here rather
// than a runtime surprise. The capability vocabulary itself is pinned by
// TestCapabilityVocabularyMatchesEnrollmentRegistry.
func CompatAppServices(service *compatapp.Service) Services {
	adapter := &compatAppAdapter{service: service}
	return Services{
		Apps:       adapter,
		Enroller:   adapter,
		Renewer:    adapter,
		Heartbeats: adapter,
	}
}

type compatAppAdapter struct {
	service *compatapp.Service
}

// mapCompatAppError translates compatapp sentinels into this package's
// sentinels so the handler renders the stable envelope instead of collapsing
// every service failure to 500. errors.Join keeps the original error visible
// to errors.Is and logs; the handler never echoes error text to a companion.
func mapCompatAppError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, compatapp.ErrEnrollmentDenied),
		errors.Is(err, compatapp.ErrCredentialInvalid),
		errors.Is(err, compatapp.ErrApplicationRevoked),
		errors.Is(err, compatapp.ErrApplicationDisabled),
		errors.Is(err, compatapp.ErrApplicationNotFound),
		errors.Is(err, compatapp.ErrPeerCertificateRequired),
		errors.Is(err, compatapp.ErrPeerCertificateMismatch):
		// All credential/trust refusals answer 401 through one sentinel so a
		// caller cannot distinguish "wrong secret" from "revoked" from
		// "unknown application".
		return errors.Join(ErrInvalidCredentials, err)
	// A revision mismatch belongs to the administrative surface rather than
	// this one, but it is a conflict wherever it surfaces, and mapping it
	// keeps an unreachable path from degrading into an opaque server error
	// if the surfaces ever meet.
	case errors.Is(err, compatapp.ErrRevisionMismatch),
		errors.Is(err, compatapp.ErrInstanceAlreadyEnrolled),
		errors.Is(err, compatapp.ErrAPIRangeUnsupported),
		errors.Is(err, compatapp.ErrKindMismatch):
		return errors.Join(ErrConflict, err)
	case errors.Is(err, compatapp.ErrUnknownKind),
		errors.Is(err, compatapp.ErrUnknownCapability),
		errors.Is(err, compatapp.ErrNoCapabilities),
		errors.Is(err, compatapp.ErrCapabilityNotGranted),
		errors.Is(err, compatapp.ErrInvalidEnrollmentRequest),
		errors.Is(err, compatapp.ErrInvalidHealthStatus):
		return errors.Join(ErrInvalid, err)
	}
	return err
}

func (a *compatAppAdapter) Authenticate(ctx context.Context, bearer string, peerTLS *tls.ConnectionState) (AppIdentity, error) {
	identity, err := a.service.Authenticate(ctx, bearer, peerTLS)
	if err != nil {
		return AppIdentity{}, mapCompatAppError(err)
	}
	capabilities := make([]string, 0, len(identity.Capabilities))
	for _, capability := range identity.Capabilities {
		capabilities = append(capabilities, string(capability))
	}
	return AppIdentity{
		ApplicationID: identity.ApplicationID,
		Kind:          string(identity.Kind),
		InstanceID:    identity.InstanceID,
		Capabilities:  capabilities,
	}, nil
}

func (a *compatAppAdapter) Enroll(ctx context.Context, secret string, req EnrollmentRequest, peerTLS *tls.ConnectionState) (ServiceCredential, error) {
	requested := make([]compatapp.Capability, 0, len(req.RequestedCapabilities))
	for _, capability := range req.RequestedCapabilities {
		requested = append(requested, compatapp.Capability(capability))
	}
	// The authoritative kind comes from the enrollment secret; the request's
	// kind travels as a declaration the service refuses on contradiction.
	// The connection state travels too, so a presented client certificate is
	// bound to the application and required from then on.
	credential, err := a.service.Enroll(ctx, secret, compatapp.EnrollmentRequest{
		InstanceID:   req.InstanceID,
		Version:      req.Version,
		ImageDigest:  req.ImageDigest,
		APIRangeMin:  req.API.Min,
		APIRangeMax:  req.API.Max,
		DeclaredKind: compatapp.Kind(req.Kind),
		Capabilities: requested,
		PeerTLS:      peerTLS,
	})
	if err != nil {
		return ServiceCredential{}, mapCompatAppError(err)
	}
	return adaptCredential(credential), nil
}

// Renew maps self-renewal onto the service's Rotate for the application the
// middleware already authenticated — including its TLS binding, which is why
// no re-authentication happens here. Rotation semantics (the old credential
// dies) give renewal its revocation-on-renew property for free.
func (a *compatAppAdapter) Renew(ctx context.Context, applicationID string) (ServiceCredential, error) {
	credential, err := a.service.Rotate(ctx, applicationID)
	if err != nil {
		return ServiceCredential{}, mapCompatAppError(err)
	}
	return adaptCredential(credential), nil
}

func (a *compatAppAdapter) Heartbeat(ctx context.Context, applicationID string, status HealthStatus, _ time.Time) error {
	return mapCompatAppError(a.service.Heartbeat(ctx, applicationID, compatapp.HealthStatus(status)))
}

func adaptCredential(credential compatapp.ServiceCredential) ServiceCredential {
	granted := make([]string, 0, len(credential.Capabilities))
	for _, capability := range credential.Capabilities {
		granted = append(granted, string(capability))
	}
	return ServiceCredential{
		ApplicationID:       credential.ApplicationID,
		Token:               credential.Secret,
		ExpiresAt:           credential.ExpiresAt,
		GrantedCapabilities: granted,
	}
}
