package downloads

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	policyengine "github.com/Silo-Server/silo-server/internal/policy"
)

// bloemCheckDownloadAction binds the resolved request tenant to a download
// action input before the decider sees it. The tenant facts are keyed on the
// input's final UserID (the policy user's ID when one is loaded). A missing or
// inactive tenant is an error, which every caller maps to ErrDownloadNotAllowed.
func bloemCheckDownloadAction(ctx context.Context, decider ActionDecider, input policyengine.ActionInput) (policyengine.ActionDecision, policyengine.Meta, error) {
	if err := bloemBindDownloadTenant(ctx, &input); err != nil {
		return policyengine.ActionDecision{}, policyengine.Meta{}, err
	}
	return decider.CheckAction(ctx, input)
}

// bloemBindDownloadTenant sets input.Tenant to the request tenant's facts,
// keyed on the input's final UserID, for a caller that runs Silo's own
// CheckAction line next. A missing or inactive tenant is an error.
func bloemBindDownloadTenant(ctx context.Context, input *policyengine.ActionInput) error {
	tenantFacts, err := policyengine.TenantFactsFromContext(ctx, input.UserID)
	if err != nil {
		return err
	}
	input.Tenant = tenantFacts
	return nil
}

// bloemDownloadPolicyContext prepares ctx for Silo's EffectivePolicyForUser
// in effectiveDownloadUser: the policy is the (account, profile) subject's, and
// a tenant-scoped provider needs the validated request tenant (no fallback to
// the no-group policy). The profile is an explicit download profile when the
// caller set one for this account, else the request's verified profile.
// Ordinary Silo providers keep account-only policy.
func (s *Service) bloemDownloadPolicyContext(ctx context.Context, user *models.User) (context.Context, error) {
	profileID := requestProfile(ctx, user.ID)
	if subject, ok := ctx.Value(bloemDownloadProfileKey{}).(bloemDownloadProfile); ok {
		// The profile/account binding belongs to group policy. With no group
		// provider, preserve Silo's user-row lookup semantics; an installed PDP
		// separately binds its final user ID to the validated tenant.
		if s.groupProvider != nil && subject.userID != user.ID {
			return ctx, ErrDownloadNotAllowed
		}
		profileID = subject.profileID
	}
	return access.WithPolicyProfile(access.RequireTenantPolicy(ctx), profileID), nil
}

// bloemTenantActionDecider wraps the download action decider so every one of
// Silo's CheckAction calls binds the request tenant first
// (bloemCheckDownloadAction). SetActionDecider installs it; nil stays nil, so
// Silo's legacy inline checks still apply without a decider.
func bloemTenantActionDecider(decider ActionDecider) ActionDecider {
	if decider == nil {
		return nil
	}
	if _, wrapped := decider.(tenantActionDecider); wrapped {
		return decider
	}
	return tenantActionDecider{inner: decider}
}

type tenantActionDecider struct{ inner ActionDecider }

func (d tenantActionDecider) CheckAction(ctx context.Context, input policyengine.ActionInput) (policyengine.ActionDecision, policyengine.Meta, error) {
	return bloemCheckDownloadAction(ctx, d.inner, input)
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
