package downloads

import "context"

// CapabilityForProfile uses the active-profile extension when implemented.
// Upstream providers retain their account-only capability interface.
func CapabilityForProfile(ctx context.Context, service interface {
	Capability(context.Context, int) (Capability, error)
}, userID int, profileID string) (Capability, error) {
	if scoped, ok := service.(interface {
		CapabilityForProfile(context.Context, int, string) (Capability, error)
	}); ok {
		return scoped.CapabilityForProfile(ctx, userID, profileID)
	}
	return service.Capability(ctx, userID)
}

func (s *Service) CapabilityForProfile(ctx context.Context, userID int, profileID string) (Capability, error) {
	return s.Capability(bloemWithDownloadProfile(ctx, userID, profileID), userID)
}
