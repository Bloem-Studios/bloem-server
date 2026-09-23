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
	"github.com/Silo-Server/silo-server/internal/settingskeys"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

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
		if !writeBloemLifecycleError(w, err) {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to mutate setting")
		}
		return
	}
	if !result.Replayed && changed {
		publishUserSettingsEvent(r.Context(), h.EventsHub, userID, identity.ProfileID, identity.Key, string(identity.Scope))
	}
	writeLifecycleResult(w, result)
}
