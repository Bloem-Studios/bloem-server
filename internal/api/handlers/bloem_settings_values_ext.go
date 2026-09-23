package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// SetLifecycleIdempotency installs receipt-first coordination for admin
// account setting mutations.
func (h *SettingValuesHandler) SetLifecycleIdempotency(coordinator lifecycleidempotency.Coordinator, digester lifecycleidempotency.RequestDigester) {
	h.lifecycle = coordinator
	h.lifecycleDigest = digester
}

// effectiveSettingValuesResponse names the existing effective-values envelope
// so the client DTO generator can publish its exact wire shape.
type effectiveSettingValuesResponse struct {
	Settings []effectiveSettingValueResponse `json:"settings"`
	Revision int                             `json:"revision"`
}

// clientFamilyForKeys validates an optional family header for effective reads.
// An absent header deliberately drops the profile_client layer so pre-revision
// 5 callers keep resolving broader fallbacks; explicit profile_client reads and
// writes still require the header in identityForSessionKey. The server never
// guesses this identity from X-Silo-Device-Platform: that header is free-form
// display metadata, while client_family is a closed storage key shared by like
// clients.
func (h *SettingValuesHandler) clientFamilyForKeys(
	w http.ResponseWriter, r *http.Request, keys []string,
) (settingscontract.ClientFamily, bool, bool) {
	eligible := false
	for _, key := range keys {
		if def, ok := h.contract.Lookup(key); ok && def.AllowsScope(settingscontract.ScopeProfileClient) {
			eligible = true
			break
		}
	}

	value := strings.TrimSpace(r.Header.Get(clientFamilyHeader))
	if value == "" {
		return "", false, true
	}
	family := settingscontract.ClientFamily(value)
	if !family.Valid() {
		writeError(w, http.StatusBadRequest, "bad_request",
			"X-Silo-Client-Family header must be one of tv, mobile, tablet, desktop or web")
		return "", false, false
	}
	return family, eligible, true
}

func (h *SettingValuesHandler) identityForSessionKey(
	w http.ResponseWriter, r *http.Request, requestedKey string,
) (userstore.SettingIdentity, bool) {
	// No definition uses the account scope today, but the scope arrives from
	// the query string: the moment one exists, a session bound to a single
	// profile would be reading and writing account-wide state through a route
	// the inventory already admits. Refuse before any contract logic — the
	// refusal must not depend on whether the key exists or what its
	// definition allows.
	if settingscontract.Scope(strings.TrimSpace(r.URL.Query().Get("scope"))) == settingscontract.ScopeAccount &&
		apimw.IsDirectProfileSession(r) {
		writeError(w, http.StatusForbidden, "forbidden",
			"Direct profile sessions cannot use account-scoped settings")
		return userstore.SettingIdentity{}, false
	}

	key, scope, ok := h.keyedScope(w, requestedKey, r.URL.Query())
	if !ok {
		return userstore.SettingIdentity{}, false
	}

	identity := userstore.SettingIdentity{Key: key, Scope: scope}

	// The profile defaults to the session header, so an ordinary caller cannot
	// write another's settings by naming it. A household parent may name a
	// different profile on their own account — authorized below.
	if scope != settingscontract.ScopeAccount {
		identity.ProfileID = strings.TrimSpace(apimw.GetProfileID(r.Context()))
		if identity.ProfileID == "" {
			writeError(w, http.StatusBadRequest, "bad_request",
				"X-Profile-Id header is required for this scope")
			return userstore.SettingIdentity{}, false
		}
		if named := strings.TrimSpace(r.URL.Query().Get("profile_id")); named != "" &&
			named != identity.ProfileID {
			if !h.mayActForProfile(w, r, named) {
				return userstore.SettingIdentity{}, false
			}
			identity.ProfileID = named
		}
	}
	if scope == settingscontract.ScopeProfileClient {
		family := settingscontract.ClientFamily(strings.TrimSpace(r.Header.Get(clientFamilyHeader)))
		if !family.Valid() {
			writeError(w, http.StatusBadRequest, "bad_request",
				"X-Silo-Client-Family header must be one of tv, mobile, tablet, desktop or web")
			return userstore.SettingIdentity{}, false
		}
		identity.ClientFamily = family
	}
	if scope == settingscontract.ScopeProfileDevice {
		// A device may be named explicitly so one device can manage another's
		// settings — the screen that lists your devices edits them in place.
		// Unlike the profile above, that is safe to accept from the query only
		// because the device is then checked against this profile's registry.
		named := strings.TrimSpace(r.URL.Query().Get("device_id"))
		identity.DeviceID = named
		if identity.DeviceID == "" {
			identity.DeviceID = deviceMetadataFromRequest(r).DeviceID
		}
		if identity.DeviceID == "" {
			writeError(w, http.StatusBadRequest, "bad_request",
				"X-Silo-Device-Id header is required for a device override")
			return userstore.SettingIdentity{}, false
		}
		if named != "" && !h.deviceBelongsToProfile(w, r, identity.ProfileID, named) {
			return userstore.SettingIdentity{}, false
		}
	}

	return h.completeIdentity(w, r.Context(), r.URL.Query(), identity)
}

// mayActForProfile authorizes acting for a profile other than the caller's own.
//
// Two checks, in this order and for different reasons. First the household
// guard: only the primary profile (or a server admin) manages the household, so
// an ordinary member naming a sibling is 403 — the profile plainly exists, and
// pretending otherwise would be a lie the caller can already disprove through
// GET /profiles. Then existence, resolved through the caller's *own* user
// store, which is what confines this to one account: a profile id from another
// account is simply absent there, so it is 404 and the caller learns nothing.
func (h *SettingValuesHandler) mayActForProfile(
	w http.ResponseWriter, r *http.Request, profileID string,
) bool {
	store, ok := h.storeFor(w, r)
	if !ok {
		return false
	}

	allowed, err := canManageHousehold(r, store, h.UserRepo, h.ProfileTokens)
	if err != nil {
		writeProfileManagementPermissionError(w, err)
		return false
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden",
			"Managing another profile's settings requires the primary profile or admin access")
		return false
	}

	profile, err := store.GetProfile(r.Context(), profileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load profile")
		return false
	}
	if profile == nil {
		writeError(w, http.StatusNotFound, "not_found", "Profile not found")
		return false
	}
	return true
}

// deviceBelongsToProfile authorizes a device id that came from the query rather
// than from this request's own header. It answers 404 rather than 403 for an
// unknown device: a 403 would confirm the id exists somewhere.
//
// The caller's own header device is deliberately not checked. Registration is
// lazy — a device's first write is what registers it — so requiring a row there
// would reject every new device's first setting.
func (h *SettingValuesHandler) deviceBelongsToProfile(
	w http.ResponseWriter, r *http.Request, profileID, deviceID string,
) bool {
	store, ok := h.storeFor(w, r)
	if !ok {
		return false
	}
	registry, ok := store.(userstore.DeviceRegistry)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "Device not found")
		return false
	}
	exists, err := registry.DeviceExists(r.Context(), profileID, deviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to look up device")
		return false
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not_found", "Device not found")
		return false
	}
	return true
}

// keyedScopeFromRequest parses the parts every scoped request names: a key
// that exists in the contract and is remote, plus an explicit scope.
func (h *SettingValuesHandler) keyedScopeFromRequest(
	w http.ResponseWriter, r *http.Request,
) (string, settingscontract.Scope, bool) {
	return h.keyedScope(w, chi.URLParam(r, "key"), r.URL.Query())
}

func (h *SettingValuesHandler) keyedScope(
	w http.ResponseWriter, requestedKey string, query url.Values,
) (string, settingscontract.Scope, bool) {
	key := strings.TrimSpace(requestedKey)
	if strings.TrimSpace(key) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "A setting key is required")
		return "", "", false
	}
	if _, ok := h.definitionFor(w, key); !ok {
		return "", "", false
	}

	scope := settingscontract.Scope(strings.TrimSpace(query.Get("scope")))
	if scope == "" {
		writeError(w, http.StatusBadRequest, "bad_request",
			"A scope is required: account, profile, profile_client, profile_device, profile_library or profile_series")
		return "", "", false
	}
	return key, scope, true
}

// completeIdentity fills the content-scope ids from the query, then runs the
// checks the session and admin routes share: the identity matches its scope's
// columns and the contract allows the key at that scope.
func (h *SettingValuesHandler) completeIdentity(
	w http.ResponseWriter, ctx context.Context, query url.Values, identity userstore.SettingIdentity,
) (userstore.SettingIdentity, bool) {
	if identity.Scope == settingscontract.ScopeProfileLibrary {
		libraryID, err := strconv.Atoi(strings.TrimSpace(query.Get("library_id")))
		if err != nil || libraryID <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request",
				"library_id is required for a library override")
			return userstore.SettingIdentity{}, false
		}
		identity.LibraryID = libraryID
		if !h.libraryContextExists(w, ctx, libraryID) {
			return userstore.SettingIdentity{}, false
		}
	}
	if identity.Scope == settingscontract.ScopeProfileSeries {
		identity.SeriesID = strings.TrimSpace(query.Get("series_id"))
		if identity.SeriesID == "" {
			writeError(w, http.StatusBadRequest, "bad_request",
				"series_id is required for a series override")
			return userstore.SettingIdentity{}, false
		}
	}

	if err := identity.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return userstore.SettingIdentity{}, false
	}

	// The contract decides where a setting may be written, independently of
	// whether the identity is well formed.
	def, _ := h.contract.Lookup(identity.Key)
	if !def.AllowsScope(identity.Scope) {
		writeError(w, http.StatusBadRequest, "scope_not_allowed",
			identity.Key+" cannot be set at "+string(identity.Scope))
		return userstore.SettingIdentity{}, false
	}

	return identity, true
}

func (h *SettingValuesHandler) libraryContextExists(
	w http.ResponseWriter, ctx context.Context, libraryID int,
) bool {
	if h.libraryLookup == nil {
		return true
	}
	if _, err := h.libraryLookup.GetByID(ctx, libraryID); err != nil {
		if errors.Is(err, catalog.ErrFolderNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Library not found")
			return false
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to look up library")
		return false
	}
	return true
}
