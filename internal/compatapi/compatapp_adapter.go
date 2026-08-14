package compatapi

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/Silo-Server/silo-server/internal/compatapp"
)

// CompatAppServices adapts the real compatapp.Service to this package's
// narrow interfaces. Task 4 was built against the plan-declared shape while
// Task 3 built the service itself on a parallel branch; this file is the one
// place the two meet, so a drift between them is a compile error here rather
// than a runtime surprise.
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

func (a *compatAppAdapter) Authenticate(ctx context.Context, bearer string, peerTLS *tls.ConnectionState) (AppIdentity, error) {
	identity, err := a.service.Authenticate(ctx, bearer, peerTLS)
	if err != nil {
		return AppIdentity{}, err
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

func (a *compatAppAdapter) Enroll(ctx context.Context, secret string, req EnrollmentRequest) (ServiceCredential, error) {
	requested := make([]compatapp.Capability, 0, len(req.RequestedCapabilities))
	for _, capability := range req.RequestedCapabilities {
		requested = append(requested, compatapp.Capability(capability))
	}
	// The service takes the kind from the enrollment secret rather than the
	// request — the admin chose it when minting the token, and a companion
	// cannot re-declare itself. req.Kind is therefore not forwarded.
	credential, err := a.service.Enroll(ctx, secret, compatapp.EnrollmentRequest{
		InstanceID:   req.InstanceID,
		Version:      req.Version,
		APIRangeMin:  req.API.Min,
		APIRangeMax:  req.API.Max,
		Capabilities: requested,
	})
	if err != nil {
		return ServiceCredential{}, err
	}
	return adaptCredential(credential), nil
}

// Renew maps this package's bearer-driven self-renewal onto the service's
// Rotate: the bearer authenticates first, so a credential can renew only the
// application it already proves, and rotation semantics (old credential dies)
// give renewal its revocation-on-renew property for free.
func (a *compatAppAdapter) Renew(ctx context.Context, bearer string) (ServiceCredential, error) {
	identity, err := a.service.Authenticate(ctx, bearer, nil)
	if err != nil {
		return ServiceCredential{}, err
	}
	credential, err := a.service.Rotate(ctx, identity.ApplicationID)
	if err != nil {
		return ServiceCredential{}, err
	}
	return adaptCredential(credential), nil
}

func (a *compatAppAdapter) Heartbeat(ctx context.Context, applicationID string, _ time.Time) error {
	return a.service.Heartbeat(ctx, applicationID, compatapp.HealthHealthy)
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
