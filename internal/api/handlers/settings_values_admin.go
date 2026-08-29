package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/settingskeys"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

const adminNavigationShortcutRepairMessage = "Use whole-document PUT on this admin resource to clear or replace navigation shortcuts"

// Admin projections of the canonical settings API. These replace the string
// registry's /admin/users/{id}/settings* and device-settings* routes with the
// same typed surface clients use on /settings/values: the same validation, the
// same scopes, the same response shapes. The only differences are that the
// target user comes from the path instead of the session, and that profile and
// device ids come from the query string — an admin has no session claim to the
// user they are inspecting.
//
// Mounted behind requireActingAdmin next to the other /admin/users routes, so
// authorization is the router group's, not re-checked here.

// HandleAdminListUserSettingValues handles
// GET /admin/users/{id}/settings/values: every explicit value the target user
// has stored, across all scopes. It deliberately lists stored rows rather than
// resolving: the admin surface answers "what overrides exist" (and offers a
// reset per row), which is the same question the session route's per-scope GET
// answers for one identity.
func (h *SettingValuesHandler) HandleAdminListUserSettingValues(w http.ResponseWriter, r *http.Request) {
	store, _, ok := h.adminTargetStore(w, r)
	if !ok {
		return
	}

	values, err := store.ListAllSettingValues(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list settings")
		return
	}
	out := make([]settingValueResponse, 0, len(values))
	for _, value := range values {
		out = append(out, settingValueToResponse(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		fieldValues:   out,
		fieldRevision: h.contract.Revision,
	})
}

// HandleAdminSetUserSettingValue handles
// PUT /admin/users/{id}/settings/values/{key}: write an explicit value at one
// scope on behalf of the target user, through the same validation and
// idempotency path as the session route.
func (h *SettingValuesHandler) HandleAdminSetUserSettingValue(w http.ResponseWriter, r *http.Request) {
	if h.lifecycle != nil && h.lifecycleDigest != nil {
		h.handleLifecycleAdminSettingMutation(w, r, false)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	store, userID, ok := h.adminTargetStore(w, r)
	if !ok {
		return
	}
	identity, ok := h.adminIdentityFromRequest(w, r)
	if !ok {
		return
	}
	// The session route's profile is validated by middleware; the admin names
	// one in the query, so its existence is checked here. Postgres would refuse
	// an orphan row on its profile FK anyway — checking first turns that 500
	// into a 404 and gives SQLite the same behavior.
	if identity.ProfileID != "" && !adminProfileExists(w, r, store, identity.ProfileID) {
		return
	}
	h.setValueAt(w, r, store, userID, identity)
}

// HandleAdminDeleteUserSettingValue handles
// DELETE /admin/users/{id}/settings/values/{key}: remove the target user's
// explicit value at one scope so inheritance applies again.
func (h *SettingValuesHandler) HandleAdminDeleteUserSettingValue(w http.ResponseWriter, r *http.Request) {
	if h.lifecycle != nil && h.lifecycleDigest != nil {
		h.handleLifecycleAdminSettingMutation(w, r, true)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	store, userID, ok := h.adminTargetStore(w, r)
	if !ok {
		return
	}
	identity, ok := h.adminIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if identity.Key == settingskeys.NavShortcuts {
		writeError(w, http.StatusBadRequest, "atomic_update_required",
			adminNavigationShortcutRepairMessage)
		return
	}
	h.deleteValueAt(w, r, store, userID, identity)
}

type lifecycleSettingStore interface {
	SettingMutationWriterInTransaction(context.Context, pgx.Tx) userstore.SettingMutationWriter
}

type lifecycleBoundSettingStore struct {
	userstore.UserStore
	writer userstore.SettingMutationWriter
}

func (s lifecycleBoundSettingStore) WithSettingMutationTransaction(_ context.Context, _ string, fn func(userstore.SettingMutationWriter) error) error {
	return fn(s.writer)
}

type lifecycleSettingHTTPError struct{ response *httptest.ResponseRecorder }

func (e *lifecycleSettingHTTPError) Error() string { return "canonical setting mutation rejected" }

func (h *SettingValuesHandler) handleLifecycleAdminSettingMutation(w http.ResponseWriter, r *http.Request, deleting bool) {
	selector := strings.TrimSpace(chi.URLParam(r, "id"))
	userID, err := strconv.Atoi(selector)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid user ID")
		return
	}
	identity, ok := h.adminIdentityFromRequest(w, r)
	if !ok {
		return
	}
	if deleting && identity.Key == settingskeys.NavShortcuts {
		writeError(w, http.StatusBadRequest, "atomic_update_required", adminNavigationShortcutRepairMessage)
		return
	}
	var body []byte
	if !deleting {
		body, err = io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
			return
		}
	}
	actorID, actorIncarnation, ok := lifecycleActor(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authenticated account identity is incomplete")
		return
	}
	routeID := "account.setting.set"
	if deleting {
		routeID = "account.setting.delete"
	}
	request := lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID,
			ActorAccountIncarnationID: &actorIncarnation, Method: r.Method, RouteID: routeID,
			RequestHash:  h.lifecycleDigest(r.Method, routeID, map[string]string{"id": selector, "key": identity.Key}, r.URL.Query(), body),
			TargetSource: lifecycleidempotency.TargetPathAccount,
		},
		ResolveTargets: func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
			targets, err := lifecycleidempotency.ResolveAccountTargets(ctx, tx, userID)
			if err != nil {
				return nil, err
			}
			if identity.ProfileID != "" {
				var exists bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_profiles WHERE user_id=$1 AND id=$2)`, userID, identity.ProfileID).Scan(&exists); err != nil {
					return nil, err
				}
				if !exists {
					return nil, errLifecycleProfileNotFound
				}
			}
			for i := range targets {
				targets[i].ProfileID = identity.ProfileID
				targets[i].ResourceID = identity.Key
			}
			return targets, nil
		},
	}
	var changed bool
	result, err := h.lifecycle.Execute(r.Context(), request, func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
		store, err := h.storeProvider.ForUser(ctx, userID)
		if err != nil || store == nil {
			return lifecycleidempotency.Result{}, errors.New("target user store unavailable")
		}
		transactional, ok := store.(lifecycleSettingStore)
		if !ok {
			return lifecycleidempotency.Result{}, errors.New("settings store does not support caller-owned transactions")
		}
		bound := lifecycleBoundSettingStore{UserStore: store, writer: transactional.SettingMutationWriterInTransaction(ctx, tx)}
		inner := *h
		inner.EventsHub = nil
		requestCopy := r.Clone(ctx)
		requestCopy.Body = io.NopCloser(bytes.NewReader(body))
		recorder := httptest.NewRecorder()
		if deleting {
			inner.deleteValueAt(recorder, requestCopy, bound, userID, identity)
		} else {
			inner.setValueAt(recorder, requestCopy, bound, userID, identity)
		}
		if recorder.Code >= http.StatusBadRequest {
			return lifecycleidempotency.Result{}, &lifecycleSettingHTTPError{response: recorder}
		}
		changed = recorder.Header().Get("X-Silo-Idempotent-Replay") == ""
		return lifecycleidempotency.Result{Status: recorder.Code, Body: recorder.Body.Bytes(), Headers: recorder.Header()}, nil
	})
	if err != nil {
		var httpErr *lifecycleSettingHTTPError
		if errors.As(err, &httpErr) {
			for key, values := range httpErr.response.Header() {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.WriteHeader(httpErr.response.Code)
			_, _ = w.Write(httpErr.response.Body.Bytes())
			return
		}
		if errors.Is(err, errLifecycleProfileNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Profile not found")
			return
		}
		if !writeV2LifecycleError(w, err) {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to mutate setting")
		}
		return
	}
	if !result.Replayed && changed {
		publishUserSettingsEvent(r.Context(), h.EventsHub, userID, identity.ProfileID, identity.Key, string(identity.Scope))
	}
	writeLifecycleResult(w, result)
}

// adminTargetStore resolves the {id} path parameter to the target user's
// store.
func (h *SettingValuesHandler) adminTargetStore(
	w http.ResponseWriter, r *http.Request,
) (userstore.UserStore, int, bool) {
	userID, ok := parseAdminUserIDParam(w, r)
	if !ok {
		return nil, 0, false
	}
	store, err := h.storeProvider.ForUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access user store")
		return nil, 0, false
	}
	if store == nil {
		writeError(w, http.StatusNotFound, "not_found", "User store not found")
		return nil, 0, false
	}
	return store, userID, true
}

// adminIdentityFromRequest is identityFromRequest with the profile and device
// taken from the query string instead of the session: the admin is not the
// user being addressed, so there are no session headers to trust. Everything
// after that — content-scope ids, identity validation, the contract's scope
// allowance — is the shared completeIdentity path.
func (h *SettingValuesHandler) adminIdentityFromRequest(
	w http.ResponseWriter, r *http.Request,
) (userstore.SettingIdentity, bool) {
	key, scope, ok := h.keyedScopeFromRequest(w, r)
	if !ok {
		return userstore.SettingIdentity{}, false
	}

	query := r.URL.Query()
	identity := userstore.SettingIdentity{Key: key, Scope: scope}
	if scope != settingscontract.ScopeAccount {
		identity.ProfileID = strings.TrimSpace(query.Get("profile_id"))
		if identity.ProfileID == "" {
			writeError(w, http.StatusBadRequest, "bad_request",
				"profile_id is required for this scope")
			return userstore.SettingIdentity{}, false
		}
	}
	if scope == settingscontract.ScopeProfileClient {
		identity.ClientFamily = settingscontract.ClientFamily(strings.TrimSpace(query.Get("client_family")))
		if !identity.ClientFamily.Valid() {
			writeError(w, http.StatusBadRequest, "bad_request",
				"client_family must be one of tv, mobile, tablet, desktop or web")
			return userstore.SettingIdentity{}, false
		}
	}
	if scope == settingscontract.ScopeProfileDevice {
		identity.DeviceID = strings.TrimSpace(query.Get("device_id"))
		if identity.DeviceID == "" {
			writeError(w, http.StatusBadRequest, "bad_request",
				"device_id is required for a device override")
			return userstore.SettingIdentity{}, false
		}
	}

	return h.completeIdentity(w, r.Context(), query, identity)
}
