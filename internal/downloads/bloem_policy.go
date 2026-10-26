package downloads

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	policyengine "github.com/Silo-Server/silo-server/internal/policy"
)

// bloemCheckDownloadAction binds the resolved request tenant to a download
// action input before the decider sees it. The tenant facts are keyed on the
// input's final UserID (the policy user's ID when one is loaded). A missing or
// inactive tenant is an error, which every caller maps to ErrDownloadNotAllowed.
func bloemCheckDownloadAction(ctx context.Context, decider ActionDecider, input policyengine.ActionInput) (policyengine.ActionDecision, policyengine.Meta, error) {
	tenantFacts, err := policyengine.TenantFactsFromContext(ctx, input.UserID)
	if err != nil {
		return policyengine.ActionDecision{}, policyengine.Meta{}, err
	}
	input.Tenant = tenantFacts
	return decider.CheckAction(ctx, input)
}

// bloemEffectiveDownloadPolicy resolves the download policy for the
// (account, profile) subject. Tenant-aware providers require the validated
// request tenant; ordinary Silo providers retain account-only policy.
func (s *Service) bloemEffectiveDownloadPolicy(ctx context.Context, user *models.User) (access.EffectiveUserPolicy, error) {
	profileID := requestProfile(ctx, user.ID)
	if subject, ok := ctx.Value(bloemDownloadProfileKey{}).(bloemDownloadProfile); ok {
		// The profile/account binding belongs to group policy. With no group
		// provider, preserve Silo's user-row lookup semantics; an installed PDP
		// separately binds its final user ID to the validated tenant.
		if s.groupProvider != nil && subject.userID != user.ID {
			return access.EffectiveUserPolicy{}, ErrDownloadNotAllowed
		}
		profileID = subject.profileID
	}
	// Subject providers require the validated tenant even for an account-only
	// request. Ordinary Silo providers retain their account/user-row semantics.
	return access.EffectivePolicyForRequest(ctx, user, profileID, s.groupProvider)
}

// requestProfile is the request's verified profile when the request
// authenticated as accountID, so Silo's handlers, which pass no profile, still
// get the profile's policy. An explicit download profile takes precedence.
func requestProfile(ctx context.Context, accountID int) string {
	if claims := apimw.GetClaims(ctx); claims == nil || claims.UserID != accountID {
		return ""
	}
	return apimw.GetProfileID(ctx)
}

type bloemDownloadProfileKey struct{}

type bloemDownloadProfile struct {
	userID    int
	profileID string
}

func bloemWithDownloadProfile(ctx context.Context, userID int, profileID string) context.Context {
	return context.WithValue(ctx, bloemDownloadProfileKey{}, bloemDownloadProfile{userID: userID, profileID: profileID})
}

func (s *Service) bloemDownloadConfigForProfile(ctx context.Context, userID int, profileID, deviceID string) (config.DownloadConfig, *PolicyUser, error) {
	return s.downloadConfigForUser(bloemWithDownloadProfile(ctx, userID, profileID), userID, deviceID)
}

func (s *Service) bloemDownloadUserForProfile(ctx context.Context, userID int, profileID string, cfg config.DownloadConfig, deviceID string) (*PolicyUser, error) {
	return s.downloadUserForConfig(bloemWithDownloadProfile(ctx, userID, profileID), userID, cfg, deviceID)
}

// bloemSubtitleSourceAllowed rechecks a subtitle's source file against the
// viewer's library scope. EnsureAccessible checked the item; a multi-folder
// show's episode-file library membership can still diverge from its series'
// (see catalog.FileAllowedByLibraryScope). Library membership only, never
// quality: a viewer's transcode quality cap must never make a subtitle for a
// higher-resolution source 404.
func bloemSubtitleSourceAllowed(file *models.MediaFile, filter catalog.AccessFilter) bool {
	return catalog.FileAllowedByLibraryScope(file, filter.AllowedLibraryIDs, filter.DisabledLibraryIDs)
}

// bloemCheckSubtitleSourceFile loads a downloaded subtitle's source file and
// applies bloemSubtitleSourceAllowed.
func (s *Service) bloemCheckSubtitleSourceFile(ctx context.Context, mediaFileID int, filter catalog.AccessFilter) error {
	file, err := s.fileRepo.GetByID(ctx, mediaFileID)
	if err != nil {
		return fmt.Errorf("loading media file: %w", err)
	}
	if !bloemSubtitleSourceAllowed(file, filter) {
		return ErrAssetNotFound
	}
	return nil
}
