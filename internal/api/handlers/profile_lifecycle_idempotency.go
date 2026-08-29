package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

var errLifecyclePrimaryProfileProtected = errors.New("primary profile is protected")

func (h *ProfileHandler) profileLifecycleTransactioner(w http.ResponseWriter, store userstore.UserStore) (userstore.ProfileLifecycleTransactioner, bool) {
	txStore, ok := store.(userstore.ProfileLifecycleTransactioner)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
	}
	return txStore, ok
}

func (h *ProfileHandler) handleLifecycleProfileCreate(w http.ResponseWriter, r *http.Request, store userstore.UserStore, userID int, profile userstore.Profile, req createProfileRequest, writes []profileSettingSync, body []byte) {
	txStore, ok := h.profileLifecycleTransactioner(w, store)
	if !ok {
		return
	}
	request, ok := h.profileLifecycleRequest(w, r, "profile.create", nil, body)
	if !ok {
		return
	}
	var changedKeys []string
	result, err := h.lifecycle.ExecuteCreate(r.Context(), request,
		func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, lifecycleidempotency.Result, error) {
			var created *userstore.Profile
			var responseWrites []profileSettingSync
			err := txStore.WithProfileLifecycleTransaction(ctx, tx, func(writer userstore.ProfileLifecycleWriter) error {
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
		h.writeProfileLifecycleError(w, err)
		return
	}
	if !result.Replayed {
		h.publishProfileSettingKeys(r.Context(), userID, profile.ID, changedKeys)
	}
	writeLifecycleResult(w, result)
}

func (h *ProfileHandler) handleLifecycleProfileUpdate(w http.ResponseWriter, r *http.Request, store userstore.UserStore, userID int, profileID string, input userstore.UpdateProfileInput, writes []profileSettingSync, body []byte) {
	txStore, ok := h.profileLifecycleTransactioner(w, store)
	if !ok {
		return
	}
	request, ok := h.profileLifecycleRequest(w, r, "profile.update", map[string]string{"id": profileID}, body)
	if !ok {
		return
	}
	request.ResolveTargets = profileTargetResolver(userID, profileID)
	var changedKeys []string
	var committedOriginal *userstore.Profile
	result, err := h.lifecycle.Execute(r.Context(), request,
		func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
			var updated *userstore.Profile
			err := txStore.WithProfileLifecycleTransaction(ctx, tx, func(writer userstore.ProfileLifecycleWriter) error {
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
		h.writeProfileLifecycleError(w, err)
		return
	}
	if !result.Replayed {
		h.publishProfileSettingKeys(r.Context(), userID, profileID, changedKeys)
		h.completeProfileUpdateAfterCommit(r.Context(), userID, committedOriginal, input)
	}
	writeLifecycleResult(w, result)
}

func (h *ProfileHandler) handleLifecycleProfileDelete(w http.ResponseWriter, r *http.Request, store userstore.UserStore, userID int, profileID string) {
	txStore, ok := h.profileLifecycleTransactioner(w, store)
	if !ok {
		return
	}
	request, ok := h.profileLifecycleRequest(w, r, "profile.delete", map[string]string{"id": profileID}, nil)
	if !ok {
		return
	}
	request.ResolveTargets = profileTargetResolver(userID, profileID)
	var deleted *userstore.Profile
	result, err := h.lifecycle.Execute(r.Context(), request,
		func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
			err := txStore.WithProfileLifecycleTransaction(ctx, tx, func(writer userstore.ProfileLifecycleWriter) error {
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
		h.writeProfileLifecycleError(w, err)
		return
	}
	if !result.Replayed {
		h.completeProfileDeleteAfterCommit(r.Context(), userID, deleted)
	}
	writeLifecycleResult(w, result)
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
	case errors.Is(err, errLifecyclePrimaryProfileProtected):
		writeError(w, http.StatusConflict, "primary_profile_protected", "The primary profile cannot be deleted. Delete the user account instead.")
	case errors.As(err, &pgErr) && pgErr.Code == "P0001" && pgErr.Message == "membership_policy_fenced":
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "membership_policy_fenced", "Profile mutation is temporarily unavailable during membership policy migration")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to mutate profile")
	}
}
