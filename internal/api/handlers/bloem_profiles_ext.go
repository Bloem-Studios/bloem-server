package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// SetLifecycleIdempotency installs durable coordination for direct household
// profile creates, updates, and deletes.
func (h *ProfileHandler) SetLifecycleIdempotency(coordinator lifecycleidempotency.Coordinator, digester lifecycleidempotency.RequestDigester) {
	h.lifecycle = coordinator
	h.digest = digester
}

// HandleGetProfile handles GET /profiles/{id}.
//
// The household list at GET /profiles is account-scoped and stays that way. A
// direct-profile session is bound to one profile and needs to read that
// profile's own record — its name, avatar, and preferences — which the login
// response does not carry; RequireOwnDirectProfile holds such a session to its
// own id, and an account session may read any profile it owns.
func (h *ProfileHandler) HandleGetProfile(w http.ResponseWriter, r *http.Request) {
	userID := apimw.GetUserID(r.Context())
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	profileID := chi.URLParam(r, "id")
	if profileID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Profile ID is required")
		return
	}

	store, err := h.storeProvider.ForUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access user store")
		return
	}
	profile, err := store.GetProfile(r.Context(), profileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load profile")
		return
	}
	if profile == nil {
		writeError(w, http.StatusNotFound, "not_found", "Profile not found")
		return
	}

	writeJSON(w, http.StatusOK, h.toProfileResponse(r.Context(), store, *profile))
}

func isProfileEntitlementLimitError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.ConstraintName == "user_profiles_entitlement_limit"
}

func (h *ProfileHandler) effectiveProfileLimit(ctx context.Context, user *models.User) (int, *int64, error) {
	if user == nil {
		return 0, nil, nil
	}
	limit := user.MaxProfiles
	if h.AccessGroups == nil {
		return limit, cloneGroupID(user.AccessGroupID), nil
	}
	explicitOrganizationID := adminResourceOrganization(ctx)
	organizationID := explicitOrganizationID
	if organizationID == uuid.Nil {
		if tenant, ok := tenancy.FromContext(ctx); ok {
			organizationID = tenant.OrganizationID
		}
	}
	if organizationID == uuid.Nil {
		if user.AccessGroupID == nil {
			return limit, nil, nil
		}
		group, err := h.AccessGroups.GetForAccount(ctx, user.ID, *user.AccessGroupID)
		if err != nil {
			return 0, nil, err
		}
		return strictestProfileLimit(limit, group.MaxProfiles, group.ManagedTemplateKey != nil), cloneGroupID(&group.ID), nil
	}
	var group *access.Group
	var err error
	if explicitOrganizationID == uuid.Nil && user.AccessGroupID != nil {
		group, err = h.AccessGroups.Get(ctx, organizationID, *user.AccessGroupID)
		if errors.Is(err, access.ErrGroupNotFound) {
			group, err = h.AccessGroups.GetDefault(ctx, organizationID)
		}
	} else {
		group, err = h.AccessGroups.GetDefault(ctx, organizationID)
	}
	if err != nil {
		return 0, nil, err
	}
	return strictestProfileLimit(limit, group.MaxProfiles, group.ManagedTemplateKey != nil), cloneGroupID(&group.ID), nil
}

func cloneGroupID(id *int64) *int64 {
	if id == nil {
		return nil
	}
	copy := *id
	return &copy
}

func strictestProfileLimit(accountLimit, groupLimit int, managed bool) int {
	// Managed template max_profiles=0 means no secondary profiles; the primary
	// profile provisioned with an account still occupies the single allowed row.
	// Legacy unmanaged groups retain their historical 0=unlimited semantics.
	if managed && groupLimit == 0 {
		groupLimit = 1
	}
	if groupLimit <= 0 || accountLimit > 0 && accountLimit <= groupLimit {
		return accountLimit
	}
	return groupLimit
}

func profilesForOrganization(ctx context.Context, profiles []userstore.Profile) []userstore.Profile {
	organizationID := adminResourceOrganization(ctx)
	if organizationID == uuid.Nil {
		if tenant, ok := tenancy.FromContext(ctx); ok {
			organizationID = tenant.OrganizationID
		}
	}
	if organizationID == uuid.Nil {
		return profiles
	}
	filtered := make([]userstore.Profile, 0, len(profiles))
	for _, profile := range profiles {
		// Empty is the legacy/pre-migration representation of the deployment
		// default organization and must remain fail-closed for cap accounting.
		if profile.OrganizationID == "" || profile.OrganizationID == organizationID.String() {
			filtered = append(filtered, profile)
		}
	}
	return filtered
}

type createProfileRequest = ProfileCreateRequest

type updateProfileRequest = ProfileUpdateRequest

type profileResponse = ProfileView

// bloemProfileHandlerExt holds the Bloem-only ProfileHandler dependencies for
// lifecycle receipts on profile mutations.
type bloemProfileHandlerExt struct {
	lifecycle lifecycleidempotency.Coordinator
	digest    lifecycleidempotency.RequestDigester
}

// bloemProfileLifecycleRequest builds the lifecycle receipt request for a
// profile mutation when receipts are wired (nil otherwise). ok=false means
// it wrote the response.
func (h *ProfileHandler) bloemProfileLifecycleRequest(w http.ResponseWriter, r *http.Request, routeID string, selectors map[string]string, body []byte) (*lifecycleidempotency.Request, bool) {
	if h.lifecycle == nil {
		return nil, true
	}
	request, ok := h.profileLifecycleRequest(w, r, routeID, selectors, body)
	if !ok {
		return nil, false
	}
	return &request, true
}

// bloemWriteProfileLifecycleResult replays the stored lifecycle receipt as
// the response when the mutation ran under one. It reports whether it wrote.
func bloemWriteProfileLifecycleResult(w http.ResponseWriter, request *lifecycleidempotency.Request, result lifecycleidempotency.Result) bool {
	if request == nil {
		return false
	}
	writeLifecycleResult(w, result)
	return true
}

// bloemCreateProfileLifecycle creates a profile under a durable lifecycle
// receipt, recording the receipt on the command for the HTTP handler.
func (h *ProfileHandler) bloemCreateProfileLifecycle(ctx context.Context, cmd ProfileCreateCommand, avatarRef string, maxPlaybackQuality string, settingsSync []profileSettingSync) (ProfileView, error) {
	req := cmd.Request
	showForcedSubtitles := true
	if req.ShowForcedSubtitles != nil {
		showForcedSubtitles = *req.ShowForcedSubtitles
	}
	profile := userstore.Profile{
		ID:                         uuid.New().String(),
		Name:                       strings.TrimSpace(req.Name),
		Avatar:                     avatarRef,
		IsChild:                    req.IsChild,
		MaxContentRating:           req.MaxContentRating,
		QualityPreference:          req.QualityPreference,
		Language:                   req.Language,
		PreferredMetadataLanguage:  req.PreferredMetadataLanguage,
		SubtitleLanguage:           req.SubtitleLanguage,
		SubtitleMode:               req.SubtitleMode,
		AutoSkipIntro:              req.AutoSkipIntro,
		AutoSkipCredits:            req.AutoSkipCredits,
		AutoSkipRecap:              req.AutoSkipRecap,
		AutoPlayNextPreview:        req.AutoPlayNextPreview,
		ShowForcedSubtitles:        showForcedSubtitles,
		LibraryRestrictionsEnabled: req.LibraryRestrictionsEnabled,
		AllowedLibraryIDs:          req.AllowedLibraryIDs,
		MaxPlaybackQuality:         maxPlaybackQuality,
	}
	result, err := h.createProfileLifecycle(ctx, cmd, profile, settingsSync)
	if cmd.lifecycleResult != nil {
		*cmd.lifecycleResult = result
	}
	return profileLifecycleView(result, err)
}

// bloemProfileLimit enforces the effective (account and entitlement) profile
// ceiling for a new profile and returns the access group it inherits.
func (h *ProfileHandler) bloemProfileLimit(ctx context.Context, user *models.User, existing int) (*int64, error) {
	limit, inheritedGroupID, err := h.effectiveProfileLimit(ctx, user)
	if err != nil {
		return nil, apiError(500, "internal_error", "Failed to resolve profile limit")
	}
	if limit >= 1 && existing >= limit {
		return nil, apiError(409, "profile_limit_reached", fmt.Sprintf("This account has reached its profile limit (%d)", limit))
	}
	return inheritedGroupID, nil
}

// bloemUpdateProfilePrelude runs Bloem's pre-store profile update steps: it
// normalizes the name and plans the canonical settings sync before any store
// access, dispatches to the lifecycle receipt path when wired, and refuses a
// PIN change from a direct profile session. done=true means the caller
// returns (view, err) as is; otherwise req carries the normalized name and the
// Silo path below repeats the same (now idempotent) steps.
func (h *ProfileHandler) bloemUpdateProfilePrelude(ctx context.Context, cmd ProfileUpdateCommand, req *ProfileUpdateRequest, avatarRef *string, maxPlaybackQuality *string) (ProfileView, bool, error) {
	var none ProfileView
	if req.Name != nil {
		trimmedName := strings.TrimSpace(*req.Name)
		if trimmedName == "" {
			return none, true, fieldError("name", "Profile name is required")
		}
		req.Name = &trimmedName
	}
	settingsSync, err := planUpdateProfileSettingsSync(*req)
	if err != nil {
		return none, true, apiError(http.StatusBadRequest, "bad_request", err.Error())
	}
	input := userstore.UpdateProfileInput{
		Name: req.Name, Avatar: avatarRef, PIN: req.PIN, IsChild: req.IsChild,
		MaxContentRating: req.MaxContentRating, QualityPreference: req.QualityPreference,
		Language: req.Language, PreferredMetadataLanguage: req.PreferredMetadataLanguage,
		SubtitleLanguage: req.SubtitleLanguage, SubtitleMode: req.SubtitleMode,
		AutoSkipIntro: req.AutoSkipIntro, AutoSkipCredits: req.AutoSkipCredits,
		AutoSkipRecap: req.AutoSkipRecap, AutoPlayNextPreview: req.AutoPlayNextPreview,
		ShowForcedSubtitles: req.ShowForcedSubtitles, LibraryRestrictionsEnabled: req.LibraryRestrictionsEnabled,
		AllowedLibraryIDs: req.AllowedLibraryIDs, MaxPlaybackQuality: maxPlaybackQuality,
	}
	if h.lifecycle != nil {
		result, err := h.updateProfileLifecycle(ctx, cmd, input, settingsSync)
		if cmd.lifecycleResult != nil {
			*cmd.lifecycleResult = result
		}
		view, err := profileLifecycleView(result, err)
		return view, true, err
	}
	if req.PIN != nil && apimw.IsDirectProfileSession((&http.Request{}).WithContext(ctx)) {
		return none, true, apiError(http.StatusForbidden, "forbidden", "Direct profile sessions cannot change the profile PIN")
	}
	return none, false, nil
}
