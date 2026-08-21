package policy

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// TenantLibraryResolver resolves the media folders visible to an authoritative
// tenant context.
type TenantLibraryResolver interface {
	AvailableMediaFolderIDs(context.Context, tenancy.Context) ([]int, error)
}

// ViewerResolver resolves viewer access scopes through the policy PDP.
type ViewerResolver struct {
	users           access.UserRepository
	storeFactory    userstore.UserStoreProvider
	tokens          access.ProfileTokenValidator
	pdp             *PDP
	tenantLibraries TenantLibraryResolver
	groups          access.GroupPolicyProvider
}

// NewViewerResolver creates a PDP-backed viewer scope resolver.
func NewViewerResolver(
	users access.UserRepository,
	storeFactory userstore.UserStoreProvider,
	tokens access.ProfileTokenValidator,
	pdp *PDP,
	tenantLibraries TenantLibraryResolver,
	groups ...access.GroupPolicyProvider,
) *ViewerResolver {
	var groupProvider access.GroupPolicyProvider
	if len(groups) > 0 {
		groupProvider = groups[0]
	}
	return &ViewerResolver{
		users:           users,
		storeFactory:    storeFactory,
		tokens:          tokens,
		pdp:             pdp,
		tenantLibraries: tenantLibraries,
		groups:          groupProvider,
	}
}

// Resolve computes the effective viewer scope for the request.
func (r *ViewerResolver) Resolve(ctx context.Context, input access.ResolveInput) (access.Scope, error) {
	user, err := r.users.GetByID(ctx, input.UserID)
	if err != nil {
		return access.Scope{}, fmt.Errorf("loading user %d: %w", input.UserID, err)
	}
	tenantFacts, err := TenantFactsFromContext(ctx, user.ID)
	if err != nil {
		return access.Scope{}, fmt.Errorf("resolve viewer scope tenant facts: %w", err)
	}

	store, err := r.storeFactory.ForUser(ctx, input.UserID)
	if err != nil {
		return access.Scope{}, fmt.Errorf("opening user store for %d: %w", input.UserID, err)
	}

	var profile *userstore.Profile
	if input.ProfileID != "" {
		profile, err = store.GetProfile(ctx, input.ProfileID)
		if err != nil {
			return access.Scope{}, fmt.Errorf("loading profile %s: %w", input.ProfileID, err)
		}
		if profile == nil {
			return access.Scope{}, access.ErrProfileNotFound
		}
	}

	subject := access.GroupSubject{AccountID: user.ID, ProfileID: input.ProfileID}
	if r.groups != nil {
		subject, err = access.GroupSubjectFromContext(ctx, user.ID, input.ProfileID)
		if err != nil {
			return access.Scope{}, fmt.Errorf("loading access group policy for user %d: %w", input.UserID, err)
		}
	}
	effective, err := access.EffectivePolicyForSubject(ctx, user, subject, r.groups)
	if err != nil {
		return access.Scope{}, fmt.Errorf("loading access group policy for user %d: %w", input.UserID, err)
	}

	profileVerified := input.ProfileID == ""
	if profile != nil {

		profileVerified, err = access.VerifyProfileForRequest(
			profile,
			input,
			user.ID,
			user.AccessPolicyRevision,
			r.tokens,
		)
		if err != nil {
			return access.Scope{}, err
		}
	}
	if r.tenantLibraries == nil {
		return access.Scope{}, fmt.Errorf("resolve viewer scope tenant libraries: missing resolver")
	}
	tenant, ok := tenancy.FromContext(ctx)
	if !ok {
		return access.Scope{}, fmt.Errorf("resolve viewer scope tenant libraries: %w", ErrTenantFactsUnavailable)
	}
	tenantLibraryIDs, err := r.tenantLibraries.AvailableMediaFolderIDs(ctx, tenant)
	if err != nil {
		return access.Scope{}, fmt.Errorf("resolve viewer scope tenant libraries: %w", err)
	}
	preferences := access.ResolveViewerPreferences(ctx, store, input.ProfileID)

	policyInput := ScopeInput{
		SchemaVersion:        1,
		Tenant:               tenantFacts,
		UserID:               user.ID,
		SessionID:            input.SessionID,
		ProfileID:            input.ProfileID,
		AccountLibraryIDs:    slices.Clone(effective.LibraryIDs),
		AccountRestricted:    effective.LibraryIDs != nil,
		AccountMaxQuality:    effective.MaxPlaybackQuality,
		AccessPolicyRevision: user.AccessPolicyRevision,
		DisabledLibraryIDs:   preferences.DisabledLibraryIDs,
		ProfileVerified:      profileVerified,
		TenantLibraryIDs:     slices.Clone(tenantLibraryIDs),
		RequestTime:          time.Now().UTC().Format(time.RFC3339),
		// ResolveInput cannot distinguish API keys from compat callers that
		// also skip PIN verification, so v1 leaves this false.
		IsAPIKey: false,
	}
	if profile != nil {
		policyInput.ProfilePresent = true
		policyInput.ProfileMaxRating = profile.MaxContentRating
		policyInput.ProfileMaxQuality = profile.MaxPlaybackQuality
		policyInput.ProfileLibraryLimited = profile.LibraryRestrictionsEnabled
		policyInput.ProfileLibraryIDs = slices.Clone(profile.AllowedLibraryIDs)
		policyInput.ProfileHasPIN = profile.PINHash != ""
		// Resolved canonically (profile scope -> contract default), not read off
		// the legacy profile column it migrated from. scope.rego relays this
		// value unchanged as a preference; the manifest deliberately declares no
		// constraint on it, since constraining a setting by a policy input fed
		// from that same setting would be circular.
		policyInput.ProfileMetadataLang = preferences.PreferredMetadataLanguage
	}

	if r.pdp == nil {
		return access.Scope{}, fmt.Errorf("resolve viewer scope policy: missing PDP")
	}
	decision, _, err := r.pdp.ResolveViewerScope(ctx, policyInput)
	if err != nil {
		return access.Scope{}, fmt.Errorf("resolve viewer scope policy: %w", err)
	}
	// The scope contract emits a tighten-only profile_verified output: a custom
	// override may revoke verification but never grant it. Enforce a revocation
	// the same way legacy PIN failures surface, so the middleware returns
	// 403 profile_unverified instead of silently proceeding.
	if profileVerified && !decision.ProfileVerified {
		return access.Scope{}, fmt.Errorf("%w: revoked by policy", access.ErrProfileUnverified)
	}

	var allowed []int
	if !decision.Unrestricted {
		allowed = slices.Clone(decision.AllowedLibraryIDs)
		if allowed == nil {
			allowed = []int{}
		}
	}
	disabled := slices.Clone(decision.DisabledLibraryIDs)
	if len(disabled) == 0 {
		disabled = nil
	}

	return access.Scope{
		UserID:                    user.ID,
		ProfileID:                 input.ProfileID,
		AllowedLibraryIDs:         allowed,
		DisabledLibraryIDs:        disabled,
		LibrariesRestricted:       decision.LibrariesRestricted,
		MaxContentRating:          decision.MaxContentRating,
		MaxPlaybackQuality:        decision.MaxPlaybackQuality,
		PlaybackAllowed:           effective.PlaybackAllowed,
		DownloadAllowed:           effective.DownloadAllowed,
		PreferredMetadataLanguage: decision.PreferredMetadataLanguage,
		MetadataLanguageOverrides: preferences.MetadataLanguageOverrides,
		PolicyRevision:            user.AccessPolicyRevision,
		// The policy output is tighten-only (merged_profile_verified), so a
		// custom override may revoke verification but never grant it. ANDing
		// with the Go-computed fact keeps that invariant even if a policy bug
		// emitted true for an unverified profile.
		ProfileVerified: profileVerified && decision.ProfileVerified,
	}, nil
}
