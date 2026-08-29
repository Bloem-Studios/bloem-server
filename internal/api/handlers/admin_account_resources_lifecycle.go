package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiresponse"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type adminLifecycleProfileStore interface {
	PreferenceSettingsWriterInTransaction(context.Context, pgx.Tx) (userstore.PreferenceSettingsWriter, error)
	GetProfileInTransaction(context.Context, pgx.Tx, string) (*userstore.Profile, error)
	ListProfilesInTransaction(context.Context, pgx.Tx) ([]userstore.Profile, error)
	ListSettingValuesForResolutionInTransaction(context.Context, pgx.Tx, userstore.SettingResolutionQuery) ([]userstore.SettingValue, error)
	DeleteProfileInTransaction(context.Context, pgx.Tx, string) error
	DeleteDeviceInTransaction(context.Context, pgx.Tx, []string, string) ([]userstore.SettingIdentity, error)
}

type transactionalProfileAccessGroups interface {
	GetInTransaction(context.Context, pgx.Tx, uuid.UUID, int64) (*access.Group, error)
	GetForAccountInTransaction(context.Context, pgx.Tx, int, int64) (*access.Group, error)
	GetDefaultInTransaction(context.Context, pgx.Tx, uuid.UUID) (*access.Group, error)
}

type transactionProfileResolutionStore struct {
	store adminLifecycleProfileStore
	tx    pgx.Tx
}

func (s transactionProfileResolutionStore) ListSettingValuesForResolution(ctx context.Context, query userstore.SettingResolutionQuery) ([]userstore.SettingValue, error) {
	return s.store.ListSettingValuesForResolutionInTransaction(ctx, s.tx, query)
}

func lifecycleActor(r *http.Request) (int, uuid.UUID, bool) {
	claims := apimw.GetClaims(r.Context())
	if claims == nil {
		return 0, uuid.Nil, false
	}
	incarnation, err := uuid.Parse(claims.AccountIncarnationID)
	return claims.UserID, incarnation, err == nil && claims.UserID > 0 && incarnation != uuid.Nil
}

func lifecycleJSONResult(status int, value any) (lifecycleidempotency.Result, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return lifecycleidempotency.Result{}, err
	}
	body = append(body, '\n')
	return lifecycleidempotency.Result{Status: status, Body: body, Headers: map[string][]string{"Content-Type": {"application/json"}}}, nil
}

func adminResourceLifecycleScope(ctx context.Context, accountRoute string, userID int, selectors map[string]string) (string, lifecycleidempotency.TargetSource, func(context.Context, pgx.Tx) ([]lifecycleidempotency.TargetBinding, error)) {
	organizationID := adminResourceOrganization(ctx)
	if organizationID == uuid.Nil {
		return accountRoute, lifecycleidempotency.TargetPathAccount, func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
			return lifecycleidempotency.ResolveAccountTargets(ctx, tx, userID)
		}
	}
	selectors["tenant_id"] = organizationID.String()
	route := strings.Replace(accountRoute, "account.", "tenant.member.", 1)
	return route, lifecycleidempotency.TargetPathTenantMember, func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
		target, err := lifecycleidempotency.ResolveTenantMemberTarget(ctx, tx, organizationID, userID)
		if err != nil {
			return nil, err
		}
		return []lifecycleidempotency.TargetBinding{target}, nil
	}
}

func (h *AdminHandler) effectiveProfileLimitInTransaction(ctx context.Context, tx pgx.Tx, user *models.User) (int, *int64, error) {
	if user == nil {
		return 0, nil, nil
	}
	limit := user.MaxProfiles
	if h.AccessGroups == nil {
		return limit, cloneGroupID(user.AccessGroupID), nil
	}
	groups, ok := h.AccessGroups.(transactionalProfileAccessGroups)
	if !ok {
		return 0, nil, errors.New("access groups do not support caller-owned transactions")
	}
	explicitOrganizationID := adminResourceOrganization(ctx)
	organizationID := explicitOrganizationID
	if organizationID == uuid.Nil {
		if tenant, exists := tenancy.FromContext(ctx); exists {
			organizationID = tenant.OrganizationID
		}
	}
	if organizationID == uuid.Nil {
		if user.AccessGroupID == nil {
			return limit, nil, nil
		}
		group, err := groups.GetForAccountInTransaction(ctx, tx, user.ID, *user.AccessGroupID)
		if err != nil {
			return 0, nil, err
		}
		return strictestProfileLimit(limit, group.MaxProfiles, group.ManagedTemplateKey != nil), cloneGroupID(&group.ID), nil
	}
	var group *access.Group
	var err error
	if explicitOrganizationID == uuid.Nil && user.AccessGroupID != nil {
		group, err = groups.GetInTransaction(ctx, tx, organizationID, *user.AccessGroupID)
		if errors.Is(err, access.ErrGroupNotFound) {
			group, err = groups.GetDefaultInTransaction(ctx, tx, organizationID)
		}
	} else {
		group, err = groups.GetDefaultInTransaction(ctx, tx, organizationID)
	}
	if err != nil {
		return 0, nil, err
	}
	return strictestProfileLimit(limit, group.MaxProfiles, group.ManagedTemplateKey != nil), cloneGroupID(&group.ID), nil
}

func (h *AdminHandler) handleLifecycleCreateUserProfile(w http.ResponseWriter, r *http.Request) {
	selector := strings.TrimSpace(chi.URLParam(r, "user_id"))
	userID, err := strconv.Atoi(selector)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid user id")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	var req createProfileRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Profile name is required")
		return
	}
	avatarRef, err := normalizePresetAvatarReference(req.Avatar)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	maxPlaybackQuality, ok := access.ParsePlaybackQualityPreset(req.MaxPlaybackQuality)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid max_playback_quality")
		return
	}
	settingsSync, err := planCreateProfileSettingsSync(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	actorID, actorIncarnation, ok := lifecycleActor(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authenticated account identity is incomplete")
		return
	}
	selectors := map[string]string{"user_id": selector}
	routeID, targetSource, resolveTargets := adminResourceLifecycleScope(r.Context(), "account.profile.create", userID, selectors)
	request := lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID,
			ActorAccountIncarnationID: &actorIncarnation, Method: r.Method, RouteID: routeID,
			RequestHash:  h.lifecycleDigest(r.Method, routeID, selectors, r.URL.Query(), body),
			TargetSource: targetSource,
		},
		ResolveTargets: resolveTargets,
	}
	var changedKeys []string
	var createdProfileID string
	result, err := h.lifecycle.Execute(r.Context(), request, func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
		users, ok := h.userRepo.(transactionalAdminUserRepository)
		if !ok || h.storeProv == nil {
			return lifecycleidempotency.Result{}, errors.New("user resources do not support caller-owned transactions")
		}
		user, err := users.GetByIDInTransaction(ctx, tx, userID)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		baseStore, err := h.storeProv.ForUser(ctx, userID)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		store, ok := baseStore.(adminLifecycleProfileStore)
		if !ok {
			return lifecycleidempotency.Result{}, errors.New("user store does not support caller-owned lifecycle transactions")
		}
		profiles, err := store.ListProfilesInTransaction(ctx, tx)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		limit, inheritedGroupID, err := h.effectiveProfileLimitInTransaction(ctx, tx, user)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		if limit >= 1 && len(profiles) >= limit {
			return lifecycleidempotency.Result{}, errLifecycleProfileLimit
		}
		if profileNameConflicts(profiles, name, "") {
			return lifecycleidempotency.Result{}, errLifecycleProfileNameConflict
		}
		showForcedSubtitles := true
		if req.ShowForcedSubtitles != nil {
			showForcedSubtitles = *req.ShowForcedSubtitles
		}
		profile := userstore.Profile{ID: uuid.NewString(), Name: name, Avatar: avatarRef, IsChild: req.IsChild,
			MaxContentRating: req.MaxContentRating, QualityPreference: req.QualityPreference, Language: req.Language,
			PreferredMetadataLanguage: req.PreferredMetadataLanguage, SubtitleLanguage: req.SubtitleLanguage,
			SubtitleMode: req.SubtitleMode, AutoSkipIntro: req.AutoSkipIntro, AutoSkipCredits: req.AutoSkipCredits,
			AutoSkipRecap: req.AutoSkipRecap, AutoPlayNextPreview: req.AutoPlayNextPreview,
			ShowForcedSubtitles: showForcedSubtitles, LibraryRestrictionsEnabled: req.LibraryRestrictionsEnabled,
			AllowedLibraryIDs: req.AllowedLibraryIDs, MaxPlaybackQuality: maxPlaybackQuality, AccessGroupID: inheritedGroupID}
		if organizationID := adminResourceOrganization(ctx); organizationID != uuid.Nil {
			profile.OrganizationID = organizationID.String()
		}
		createdProfileID = profile.ID
		writer, err := store.PreferenceSettingsWriterInTransaction(ctx, tx)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		if err := writer.CreateProfile(ctx, profile); err != nil {
			return lifecycleidempotency.Result{}, err
		}
		inherited, err := planInheritedLegacyUserSettings(ctx, writer)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		changedKeys, err = writeCanonicalSettingsSync(ctx, writer, userstore.SettingIdentity{Scope: settingscontract.ScopeProfile, ProfileID: profile.ID}, append(settingsSync, inherited...))
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		if req.PIN != "" {
			if err := writer.UpdateProfile(ctx, profile.ID, userstore.UpdateProfileInput{PIN: &req.PIN}); err != nil {
				return lifecycleidempotency.Result{}, err
			}
		}
		if req.ShowForcedSubtitles != nil && !*req.ShowForcedSubtitles {
			if err := writer.UpdateProfile(ctx, profile.ID, userstore.UpdateProfileInput{ShowForcedSubtitles: req.ShowForcedSubtitles}); err != nil {
				return lifecycleidempotency.Result{}, err
			}
		}
		created, err := store.GetProfileInTransaction(ctx, tx, profile.ID)
		if err != nil || created == nil {
			return lifecycleidempotency.Result{}, fmt.Errorf("retrieve created profile: %w", err)
		}
		resolutionStore := transactionProfileResolutionStore{store: store, tx: tx}
		prefs := resolveProfilePreferences(ctx, resolutionStore, []string{created.ID})
		response := h.adminResourceProfileHandler().profileResponseWith(ctx, *created, prefs[created.ID])
		return lifecycleJSONResult(http.StatusCreated, response)
	})
	if err != nil {
		switch {
		case errors.Is(err, errLifecycleProfileLimit), isProfileEntitlementLimitError(err):
			writeError(w, http.StatusUnprocessableEntity, "profile_limit_reached", "This account has reached its profile limit")
		case errors.Is(err, errLifecycleProfileNameConflict):
			writeError(w, http.StatusConflict, "name_conflict", "A profile with this name already exists")
		default:
			h.writeAccountResourceLifecycleError(w, err, "Failed to create profile")
		}
		return
	}
	if !result.Replayed {
		for _, key := range changedKeys {
			publishUserSettingsEvent(r.Context(), h.EventsHub, userID, createdProfileID, key, string(settingscontract.ScopeProfile))
		}
	}
	writeLifecycleResult(w, result)
}

func (h *AdminHandler) profileLifecycleRequest(r *http.Request, routeID string, userID int, userSelector, profileID string, body []byte) (lifecycleidempotency.Request, error) {
	actorID, actorIncarnation, ok := lifecycleActor(r)
	if !ok {
		return lifecycleidempotency.Request{}, lifecycleidempotency.ErrInvalidBinding
	}
	selectors := map[string]string{"user_id": userSelector, "profile_id": profileID}
	routeID, targetSource, resolveBase := adminResourceLifecycleScope(r.Context(), routeID, userID, selectors)
	return lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID,
			ActorAccountIncarnationID: &actorIncarnation, Method: r.Method, RouteID: routeID,
			RequestHash:  h.lifecycleDigest(r.Method, routeID, selectors, r.URL.Query(), body),
			TargetSource: targetSource,
		},
		ResolveTargets: func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
			targets, err := resolveBase(ctx, tx)
			if err != nil {
				return nil, err
			}
			organizationID := adminResourceOrganization(ctx)
			var actualOrganizationID uuid.UUID
			err = tx.QueryRow(ctx, `SELECT organization_id FROM user_profiles WHERE user_id=$1 AND id=$2 FOR UPDATE`, userID, profileID).Scan(&actualOrganizationID)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && organizationID != uuid.Nil && organizationID != actualOrganizationID) {
				return nil, errLifecycleProfileNotFound
			}
			if err != nil {
				return nil, err
			}
			targets[0].ProfileID = profileID
			return targets, nil
		},
	}, nil
}

func parseLifecycleProfileSelectors(w http.ResponseWriter, r *http.Request) (int, string, string, bool) {
	userSelector := strings.TrimSpace(chi.URLParam(r, "user_id"))
	userID, err := strconv.Atoi(userSelector)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid user id")
		return 0, "", "", false
	}
	profileID := strings.TrimSpace(chi.URLParam(r, "profile_id"))
	if profileID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Profile ID is required")
		return 0, "", "", false
	}
	return userID, userSelector, profileID, true
}

func (h *AdminHandler) lifecycleProfileStore(ctx context.Context, tx pgx.Tx, userID int) (adminLifecycleProfileStore, error) {
	if h.storeProv == nil {
		return nil, errors.New("user resources are not configured")
	}
	baseStore, err := h.storeProv.ForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	store, ok := baseStore.(adminLifecycleProfileStore)
	if !ok {
		return nil, errors.New("user store does not support caller-owned lifecycle transactions")
	}
	return store, nil
}

func (h *AdminHandler) handleLifecycleUpdateUserProfile(w http.ResponseWriter, r *http.Request) {
	userID, userSelector, profileID, ok := parseLifecycleProfileSelectors(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	var req updateProfileRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	var avatarRef *string
	if req.Avatar != nil {
		normalized, err := normalizePresetAvatarReference(*req.Avatar)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		avatarRef = &normalized
	}
	var maxPlaybackQuality *string
	if req.MaxPlaybackQuality != nil {
		normalized, valid := access.ParsePlaybackQualityPreset(*req.MaxPlaybackQuality)
		if !valid {
			writeError(w, http.StatusBadRequest, "bad_request", "Invalid max_playback_quality")
			return
		}
		maxPlaybackQuality = &normalized
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "Profile name is required")
			return
		}
		req.Name = &name
	}
	settingsSync, err := planUpdateProfileSettingsSync(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	input := userstore.UpdateProfileInput{Name: req.Name, Avatar: avatarRef, PIN: req.PIN, IsChild: req.IsChild,
		MaxContentRating: req.MaxContentRating, QualityPreference: req.QualityPreference, Language: req.Language,
		PreferredMetadataLanguage: req.PreferredMetadataLanguage, SubtitleLanguage: req.SubtitleLanguage,
		SubtitleMode: req.SubtitleMode, AutoSkipIntro: req.AutoSkipIntro, AutoSkipCredits: req.AutoSkipCredits,
		AutoSkipRecap: req.AutoSkipRecap, AutoPlayNextPreview: req.AutoPlayNextPreview,
		ShowForcedSubtitles: req.ShowForcedSubtitles, LibraryRestrictionsEnabled: req.LibraryRestrictionsEnabled,
		AllowedLibraryIDs: req.AllowedLibraryIDs, MaxPlaybackQuality: maxPlaybackQuality}
	request, err := h.profileLifecycleRequest(r, "account.profile.update", userID, userSelector, profileID, body)
	if err != nil {
		h.writeAccountResourceLifecycleError(w, err, "Failed to update profile")
		return
	}
	var currentAvatar string
	var changedKeys []string
	result, err := h.lifecycle.Execute(r.Context(), request, func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
		store, err := h.lifecycleProfileStore(ctx, tx, userID)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		current, err := store.GetProfileInTransaction(ctx, tx, profileID)
		if err != nil || current == nil {
			return lifecycleidempotency.Result{}, errLifecycleProfileNotFound
		}
		currentAvatar = current.Avatar
		if req.Name != nil {
			profiles, err := store.ListProfilesInTransaction(ctx, tx)
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			if profileNameConflicts(profiles, *req.Name, profileID) {
				return lifecycleidempotency.Result{}, errLifecycleProfileNameConflict
			}
		}
		writer, err := store.PreferenceSettingsWriterInTransaction(ctx, tx)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		if err := writer.UpdateProfile(ctx, profileID, input); err != nil {
			return lifecycleidempotency.Result{}, err
		}
		changedKeys, err = writeCanonicalSettingsSync(ctx, writer, userstore.SettingIdentity{Scope: settingscontract.ScopeProfile, ProfileID: profileID}, settingsSync)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		updated, err := store.GetProfileInTransaction(ctx, tx, profileID)
		if err != nil || updated == nil {
			return lifecycleidempotency.Result{}, errLifecycleProfileNotFound
		}
		resolutionStore := transactionProfileResolutionStore{store: store, tx: tx}
		prefs := resolveProfilePreferences(ctx, resolutionStore, []string{profileID})
		return lifecycleJSONResult(http.StatusOK, h.adminResourceProfileHandler().profileResponseWith(ctx, *updated, prefs[profileID]))
	})
	if err != nil {
		if errors.Is(err, errLifecycleProfileNameConflict) {
			writeError(w, http.StatusConflict, "name_conflict", "A profile with this name already exists")
		} else if errors.Is(err, errLifecycleProfileNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Profile not found")
		} else {
			h.writeAccountResourceLifecycleError(w, err, "Failed to update profile")
		}
		return
	}
	if !result.Replayed {
		for _, key := range changedKeys {
			publishUserSettingsEvent(r.Context(), h.EventsHub, userID, profileID, key, string(settingscontract.ScopeProfile))
		}
		if currentAvatar != "" && input.Avatar != nil && avatarRefReplacesUpload(currentAvatar, *input.Avatar) {
			if cleanupErr := deleteUploadedAvatarObjects(r.Context(), h.adminResourceProfileHandler().AvatarStore, userID, profileID); cleanupErr != nil {
				slog.WarnContext(r.Context(), "profile avatar cleanup failed after update", "component", "api", "user_id", userID, "profile_id", profileID, "error", cleanupErr)
			}
		}
	}
	writeLifecycleResult(w, result)
}

func (h *AdminHandler) handleLifecycleDeleteUserProfile(w http.ResponseWriter, r *http.Request) {
	userID, userSelector, profileID, ok := parseLifecycleProfileSelectors(w, r)
	if !ok {
		return
	}
	request, err := h.profileLifecycleRequest(r, "account.profile.delete", userID, userSelector, profileID, nil)
	if err != nil {
		h.writeAccountResourceLifecycleError(w, err, "Failed to delete profile")
		return
	}
	var deleted *userstore.Profile
	result, err := h.lifecycle.Execute(r.Context(), request, func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
		store, err := h.lifecycleProfileStore(ctx, tx, userID)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		profile, err := store.GetProfileInTransaction(ctx, tx, profileID)
		if err != nil || profile == nil {
			return lifecycleidempotency.Result{}, errLifecycleProfileNotFound
		}
		if profile.IsPrimary {
			return lifecycleidempotency.Result{}, errLifecyclePrimaryProfile
		}
		if err := store.DeleteProfileInTransaction(ctx, tx, profileID); err != nil {
			return lifecycleidempotency.Result{}, err
		}
		deleted = profile
		return lifecycleidempotency.Result{Status: http.StatusNoContent}, nil
	})
	if err != nil {
		if errors.Is(err, errLifecyclePrimaryProfile) {
			writeError(w, http.StatusConflict, "primary_profile_protected", "The primary profile cannot be deleted. Delete the user account instead.")
		} else if errors.Is(err, errLifecycleProfileNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Profile not found")
		} else {
			h.writeAccountResourceLifecycleError(w, err, "Failed to delete profile")
		}
		return
	}
	if !result.Replayed && deleted != nil {
		profileHandler := h.adminResourceProfileHandler()
		if isUploadedAvatarRef(deleted.Avatar) {
			if cleanupErr := deleteUploadedAvatarObjects(r.Context(), profileHandler.AvatarStore, userID, profileID); cleanupErr != nil {
				slog.WarnContext(r.Context(), "profile avatar cleanup failed after delete", "component", "api", "user_id", userID, "profile_id", profileID, "error", cleanupErr)
			}
		}
		if profileHandler.DeviceLibraryPurger != nil {
			purgeCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
			defer cancel()
			if purgeErr := profileHandler.DeviceLibraryPurger.PurgeProfileDevices(purgeCtx, userID, profileID); purgeErr != nil {
				slog.WarnContext(r.Context(), "profile device-library purge failed after delete", "component", "api", "user_id", userID, "profile_id", profileID, "error", purgeErr)
			}
		}
	}
	writeLifecycleResult(w, result)
}

func (h *AdminHandler) handleLifecycleDeleteUserDevice(w http.ResponseWriter, r *http.Request) {
	userSelector := strings.TrimSpace(chi.URLParam(r, "user_id"))
	userID, err := strconv.Atoi(userSelector)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid user id")
		return
	}
	deviceID := strings.TrimSpace(chi.URLParam(r, "device_id"))
	if deviceID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Device ID is required")
		return
	}
	actorID, actorIncarnation, ok := lifecycleActor(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authenticated account identity is incomplete")
		return
	}
	organizationID := adminResourceOrganization(r.Context())
	selectors := map[string]string{"user_id": userSelector, "device_id": deviceID}
	routeID, targetSource, resolveBase := adminResourceLifecycleScope(r.Context(), "account.device.delete", userID, selectors)
	var profileIDs []string
	request := lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID,
			ActorAccountIncarnationID: &actorIncarnation, Method: r.Method, RouteID: routeID,
			RequestHash:  h.lifecycleDigest(r.Method, routeID, selectors, r.URL.Query(), nil),
			TargetSource: targetSource,
		},
		ResolveTargets: func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
			targets, err := resolveBase(ctx, tx)
			if err != nil {
				return nil, err
			}
			rows, err := tx.Query(ctx, `
				SELECT p.id
				FROM user_profiles p
				WHERE p.user_id=$1
				  AND ($3::uuid IS NULL OR p.organization_id=$3)
				  AND (
					EXISTS(SELECT 1 FROM user_devices d WHERE d.user_id=$1 AND d.profile_id=p.id AND d.device_id=$2)
					OR EXISTS(SELECT 1 FROM user_device_settings d WHERE d.user_id=$1 AND d.profile_id=p.id AND d.device_id=$2)
					OR EXISTS(SELECT 1 FROM user_setting_values v WHERE v.user_id=$1 AND v.profile_id=p.id AND v.device_id=$2)
				  )
				ORDER BY p.id FOR UPDATE`, userID, deviceID, nullableUUID(organizationID))
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			for rows.Next() {
				var profileID string
				if err := rows.Scan(&profileID); err != nil {
					return nil, err
				}
				profileIDs = append(profileIDs, profileID)
				target := targets[0]
				target.ProfileID = profileID
				target.ResourceID = deviceID
				targets = append(targets, target)
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
			if len(profileIDs) == 0 {
				var foreign bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(
					SELECT 1 FROM user_devices WHERE user_id<>$1 AND device_id=$2
					UNION ALL SELECT 1 FROM user_device_settings WHERE user_id<>$1 AND device_id=$2
					UNION ALL SELECT 1 FROM user_setting_values WHERE user_id<>$1 AND device_id=$2
				)`, userID, deviceID).Scan(&foreign); err != nil {
					return nil, err
				}
				if foreign || organizationID != uuid.Nil {
					return nil, errLifecycleDeviceNotFound
				}
			}
			targets[0].ResourceID = deviceID
			return targets, nil
		},
	}
	var changed []userstore.SettingIdentity
	result, err := h.lifecycle.Execute(r.Context(), request, func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
		store, err := h.lifecycleProfileStore(ctx, tx, userID)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		if len(profileIDs) > 0 {
			changed, err = store.DeleteDeviceInTransaction(ctx, tx, profileIDs, deviceID)
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
		}
		return lifecycleidempotency.Result{Status: http.StatusNoContent}, nil
	})
	if err != nil {
		if errors.Is(err, errLifecycleDeviceNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Device not found")
		} else {
			h.writeAccountResourceLifecycleError(w, err, "Failed to remove device")
		}
		return
	}
	if !result.Replayed {
		for _, identity := range changed {
			publishUserSettingsEvent(r.Context(), h.EventsHub, userID, identity.ProfileID, identity.Key, string(identity.Scope))
		}
	}
	writeLifecycleResult(w, result)
}

func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

var (
	errLifecycleProfileLimit        = errors.New("profile limit reached")
	errLifecycleProfileNameConflict = errors.New("profile name conflict")
	errLifecycleProfileNotFound     = errors.New("profile not found")
	errLifecyclePrimaryProfile      = errors.New("primary profile protected")
	errLifecycleDeviceNotFound      = errors.New("device not found")
)

func (h *AdminHandler) writeAccountResourceLifecycleError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, tenancy.ErrMembershipPolicyWriteUnavailable):
		apiresponse.WriteRetryableUnavailable(w, "membership_policy_rollout_pending", "Membership policy rollout is not ready for this mutation", 1)
	case errors.Is(err, lifecycleidempotency.ErrKeyRequired):
		writeError(w, http.StatusPreconditionRequired, "idempotency_key_required", "Idempotency-Key is required for this lifecycle mutation")
	case errors.Is(err, lifecycleidempotency.ErrKeyMalformed):
		writeError(w, http.StatusBadRequest, "idempotency_key_invalid", "Idempotency-Key must be a bounded opaque ASCII value")
	case errors.Is(err, lifecycleidempotency.ErrConflict):
		writeError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key conflicts with its original lifecycle request")
	case errors.Is(err, lifecycleidempotency.ErrTargetNotFound), auth.IsNotFound(err):
		writeError(w, http.StatusNotFound, "not_found", "User not found")
	case errors.Is(err, lifecycleidempotency.ErrPending):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "lifecycle_request_pending", "Lifecycle request completion is pending")
	case errors.Is(err, lifecycleidempotency.ErrInvalidBinding):
		writeError(w, http.StatusUnauthorized, "unauthorized", "Lifecycle request identity is no longer valid")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", fallback)
	}
}
