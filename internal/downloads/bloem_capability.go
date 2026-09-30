package downloads

import (
	"context"
	"fmt"
)

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
	cfg := s.loadConfig(ctx)
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return Capability{}, fmt.Errorf("loading user: %w", err)
	}
	policyUser, err := s.effectiveDownloadUser(ctx, user, profileID)
	if err != nil {
		return Capability{}, fmt.Errorf("loading access group policy: %w", err)
	}
	c := Capability{
		Enabled:              cfg.Enabled,
		DownloadAllowed:      policyUser.Policy.DownloadAllowed,
		QualityPresets:       []string{},
		TranscodeEnabled:     cfg.TranscodeEnabled,
		TranscodeUserAllowed: policyUser.Policy.DownloadTranscodeAllowed,
	}
	policyCeiling := ""
	if s.actionDecider != nil {
		c.QualityPresets, policyCeiling = s.policyPresetsFor(ctx, policyUser, cfg, s.artifacts != nil)
	} else {
		c.QualityPresets = s.policy.PresetsFor(policyUser, cfg, s.artifacts != nil)
	}
	c.QualityOptions = qualityOptionsFor(c.QualityPresets, cfg, policyUser, policyCeiling)
	if len(c.QualityPresets) > 0 {
		// Per-season download is always available when downloads are enabled;
		// auto-download monitoring additionally requires the subscription repo.
		c.SeasonDownload = true
		if s.subRepo != nil {
			c.SeriesMonitoring = true
			c.MonitoringModes = []string{SubModeAll, SubModeFuture, SubModeLatestSeason, SubModeSpecificSeasons}
		}
	}
	return c, nil
}
