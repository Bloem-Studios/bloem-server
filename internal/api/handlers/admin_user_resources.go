package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/sessioninvalidation"
	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type adminResourceOrganizationKey struct{}

func withAdminResourceOrganization(ctx context.Context, organizationID uuid.UUID) context.Context {
	return context.WithValue(ctx, adminResourceOrganizationKey{}, organizationID)
}

func adminResourceOrganization(ctx context.Context) uuid.UUID {
	organizationID, _ := ctx.Value(adminResourceOrganizationKey{}).(uuid.UUID)
	return organizationID
}

func profileInAdminResourceOrganization(ctx context.Context, profile *userstore.Profile) bool {
	if profile == nil {
		return false
	}
	organizationID := adminResourceOrganization(ctx)
	return organizationID == uuid.Nil || profile.OrganizationID == organizationID.String()
}

func adminResourceOrganizationProfiles(ctx context.Context, store userstore.UserStore) (map[string]userstore.Profile, error) {
	profiles, err := store.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	organizationID := adminResourceOrganization(ctx)
	out := make(map[string]userstore.Profile, len(profiles))
	for _, profile := range profiles {
		if organizationID == uuid.Nil || profile.OrganizationID == organizationID.String() {
			out[profile.ID] = profile
		}
	}
	return out, nil
}

type adminUserSessionRepository interface {
	GetByID(ctx context.Context, id string) (*models.AuthSession, error)
	ListByUser(ctx context.Context, userID int) ([]*models.AuthSession, error)
	RevokeByUserAndSession(ctx context.Context, userID int, sessionID string) error
	RevokeAllByUser(ctx context.Context, userID int) error
	RevokeAllByUserAndProfiles(ctx context.Context, userID int, profileIDs []string) error
	RevokeAllByImpersonator(ctx context.Context, impersonatorUserID int) error
}

type adminUserResources struct {
	user  *models.User
	store userstore.UserStore
}

type adminDeviceSettingChange struct {
	profileID string
	key       string
}

type adminUserAuthSessionResponse struct {
	ID                     string     `json:"id"`
	DeviceName             string     `json:"device_name,omitempty"`
	DeviceID               string     `json:"device_id,omitempty"`
	IPAddress              string     `json:"ip_address,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	ExpiresAt              time.Time  `json:"expires_at"`
	RevokedAt              *time.Time `json:"revoked_at,omitempty"`
	ProfileID              *string    `json:"profile_id,omitempty"`
	AuthMethod             string     `json:"auth_method,omitempty"`
	IsImpersonationSession bool       `json:"is_impersonation_session"`
}

func (h *AdminHandler) loadAdminUserResources(w http.ResponseWriter, r *http.Request) (adminUserResources, bool) {
	rawUserID := strings.TrimSpace(chi.URLParam(r, "user_id"))
	userID, err := strconv.Atoi(rawUserID)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid user id")
		return adminUserResources{}, false
	}
	if h.userRepo == nil || h.storeProv == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "User resources are not configured")
		return adminUserResources{}, false
	}
	user, err := h.userRepo.GetByID(r.Context(), userID)
	if err != nil {
		if auth.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "not_found", "User not found")
			return adminUserResources{}, false
		}
		slog.ErrorContext(r.Context(), "admin user resource lookup failed",
			"component", "api", "user_id", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load user")
		return adminUserResources{}, false
	}
	if user == nil {
		writeError(w, http.StatusNotFound, "not_found", "User not found")
		return adminUserResources{}, false
	}
	store, err := h.storeProv.ForUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access user store")
		return adminUserResources{}, false
	}
	if store == nil {
		writeError(w, http.StatusNotFound, "not_found", "User store not found")
		return adminUserResources{}, false
	}
	return adminUserResources{user: user, store: store}, true
}

func (h *AdminHandler) adminResourceProfileHandler() *ProfileHandler {
	if h.profileHandler != nil {
		return h.profileHandler
	}
	profileHandler := NewProfileHandler(h.storeProv)
	profileHandler.UserRepo = h.userRepo
	profileHandler.AccessGroups = h.AccessGroups
	profileHandler.EventsHub = h.EventsHub
	return profileHandler
}

// HandleListUserProfiles handles GET /admin/users/{user_id}/profiles.
func (h *AdminHandler) HandleListUserProfiles(w http.ResponseWriter, r *http.Request) {
	resources, ok := h.loadAdminUserResources(w, r)
	if !ok {
		return
	}
	profiles, err := resources.store.ListProfiles(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list profiles")
		return
	}
	profiles = profilesForOrganization(r.Context(), profiles)
	if organizationID := adminResourceOrganization(r.Context()); organizationID != uuid.Nil {
		filtered := profiles[:0]
		for _, profile := range profiles {
			if profile.OrganizationID == organizationID.String() {
				filtered = append(filtered, profile)
			}
		}
		profiles = filtered
	}
	writeJSON(w, http.StatusOK, h.adminResourceProfileHandler().toProfileResponses(r.Context(), resources.store, profiles))
}

// HandleCreateUserProfile handles POST /admin/users/{user_id}/profiles.
func (h *AdminHandler) HandleCreateUserProfile(w http.ResponseWriter, r *http.Request) {
	resources, ok := h.loadAdminUserResources(w, r)
	if !ok {
		return
	}
	var req createProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	profiles, err := resources.store.ListProfiles(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list profiles")
		return
	}
	profileHandler := h.adminResourceProfileHandler()
	limit, inheritedGroupID, err := profileHandler.effectiveProfileLimit(r.Context(), resources.user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to resolve profile limit")
		return
	}
	if limit >= 1 && len(profiles) >= limit {
		writeError(w, http.StatusUnprocessableEntity, "profile_limit_reached",
			fmt.Sprintf("This account has reached its profile limit (%d)", limit))
		return
	}
	if profileNameConflicts(profiles, name, "") {
		writeError(w, http.StatusConflict, "name_conflict", "A profile with this name already exists")
		return
	}

	showForcedSubtitles := true
	if req.ShowForcedSubtitles != nil {
		showForcedSubtitles = *req.ShowForcedSubtitles
	}
	profile := userstore.Profile{
		ID:                         uuid.NewString(),
		Name:                       name,
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
		AccessGroupID:              inheritedGroupID,
	}
	if organizationID := adminResourceOrganization(r.Context()); organizationID != uuid.Nil {
		profile.OrganizationID = organizationID.String()
	}
	if err := profileHandler.createProfileWithSettingsSync(
		r.Context(), resources.store, resources.user.ID, profile, settingsSync,
	); err != nil {
		if isProfileEntitlementLimitError(err) {
			writeError(w, http.StatusUnprocessableEntity, "profile_limit_reached", "This account has reached its profile limit")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create profile")
		return
	}
	if req.PIN != "" {
		if err := resources.store.UpdateProfile(r.Context(), profile.ID, userstore.UpdateProfileInput{PIN: &req.PIN}); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to set profile PIN")
			return
		}
	}
	if req.ShowForcedSubtitles != nil && !*req.ShowForcedSubtitles {
		if err := resources.store.UpdateProfile(r.Context(), profile.ID, userstore.UpdateProfileInput{
			ShowForcedSubtitles: req.ShowForcedSubtitles,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to set forced subtitle preference")
			return
		}
	}
	created, err := resources.store.GetProfile(r.Context(), profile.ID)
	if err != nil || created == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve created profile")
		return
	}
	writeJSON(w, http.StatusCreated,
		profileHandler.toProfileResponse(r.Context(), resources.store, *created))
}

// HandleUpdateUserProfile handles PUT /admin/users/{user_id}/profiles/{profile_id}.
func (h *AdminHandler) HandleUpdateUserProfile(w http.ResponseWriter, r *http.Request) {
	resources, ok := h.loadAdminUserResources(w, r)
	if !ok {
		return
	}
	profileID := strings.TrimSpace(chi.URLParam(r, "profile_id"))
	if profileID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Profile ID is required")
		return
	}
	current, err := resources.store.GetProfile(r.Context(), profileID)
	if err != nil || !profileInAdminResourceOrganization(r.Context(), current) {
		writeError(w, http.StatusNotFound, "not_found", "Profile not found")
		return
	}
	var req updateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
		normalized, ok := access.ParsePlaybackQualityPreset(*req.MaxPlaybackQuality)
		if !ok {
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
		profiles, err := resources.store.ListProfiles(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list profiles")
			return
		}
		if profileNameConflicts(profiles, name, profileID) {
			writeError(w, http.StatusConflict, "name_conflict", "A profile with this name already exists")
			return
		}
	}
	settingsSync, err := planUpdateProfileSettingsSync(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	input := userstore.UpdateProfileInput{
		Name:                       req.Name,
		Avatar:                     avatarRef,
		PIN:                        req.PIN,
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
		ShowForcedSubtitles:        req.ShowForcedSubtitles,
		LibraryRestrictionsEnabled: req.LibraryRestrictionsEnabled,
		AllowedLibraryIDs:          req.AllowedLibraryIDs,
		MaxPlaybackQuality:         maxPlaybackQuality,
	}
	profileHandler := h.adminResourceProfileHandler()
	if err := profileHandler.updateProfileWithLifecycle(
		r.Context(), resources.store, resources.user.ID, current, input, settingsSync,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to update profile")
		return
	}
	updated, err := resources.store.GetProfile(r.Context(), profileID)
	if err != nil || updated == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve updated profile")
		return
	}
	writeJSON(w, http.StatusOK,
		profileHandler.toProfileResponse(r.Context(), resources.store, *updated))
}

// HandleDeleteUserProfile handles DELETE /admin/users/{user_id}/profiles/{profile_id}.
func (h *AdminHandler) HandleDeleteUserProfile(w http.ResponseWriter, r *http.Request) {
	resources, ok := h.loadAdminUserResources(w, r)
	if !ok {
		return
	}
	profileID := strings.TrimSpace(chi.URLParam(r, "profile_id"))
	if profileID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Profile ID is required")
		return
	}
	profile, err := resources.store.GetProfile(r.Context(), profileID)
	if err != nil || !profileInAdminResourceOrganization(r.Context(), profile) {
		writeError(w, http.StatusNotFound, "not_found", "Profile not found")
		return
	}
	if profile.IsPrimary {
		writeError(w, http.StatusConflict, "primary_profile_protected",
			"The primary profile cannot be deleted. Delete the user account instead.")
		return
	}
	if err := h.adminResourceProfileHandler().deleteProfileWithLifecycle(
		r.Context(), resources.store, resources.user.ID, profile,
	); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Profile not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleListUserDevices handles GET /admin/users/{user_id}/devices.
func (h *AdminHandler) HandleListUserDevices(w http.ResponseWriter, r *http.Request) {
	resources, ok := h.loadAdminUserResources(w, r)
	if !ok {
		return
	}
	registry, ok := resources.store.(userstore.DeviceRegistry)
	if !ok {
		writeJSON(w, http.StatusOK, []deviceResponse{})
		return
	}
	devices, err := registry.ListDevices(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list devices")
		return
	}
	counts, err := deviceOverrideCounts(r, resources.store)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to read device settings")
		return
	}
	profileNames, err := listProfileNamesByID(r.Context(), resources.store)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list profiles")
		return
	}
	response := make([]deviceResponse, 0, len(devices))
	organizationProfiles, err := adminResourceOrganizationProfiles(r.Context(), resources.store)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list profiles")
		return
	}
	for _, device := range devices {
		if adminResourceOrganization(r.Context()) != uuid.Nil {
			if _, ok := organizationProfiles[device.ProfileID]; !ok {
				continue
			}
		}
		response = append(response, deviceResponse{
			DeviceID:       device.DeviceID,
			DeviceName:     device.DeviceName,
			DevicePlatform: device.DevicePlatform,
			LastSeenAt:     device.LastSeenAt,
			ProfileID:      device.ProfileID,
			ProfileName:    profileNames[device.ProfileID],
			ChangedCount:   counts[deviceKey{profileID: device.ProfileID, deviceID: device.DeviceID}],
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func adminDeviceProfileIDs(ctx context.Context, store userstore.UserStore, deviceID string) ([]string, []adminDeviceSettingChange, error) {
	profileIDs := make(map[string]struct{})
	keysByProfile := make(map[string][]string)
	if registry, ok := store.(userstore.DeviceRegistry); ok {
		devices, err := registry.ListDevices(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, device := range devices {
			if device.DeviceID == deviceID {
				profileIDs[device.ProfileID] = struct{}{}
			}
		}
	}
	legacy, err := store.ListAllDeviceSettings(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, entry := range legacy {
		if entry.DeviceID == deviceID {
			profileIDs[entry.ProfileID] = struct{}{}
		}
	}
	values, err := store.ListAllSettingValues(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, value := range values {
		if value.Scope == settingscontract.ScopeProfileDevice && value.DeviceID == deviceID {
			profileIDs[value.ProfileID] = struct{}{}
			keysByProfile[value.ProfileID] = append(keysByProfile[value.ProfileID], value.Key)
		}
	}
	ids := make([]string, 0, len(profileIDs))
	for profileID := range profileIDs {
		ids = append(ids, profileID)
	}
	sort.Strings(ids)
	changes := make([]adminDeviceSettingChange, 0)
	for _, profileID := range ids {
		for _, key := range keysByProfile[profileID] {
			changes = append(changes, adminDeviceSettingChange{profileID: profileID, key: key})
		}
	}
	return ids, changes, nil
}

func (h *AdminHandler) deviceBelongsToAnotherUser(ctx context.Context, selectedUserID int, deviceID string) (bool, error) {
	users, err := h.userRepo.List(ctx)
	if err != nil {
		return false, err
	}
	for _, user := range users {
		if user == nil || user.ID == selectedUserID {
			continue
		}
		store, err := h.storeProv.ForUser(ctx, user.ID)
		if err != nil {
			return false, err
		}
		if store == nil {
			continue
		}
		profileIDs, _, err := adminDeviceProfileIDs(ctx, store, deviceID)
		if err != nil {
			return false, err
		}
		if len(profileIDs) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// HandleDeleteUserDevice handles DELETE /admin/users/{user_id}/devices/{device_id}.
func (h *AdminHandler) HandleDeleteUserDevice(w http.ResponseWriter, r *http.Request) {
	resources, ok := h.loadAdminUserResources(w, r)
	if !ok {
		return
	}
	deviceID := strings.TrimSpace(chi.URLParam(r, "device_id"))
	if deviceID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Device ID is required")
		return
	}
	profileIDs, changedKeys, err := adminDeviceProfileIDs(r.Context(), resources.store, deviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to look up device")
		return
	}
	if len(profileIDs) == 0 {
		foreign, err := h.deviceBelongsToAnotherUser(r.Context(), resources.user.ID, deviceID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to look up device")
			return
		}
		if foreign {
			writeError(w, http.StatusNotFound, "not_found", "Device not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if organizationID := adminResourceOrganization(r.Context()); organizationID != uuid.Nil {
		organizationProfiles, err := adminResourceOrganizationProfiles(r.Context(), resources.store)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to look up device")
			return
		}
		filtered := profileIDs[:0]
		allowedChanges := changedKeys[:0]
		for _, profileID := range profileIDs {
			if _, ok := organizationProfiles[profileID]; ok {
				filtered = append(filtered, profileID)
			}
		}
		for _, changed := range changedKeys {
			if _, ok := organizationProfiles[changed.profileID]; ok {
				allowedChanges = append(allowedChanges, changed)
			}
		}
		profileIDs, changedKeys = filtered, allowedChanges
		if len(profileIDs) == 0 {
			writeError(w, http.StatusNotFound, "not_found", "Device not found")
			return
		}
	}
	registry, _ := resources.store.(userstore.DeviceRegistry)
	for _, profileID := range profileIDs {
		if _, err := resources.store.DeleteSettingValuesForDevice(r.Context(), profileID, deviceID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to remove device")
			return
		}
		if err := resources.store.DeleteAllDeviceSettings(r.Context(), profileID, deviceID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to remove device")
			return
		}
		if registry != nil {
			if err := registry.ForgetDevice(r.Context(), profileID, deviceID); err != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to remove device")
				return
			}
		}
	}
	for _, changed := range changedKeys {
		publishUserSettingsEvent(r.Context(), h.EventsHub, resources.user.ID,
			changed.profileID, changed.key, string(settingscontract.ScopeProfileDevice))
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleListUserAuthSessions handles GET /admin/users/{user_id}/auth-sessions.
func (h *AdminHandler) HandleListUserAuthSessions(w http.ResponseWriter, r *http.Request) {
	resources, ok := h.loadAdminUserResources(w, r)
	if !ok {
		return
	}
	if h.sessionRepo == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Authentication sessions are not configured")
		return
	}
	sessions, err := h.sessionRepo.ListByUser(r.Context(), resources.user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list authentication sessions")
		return
	}
	response := make([]adminUserAuthSessionResponse, 0, len(sessions))
	organizationProfiles, err := adminResourceOrganizationProfiles(r.Context(), resources.store)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list authentication sessions")
		return
	}
	for _, session := range sessions {
		if session == nil || session.UserID != resources.user.ID {
			continue
		}
		if adminResourceOrganization(r.Context()) != uuid.Nil {
			if session.ProfileID == nil {
				continue
			}
			if _, ok := organizationProfiles[*session.ProfileID]; !ok {
				continue
			}
		}
		response = append(response, adminUserAuthSessionResponse{
			ID:                     session.ID,
			DeviceName:             session.DeviceName,
			DeviceID:               session.DeviceID,
			IPAddress:              session.IPAddress,
			CreatedAt:              session.CreatedAt,
			ExpiresAt:              session.ExpiresAt,
			RevokedAt:              session.RevokedAt,
			ProfileID:              session.ProfileID,
			AuthMethod:             session.AuthMethod,
			IsImpersonationSession: session.ImpersonatorUserID != nil,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

// HandleRevokeUserAuthSession handles DELETE
// /admin/users/{user_id}/auth-sessions/{session_id}.
func (h *AdminHandler) HandleRevokeUserAuthSession(w http.ResponseWriter, r *http.Request) {
	resources, ok := h.loadAdminUserResources(w, r)
	if !ok {
		return
	}
	if h.sessionRepo == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Authentication sessions are not configured")
		return
	}
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Session ID is required")
		return
	}
	session, err := h.sessionRepo.GetByID(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, auth.ErrSessionNotFound) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to look up authentication session")
		return
	}
	if session == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if session.UserID != resources.user.ID {
		writeError(w, http.StatusNotFound, "not_found", "Authentication session not found")
		return
	}
	organizationID := adminResourceOrganization(r.Context())
	var scopedProfileID string
	if organizationID != uuid.Nil {
		organizationProfiles, scopeErr := adminResourceOrganizationProfiles(r.Context(), resources.store)
		if scopeErr != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to look up authentication session")
			return
		}
		if session.ProfileID == nil {
			writeError(w, http.StatusNotFound, "not_found", "Authentication session not found")
			return
		}
		if _, ok := organizationProfiles[*session.ProfileID]; !ok {
			writeError(w, http.StatusNotFound, "not_found", "Authentication session not found")
			return
		}
		scopedProfileID = *session.ProfileID
	}
	if err := h.sessionRepo.RevokeByUserAndSession(r.Context(), resources.user.ID, sessionID); err != nil {
		if errors.Is(err, auth.ErrSessionNotFound) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to revoke authentication session")
		return
	}
	if organizationID != uuid.Nil {
		if h.OnUserProfileSessionsRevoked != nil {
			if err := sessioninvalidation.Run(r.Context(), func(callbackCtx context.Context) error {
				return h.OnUserProfileSessionsRevoked(callbackCtx, resources.user.ID, []string{scopedProfileID})
			}); err != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "Authentication session was revoked but compatibility-session invalidation failed")
				return
			}
		}
	} else if h.OnUserSessionsRevoked != nil {
		if err := sessioninvalidation.Run(r.Context(), func(callbackCtx context.Context) error {
			return h.OnUserSessionsRevoked(callbackCtx, resources.user.ID)
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Authentication session was revoked but compatibility-session invalidation failed")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleRevokeAllUserAuthSessions handles DELETE
// /admin/users/{user_id}/auth-sessions.
func (h *AdminHandler) HandleRevokeAllUserAuthSessions(w http.ResponseWriter, r *http.Request) {
	resources, ok := h.loadAdminUserResources(w, r)
	if !ok {
		return
	}
	if h.sessionRepo == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Authentication sessions are not configured")
		return
	}
	organizationID := adminResourceOrganization(r.Context())
	var scopedProfileIDs []string
	if organizationID == uuid.Nil {
		if err := h.sessionRepo.RevokeAllByUser(r.Context(), resources.user.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to revoke authentication sessions")
			return
		}
	} else {
		organizationProfiles, err := adminResourceOrganizationProfiles(r.Context(), resources.store)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to revoke authentication sessions")
			return
		}
		scopedProfileIDs = make([]string, 0, len(organizationProfiles))
		for profileID := range organizationProfiles {
			scopedProfileIDs = append(scopedProfileIDs, profileID)
		}
		nativeRevoked := false
		if err := sessioninvalidation.Run(r.Context(), func(invalidationCtx context.Context) error {
			if err := h.sessionRepo.RevokeAllByUserAndProfiles(invalidationCtx, resources.user.ID, scopedProfileIDs); err != nil {
				return err
			}
			nativeRevoked = true
			if h.OnUserProfileSessionsRevoked != nil {
				return h.OnUserProfileSessionsRevoked(invalidationCtx, resources.user.ID, scopedProfileIDs)
			}
			return nil
		}); err != nil {
			if !nativeRevoked {
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to revoke authentication sessions")
			} else {
				writeError(w, http.StatusInternalServerError, "internal_error", "Authentication sessions were revoked but compatibility-session invalidation failed")
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if h.OnUserSessionsRevoked != nil {
		if err := sessioninvalidation.Run(r.Context(), func(callbackCtx context.Context) error {
			return h.OnUserSessionsRevoked(callbackCtx, resources.user.ID)
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Authentication sessions were revoked but compatibility-session invalidation failed")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
