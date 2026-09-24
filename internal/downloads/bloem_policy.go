package downloads

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
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
// (account, profile) subject. With a group provider the subject is bound to
// the request tenant; without one it is the bare account/profile pair.
func (s *Service) bloemEffectiveDownloadPolicy(ctx context.Context, user *models.User, profileID string) (access.EffectiveUserPolicy, error) {
	subject := access.GroupSubject{AccountID: user.ID, ProfileID: profileID}
	if s.groupProvider != nil {
		var err error
		subject, err = access.GroupSubjectFromContext(ctx, user.ID, profileID)
		if err != nil {
			return access.EffectiveUserPolicy{}, err
		}
	}
	return access.EffectivePolicyForSubject(ctx, user, subject, s.groupProvider)
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
