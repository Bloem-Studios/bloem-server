package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

var errLifecyclePrimaryProfileProtected = errors.New("primary profile is protected")
var (
	errLifecycleUnavailable             = errors.New("profile lifecycle idempotency unavailable")
	errLifecycleManagementForbidden     = errors.New("profile management forbidden")
	errLifecycleBootstrapAccessSettings = errors.New("bootstrap profile access settings forbidden")
	errLifecycleDirectProfilePIN        = errors.New("direct profile session cannot change profile PIN")
	errLifecycleSelfServiceTarget       = errors.New("self-service profile target forbidden")
	errLifecycleSelfServiceAccess       = errors.New("self-service profile access settings forbidden")
	errLifecycleNameConflict            = errors.New("profile name conflict")
	errLifecycleHouseholdProfileLimit   = errors.New("profile limit reached")
)

func (h *ProfileHandler) handleLifecycleProfileCreate(w http.ResponseWriter, r *http.Request, userID int, profile userstore.Profile, req createProfileRequest, writes []profileSettingSync, body []byte) {
	request, ok := h.profileLifecycleRequest(w, r, "profile.create", nil, body)
	if !ok {
		return
	}
	result, err := h.createProfileLifecycle(r.Context(), ProfileCreateCommand{UserID: userID, Lifecycle: &request, ActiveProfileID: apimw.ActiveProfileID(r), VerifyProfile: func(id string) error { return verifyProfileToken(r, h.userLookupOrNil(), h.ProfileTokens, id) }, Request: req}, profile, writes)
	if err != nil {
		h.writeProfileLifecycleError(w, err)
		return
	}
	writeLifecycleResult(w, result)
}

func (h *ProfileHandler) createProfileLifecycle(ctx context.Context, cmd ProfileCreateCommand, profile userstore.Profile, writes []profileSettingSync) (lifecycleidempotency.Result, error) {
	userID := cmd.UserID
	req := cmd.Request
	if cmd.Lifecycle == nil {
		return lifecycleidempotency.Result{}, lifecycleidempotency.ErrKeyRequired
	}
	request := *cmd.Lifecycle
	var changedKeys []string
	result, err := h.lifecycle.ExecuteCreate(ctx, request,
		func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, lifecycleidempotency.Result, error) {
			store, err := h.storeProvider.ForUser(ctx, userID)
			if err != nil {
				return nil, lifecycleidempotency.Result{}, err
			}
			txStore, ok := store.(userstore.ProfileLifecycleTransactioner)
			if !ok {
				return nil, lifecycleidempotency.Result{}, errLifecycleUnavailable
			}
			existingProfiles, err := store.ListProfiles(ctx)
			if err != nil {
				return nil, lifecycleidempotency.Result{}, err
			}
			existingProfiles = profilesForOrganization(ctx, existingProfiles)
			isBootstrap := len(existingProfiles) == 0

			if !isBootstrap {
				allowed, err := canManageHouseholdAs(ctx, store, cmd.ActiveProfileID, cmd.VerifyProfile)
				if err != nil {
					return nil, lifecycleidempotency.Result{}, err
				}
				if !allowed {
					return nil, lifecycleidempotency.Result{}, errLifecycleManagementForbidden
				}
			}
			if isBootstrap && !apimw.IsAdmin(ctx) &&
				(req.IsChild || req.MaxContentRating != "" || req.LibraryRestrictionsEnabled || len(req.AllowedLibraryIDs) > 0 || req.MaxPlaybackQuality != "") {
				return nil, lifecycleidempotency.Result{}, errLifecycleBootstrapAccessSettings
			}
			if h.UserRepo != nil {
				user, err := h.UserRepo.GetByID(ctx, userID)
				if err != nil {
					return nil, lifecycleidempotency.Result{}, err
				}
				limit, inheritedGroupID, err := h.effectiveProfileLimit(ctx, user)
				if err != nil {
					return nil, lifecycleidempotency.Result{}, err
				}
				if limit >= 1 && len(existingProfiles) >= limit {
					return nil, lifecycleidempotency.Result{}, errLifecycleHouseholdProfileLimit
				}
				profile.AccessGroupID = inheritedGroupID
			}
			if profileNameConflicts(existingProfiles, req.Name, "") {
				return nil, lifecycleidempotency.Result{}, errLifecycleNameConflict
			}
			var created *userstore.Profile
			var responseWrites []profileSettingSync
			err = txStore.WithProfileLifecycleTransaction(ctx, tx, func(writer userstore.ProfileLifecycleWriter) error {
				if err := writer.CreateProfile(ctx, profile); err != nil {
					return err
				}
				inherited, err := planInheritedLegacyUserSettings(ctx, writer)
				if err != nil {
					return err
				}
				responseWrites = append(append([]profileSettingSync(nil), writes...), inherited...)
				changedKeys, err = writeCanonicalSettingsSync(ctx, writer, userstore.SettingIdentity{
					Scope: settingscontract.ScopeProfile, ProfileID: profile.ID,
				}, responseWrites)
				if err != nil {
					return err
				}
				if req.PIN != "" {
					if err := writer.UpdateProfile(ctx, profile.ID, userstore.UpdateProfileInput{PIN: &req.PIN}); err != nil {
						return err
					}
				}
				if req.ShowForcedSubtitles != nil && !*req.ShowForcedSubtitles {
					if err := writer.UpdateProfile(ctx, profile.ID, userstore.UpdateProfileInput{ShowForcedSubtitles: req.ShowForcedSubtitles}); err != nil {
						return err
					}
				}
				created, err = writer.GetProfile(ctx, profile.ID)
				return err
			})
			if err != nil {
				return nil, lifecycleidempotency.Result{}, err
			}
			if created == nil {
				return nil, lifecycleidempotency.Result{}, lifecycleidempotency.ErrTargetNotFound
			}
			target, err := lifecycleidempotency.ResolveProfileTarget(ctx, tx, userID, profile.ID)
			if err != nil {
				return nil, lifecycleidempotency.Result{}, err
			}
			createdResult, err := h.profileLifecycleJSONResult(ctx, *created, responseWrites)
			createdResult.Status = http.StatusCreated
			return []lifecycleidempotency.TargetBinding{target}, createdResult, err
		})
	if err != nil {
		return lifecycleidempotency.Result{}, err
	}
	if !result.Replayed {
		h.publishProfileSettingKeys(ctx, userID, profile.ID, changedKeys)
	}
	return result, nil
}

func (h *ProfileHandler) handleLifecycleProfileUpdate(w http.ResponseWriter, r *http.Request, userID int, profileID string, req updateProfileRequest, input userstore.UpdateProfileInput, writes []profileSettingSync, body []byte) {
	request, ok := h.profileLifecycleRequest(w, r, "profile.update", map[string]string{"id": profileID}, body)
	if !ok {
		return
	}
	result, err := h.updateProfileLifecycle(r.Context(), ProfileUpdateCommand{UserID: userID, Lifecycle: &request, ActiveProfileID: apimw.ActiveProfileID(r), VerifyProfile: func(id string) error { return verifyProfileToken(r, h.userLookupOrNil(), h.ProfileTokens, id) }, ProfileID: profileID, Request: req}, input, writes)
	if err != nil {
		h.writeProfileLifecycleError(w, err)
		return
	}
	writeLifecycleResult(w, result)
}

func (h *ProfileHandler) updateProfileLifecycle(ctx context.Context, cmd ProfileUpdateCommand, input userstore.UpdateProfileInput, writes []profileSettingSync) (lifecycleidempotency.Result, error) {
	userID := cmd.UserID
	profileID := cmd.ProfileID
	req := cmd.Request
	if cmd.Lifecycle == nil {
		return lifecycleidempotency.Result{}, lifecycleidempotency.ErrKeyRequired
	}
	request := *cmd.Lifecycle
	request.ResolveTargets = profileTargetResolver(userID, profileID)
	var changedKeys []string
	var committedOriginal *userstore.Profile
	result, err := h.lifecycle.Execute(ctx, request,
		func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
			store, err := h.storeProvider.ForUser(ctx, userID)
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			txStore, ok := store.(userstore.ProfileLifecycleTransactioner)
			if !ok {
				return lifecycleidempotency.Result{}, errLifecycleUnavailable
			}

			if req.PIN != nil && apimw.IsDirectProfileSession((&http.Request{}).WithContext(ctx)) {
				return lifecycleidempotency.Result{}, errLifecycleDirectProfilePIN
			}
			canManage, err := canManageHouseholdAs(ctx, store, cmd.ActiveProfileID, cmd.VerifyProfile)
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			if !canManage {
				activeProfileID := cmd.ActiveProfileID
				if activeProfileID == "" || activeProfileID != profileID {
					return lifecycleidempotency.Result{}, errLifecycleSelfServiceTarget
				}
				if !isAllowedSelfServiceProfileUpdate(req) {
					return lifecycleidempotency.Result{}, errLifecycleSelfServiceAccess
				}
			}
			if req.Name != nil {
				existingProfiles, err := store.ListProfiles(ctx)
				if err != nil {
					return lifecycleidempotency.Result{}, err
				}
				if profileNameConflicts(existingProfiles, *req.Name, profileID) {
					return lifecycleidempotency.Result{}, errLifecycleNameConflict
				}
			}
			var updated *userstore.Profile
			err = txStore.WithProfileLifecycleTransaction(ctx, tx, func(writer userstore.ProfileLifecycleWriter) error {
				var err error
				committedOriginal, err = writer.GetProfile(ctx, profileID)
				if err != nil {
					return err
				}
				if committedOriginal == nil {
					return lifecycleidempotency.ErrTargetNotFound
				}
				if err := writer.UpdateProfile(ctx, profileID, input); err != nil {
					return err
				}
				changedKeys, err = writeCanonicalSettingsSync(ctx, writer, userstore.SettingIdentity{
					Scope: settingscontract.ScopeProfile, ProfileID: profileID,
				}, writes)
				if err != nil {
					return err
				}
				updated, err = writer.GetProfile(ctx, profileID)
				return err
			})
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			return h.profileLifecycleJSONResult(ctx, *updated, writes)
		})
	if err != nil {
		return lifecycleidempotency.Result{}, err
	}
	if !result.Replayed {
		h.publishProfileSettingKeys(ctx, userID, profileID, changedKeys)
		h.completeProfileUpdateAfterCommit(ctx, userID, committedOriginal, input)
	}
	return result, nil
}

func (h *ProfileHandler) handleLifecycleProfileDelete(w http.ResponseWriter, r *http.Request, userID int, profileID string) {
	request, ok := h.profileLifecycleRequest(w, r, "profile.delete", map[string]string{"id": profileID}, nil)
	if !ok {
		return
	}
	result, err := h.deleteProfileLifecycle(r.Context(), ProfileDeleteCommand{UserID: userID, Lifecycle: &request, ActiveProfileID: apimw.ActiveProfileID(r), VerifyProfile: func(id string) error { return verifyProfileToken(r, h.userLookupOrNil(), h.ProfileTokens, id) }, ProfileID: profileID})
	if err != nil {
		h.writeProfileLifecycleError(w, err)
		return
	}
	writeLifecycleResult(w, result)
}

func (h *ProfileHandler) deleteProfileLifecycle(ctx context.Context, cmd ProfileDeleteCommand) (lifecycleidempotency.Result, error) {
	userID := cmd.UserID
	profileID := cmd.ProfileID
	if cmd.Lifecycle == nil {
		return lifecycleidempotency.Result{}, lifecycleidempotency.ErrKeyRequired
	}
	request := *cmd.Lifecycle
	request.ResolveTargets = profileTargetResolver(userID, profileID)
	var deleted *userstore.Profile
	result, err := h.lifecycle.Execute(ctx, request,
		func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
			store, err := h.storeProvider.ForUser(ctx, userID)
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			txStore, ok := store.(userstore.ProfileLifecycleTransactioner)
			if !ok {
				return lifecycleidempotency.Result{}, errLifecycleUnavailable
			}
			allowed, err := canManageHouseholdAs(ctx, store, cmd.ActiveProfileID, cmd.VerifyProfile)
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			if !allowed {
				return lifecycleidempotency.Result{}, errLifecycleManagementForbidden
			}
			err = txStore.WithProfileLifecycleTransaction(ctx, tx, func(writer userstore.ProfileLifecycleWriter) error {
				var err error
				deleted, err = writer.GetProfile(ctx, profileID)
				if err != nil {
					return err
				}
				if deleted == nil {
					return lifecycleidempotency.ErrTargetNotFound
				}
				if deleted.IsPrimary {
					return errLifecyclePrimaryProfileProtected
				}
				return writer.DeleteProfile(ctx, profileID)
			})
			return lifecycleidempotency.Result{Status: http.StatusNoContent}, err
		})
	if err != nil {
		return lifecycleidempotency.Result{}, err
	}
	if !result.Replayed {
		h.completeProfileDeleteAfterCommit(ctx, userID, deleted)
	}
	return result, nil
}

func (h *ProfileHandler) profileLifecycleRequest(w http.ResponseWriter, r *http.Request, routeID string, selectors map[string]string, body []byte) (lifecycleidempotency.Request, bool) {
	claims := apimw.GetClaims(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return lifecycleidempotency.Request{}, false
	}
	incarnation, err := uuid.Parse(claims.AccountIncarnationID)
	if err != nil || incarnation == uuid.Nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authenticated account identity is incomplete")
		return lifecycleidempotency.Request{}, false
	}
	actorID := claims.UserID
	return lifecycleidempotency.Request{IdempotencyKey: r.Header.Get("Idempotency-Key"), Binding: lifecycleidempotency.Binding{
		ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID, ActorAccountIncarnationID: &incarnation,
		Method: r.Method, RouteID: routeID, RequestHash: h.digest(r.Method, routeID, selectors, r.URL.Query(), body),
		TargetSource: lifecycleidempotency.TargetExactMembership,
	}}, true
}

func profileTargetResolver(userID int, profileID string) func(context.Context, pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
	return func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
		target, err := lifecycleidempotency.ResolveProfileTarget(ctx, tx, userID, profileID)
		if err != nil {
			return nil, err
		}
		return []lifecycleidempotency.TargetBinding{target}, nil
	}
}

func (h *ProfileHandler) profileLifecycleJSONResult(ctx context.Context, profile userstore.Profile, writes []profileSettingSync) (lifecycleidempotency.Result, error) {
	prefs := profilePreferences{AudioLanguage: profile.Language, MetadataLanguage: profile.PreferredMetadataLanguage,
		SubtitleLanguage: profile.SubtitleLanguage, SubtitleMode: profile.SubtitleMode, ShowForcedSubtitles: profile.ShowForcedSubtitles}
	defaults := contractProfilePreferences()
	for _, write := range writes {
		value := write.value
		if value == nil {
			switch write.key {
			case "playback.audio_language":
				value, _ = json.Marshal(defaults.AudioLanguage)
			case "catalog.metadata_language":
				value, _ = json.Marshal(defaults.MetadataLanguage)
			case "playback.subtitle_language":
				value, _ = json.Marshal(defaults.SubtitleLanguage)
			case "playback.subtitle_mode":
				value, _ = json.Marshal(defaults.SubtitleMode)
			case "playback.show_forced_subtitles":
				value, _ = json.Marshal(defaults.ShowForcedSubtitles)
			}
		}
		applyProfilePreference(&prefs, write.key, value)
	}
	return memberLifecycleJSONResult(http.StatusOK, h.profileResponseWith(ctx, profile, prefs))
}

func (h *ProfileHandler) publishProfileSettingKeys(ctx context.Context, userID int, profileID string, keys []string) {
	for _, key := range keys {
		publishUserSettingsEvent(ctx, h.EventsHub, userID, profileID, key, string(settingscontract.ScopeProfile))
	}
}

func (h *ProfileHandler) writeProfileLifecycleError(w http.ResponseWriter, err error) {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, lifecycleidempotency.ErrKeyRequired):
		writeError(w, http.StatusPreconditionRequired, "idempotency_key_required", "Idempotency-Key is required for this lifecycle mutation")
	case errors.Is(err, lifecycleidempotency.ErrKeyMalformed):
		writeError(w, http.StatusBadRequest, "idempotency_key_invalid", "Idempotency-Key must be a bounded opaque ASCII value")
	case errors.Is(err, lifecycleidempotency.ErrConflict):
		writeError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key conflicts with its original lifecycle request")
	case errors.Is(err, lifecycleidempotency.ErrPending):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "lifecycle_request_pending", "Lifecycle request completion is pending")
	case errors.Is(err, lifecycleidempotency.ErrInvalidBinding):
		writeError(w, http.StatusUnauthorized, "unauthorized", "Lifecycle request identity is no longer valid")
	case errors.Is(err, lifecycleidempotency.ErrTargetNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Profile not found")
	case errors.Is(err, errLifecycleUnavailable), errors.Is(err, userstore.ErrProfileLifecycleUnsupported):
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
	case errors.Is(err, access.ErrProfileUnverified):
		writeProfileManagementPermissionError(w, err)
	case errors.Is(err, errLifecycleManagementForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "Profile management requires the primary profile or admin access")
	case errors.Is(err, errLifecycleBootstrapAccessSettings):
		writeError(w, http.StatusForbidden, "forbidden", "Profile access settings require the primary profile or admin access")
	case errors.Is(err, errLifecycleDirectProfilePIN):
		writeError(w, http.StatusForbidden, "forbidden", "Direct profile sessions cannot change the profile PIN")
	case errors.Is(err, errLifecycleSelfServiceTarget):
		writeError(w, http.StatusForbidden, "forbidden", "You can only update the active profile's playback preferences")
	case errors.Is(err, errLifecycleSelfServiceAccess):
		writeError(w, http.StatusForbidden, "forbidden", "Profile access settings require the primary profile or admin access")
	case errors.Is(err, errLifecycleNameConflict):
		writeError(w, http.StatusConflict, "name_conflict", "A profile with this name already exists")
	case errors.Is(err, errLifecycleHouseholdProfileLimit), isProfileEntitlementLimitError(err):
		writeError(w, http.StatusConflict, "profile_limit_reached", "This account has reached its profile limit")
	case errors.Is(err, errLifecyclePrimaryProfileProtected):
		writeError(w, http.StatusConflict, "primary_profile_protected", "The primary profile cannot be deleted. Delete the user account instead.")
	case errors.As(err, &pgErr) && pgErr.Code == "P0001" && pgErr.Message == "membership_policy_fenced":
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "membership_policy_fenced", "Profile mutation is temporarily unavailable during membership policy migration")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to mutate profile")
	}
}

func profileLifecycleError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, lifecycleidempotency.ErrKeyRequired):
		return apiError(http.StatusPreconditionRequired, "idempotency_key_required", "Idempotency-Key is required for this lifecycle mutation")
	case errors.Is(err, lifecycleidempotency.ErrKeyMalformed):
		return apiError(http.StatusBadRequest, "idempotency_key_invalid", "Idempotency-Key must be a bounded opaque ASCII value")
	case errors.Is(err, lifecycleidempotency.ErrConflict):
		return apiError(http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key conflicts with its original lifecycle request")
	case errors.Is(err, lifecycleidempotency.ErrPending):
		pending := apiError(http.StatusServiceUnavailable, "lifecycle_request_pending", "Lifecycle request completion is pending")
		pending.RetryAfter = 1
		return pending
	case errors.Is(err, lifecycleidempotency.ErrInvalidBinding):
		return apiError(http.StatusUnauthorized, "unauthorized", "Lifecycle request identity is no longer valid")
	case errors.Is(err, lifecycleidempotency.ErrTargetNotFound):
		return apiError(http.StatusNotFound, "not_found", "Profile not found")
	case errors.Is(err, errLifecycleUnavailable), errors.Is(err, userstore.ErrProfileLifecycleUnsupported):
		return apiError(http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
	case errors.Is(err, access.ErrProfileUnverified):
		return profileManagementError(err)
	case errors.Is(err, errLifecycleManagementForbidden):
		return apiError(http.StatusForbidden, "forbidden", "Profile management requires the primary profile or admin access")
	case errors.Is(err, errLifecycleBootstrapAccessSettings):
		return apiError(http.StatusForbidden, "forbidden", "Profile access settings require the primary profile or admin access")
	case errors.Is(err, errLifecycleDirectProfilePIN):
		return apiError(http.StatusForbidden, "forbidden", "Direct profile sessions cannot change the profile PIN")
	case errors.Is(err, errLifecycleSelfServiceTarget):
		return apiError(http.StatusForbidden, "forbidden", "You can only update the active profile's playback preferences")
	case errors.Is(err, errLifecycleSelfServiceAccess):
		return apiError(http.StatusForbidden, "forbidden", "Profile access settings require the primary profile or admin access")
	case errors.Is(err, errLifecycleNameConflict):
		return apiError(http.StatusConflict, "name_conflict", "A profile with this name already exists")
	case errors.Is(err, errLifecycleHouseholdProfileLimit), isProfileEntitlementLimitError(err):
		return apiError(http.StatusConflict, "profile_limit_reached", "This account has reached its profile limit")
	case errors.Is(err, errLifecyclePrimaryProfileProtected):
		return apiError(http.StatusConflict, "primary_profile_protected", "The primary profile cannot be deleted. Delete the user account instead.")
	case errors.As(err, &pgErr) && pgErr.Code == "P0001" && pgErr.Message == "membership_policy_fenced":
		return apiError(http.StatusServiceUnavailable, "membership_policy_fenced", "Profile mutation is temporarily unavailable during membership policy migration")
	default:
		return apiError(http.StatusInternalServerError, "internal_error", "Failed to mutate profile")
	}
}

func profileLifecycleView(result lifecycleidempotency.Result, err error) (ProfileView, error) {
	if err != nil {
		return ProfileView{}, profileLifecycleError(err)
	}
	var view ProfileView
	if err := json.Unmarshal(result.Body, &view); err != nil {
		return ProfileView{}, apiError(500, "internal_error", "Failed to read profile lifecycle receipt")
	}
	return view, nil
}

// ProfileLifecycleRequest binds a typed API mutation to its authenticated
// account incarnation and exact request bytes before mutable preflight reads.
func (h *ProfileHandler) ProfileLifecycleRequest(ctx context.Context, key, method, routeID, profileID string, body []byte) (*lifecycleidempotency.Request, error) {
	if h.lifecycle == nil {
		return nil, profileLifecycleError(errLifecycleUnavailable)
	}
	claims := apimw.GetClaims(ctx)
	if claims == nil {
		return nil, apiError(401, "unauthorized", "Authentication required")
	}
	incarnation, err := uuid.Parse(claims.AccountIncarnationID)
	if err != nil || incarnation == uuid.Nil {
		return nil, apiError(401, "unauthorized", "Authenticated account identity is incomplete")
	}
	if h.digest == nil {
		return nil, profileLifecycleError(errLifecycleUnavailable)
	}
	var selectors map[string]string
	if profileID != "" {
		selectors = map[string]string{"id": profileID}
	}
	actorID := claims.UserID
	return &lifecycleidempotency.Request{IdempotencyKey: key, Binding: lifecycleidempotency.Binding{
		ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID, ActorAccountIncarnationID: &incarnation,
		Method: method, RouteID: routeID, RequestHash: h.digest(method, routeID, selectors, nil, body), TargetSource: lifecycleidempotency.TargetExactMembership,
	}}, nil
}
