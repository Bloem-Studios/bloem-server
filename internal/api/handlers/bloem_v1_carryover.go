package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// Bloem-owned. Silo retired /api/v1 (Silo #3200) and deleted the v1 handler
// code these declarations lived in. Bloem's own routes under /api/bloem/v1
// (organization groups and invitations, platform account devices,
// notifications, sync progress, profile credentials, similar titles, catalog
// seeds) still write these wire shapes and use these helpers, so they are
// kept here verbatim instead of in Silo's files.

func (h *EbookReaderHandler) writeReadError(w http.ResponseWriter, err error) {
	if apiErr, ok := errors.AsType[*APIError](err); ok {
		writeError(w, apiErr.Status, apiErr.Code, apiErr.Message)
		return
	}
	switch {
	case errors.Is(err, catalog.ErrItemNotFound), errors.Is(err, catalog.ErrEpisodeNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Ebook file not found")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to authorize ebook file")
	}
}

// parseClientEventTime parses an RFC3339 client event time. Malformed values
// are an error the caller must reject: treating them as "now" would let a
// stale offline event win LWW as a fresh server-time write.
func parseClientEventTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

type syncProgressItem struct {
	MediaItemID    string  `json:"media_item_id"`
	Position       float64 `json:"position"`
	Duration       float64 `json:"duration"`
	ForceOverwrite bool    `json:"force_overwrite"`
	// UpdatedAt is the client EVENT time (RFC3339) for an offline-queued item.
	// The server clamps it to now+skew and uses it only as the LWW key.
	UpdatedAt *string `json:"updated_at,omitempty"`
}

type syncProgressRequest struct {
	Items []syncProgressItem `json:"items"`
}

type syncProgressResponse struct {
	Results []syncProgressResultItem `json:"results"`
}

type accessGroupCreateRequest struct {
	Name                     string                      `json:"name"`
	Description              string                      `json:"description"`
	LibraryIDs               accessGroupIntSliceField    `json:"library_ids"`
	MaxPlaybackQuality       string                      `json:"max_playback_quality"`
	DownloadAllowed          *bool                       `json:"download_allowed,omitempty"`
	DownloadTranscodeAllowed *bool                       `json:"download_transcode_allowed,omitempty"`
	TranscodeAllowed         *bool                       `json:"transcode_allowed,omitempty"`
	AudioTranscodeAllowed    *bool                       `json:"audio_transcode_allowed,omitempty"`
	MaxStreams               *int                        `json:"max_streams,omitempty"`
	MaxTranscodes            *int                        `json:"max_transcodes,omitempty"`
	AllowedPermissions       accessGroupStringSliceField `json:"allowed_permissions"`
	RequestsAllowed          *bool                       `json:"requests_allowed,omitempty"`
	IsDefault                bool                        `json:"is_default"`
}

type accessGroupUpdateRequest struct {
	Name                     *string                     `json:"name,omitempty"`
	Description              *string                     `json:"description,omitempty"`
	LibraryIDs               accessGroupIntSliceField    `json:"library_ids,omitempty"`
	MaxPlaybackQuality       *string                     `json:"max_playback_quality,omitempty"`
	DownloadAllowed          *bool                       `json:"download_allowed,omitempty"`
	DownloadTranscodeAllowed *bool                       `json:"download_transcode_allowed,omitempty"`
	TranscodeAllowed         *bool                       `json:"transcode_allowed,omitempty"`
	AudioTranscodeAllowed    *bool                       `json:"audio_transcode_allowed,omitempty"`
	MaxStreams               *int                        `json:"max_streams,omitempty"`
	MaxTranscodes            *int                        `json:"max_transcodes,omitempty"`
	AllowedPermissions       accessGroupStringSliceField `json:"allowed_permissions,omitempty"`
	RequestsAllowed          *bool                       `json:"requests_allowed,omitempty"`
	IsDefault                *bool                       `json:"is_default,omitempty"`
}

type accessGroupResponse struct {
	ID                       int64     `json:"id"`
	Name                     string    `json:"name"`
	Description              string    `json:"description"`
	LibraryIDs               []int     `json:"library_ids"`
	MaxPlaybackQuality       string    `json:"max_playback_quality"`
	DownloadAllowed          bool      `json:"download_allowed"`
	DownloadTranscodeAllowed bool      `json:"download_transcode_allowed"`
	TranscodeAllowed         bool      `json:"transcode_allowed"`
	AudioTranscodeAllowed    bool      `json:"audio_transcode_allowed"`
	MaxStreams               int       `json:"max_streams"`
	MaxTranscodes            int       `json:"max_transcodes"`
	AllowedPermissions       []string  `json:"allowed_permissions"`
	RequestsAllowed          bool      `json:"requests_allowed"`
	IsDefault                bool      `json:"is_default"`
	MemberCount              int       `json:"member_count"`
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
}

type accessGroupIntSliceField struct {
	Set   bool
	Value []int
}

func (f *accessGroupIntSliceField) UnmarshalJSON(data []byte) error {
	f.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		f.Value = nil
		return nil
	}
	return json.Unmarshal(data, &f.Value)
}

type accessGroupStringSliceField struct {
	Set   bool
	Value []string
}

func (f *accessGroupStringSliceField) UnmarshalJSON(data []byte) error {
	f.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		f.Value = nil
		return nil
	}
	return json.Unmarshal(data, &f.Value)
}

func toAccessGroupResponse(group access.Group) accessGroupResponse {
	return accessGroupResponse{
		ID:                       group.ID,
		Name:                     group.Name,
		Description:              group.Description,
		LibraryIDs:               slices.Clone(group.LibraryIDs),
		MaxPlaybackQuality:       access.NormalizePlaybackQuality(group.MaxPlaybackQuality),
		DownloadAllowed:          group.DownloadAllowed,
		DownloadTranscodeAllowed: group.DownloadTranscodeAllowed,
		TranscodeAllowed:         group.TranscodeAllowed,
		AudioTranscodeAllowed:    group.AudioTranscodeAllowed,
		MaxStreams:               group.MaxStreams,
		MaxTranscodes:            group.MaxTranscodes,
		AllowedPermissions:       slices.Clone(group.AllowedPermissions),
		RequestsAllowed:          group.RequestsAllowed,
		IsDefault:                group.IsDefault,
		MemberCount:              group.MemberCount,
		CreatedAt:                group.CreatedAt,
		UpdatedAt:                group.UpdatedAt,
	}
}

type invitationResponse struct {
	ID            int64     `json:"id"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	AccessGroupID *int64    `json:"access_group_id,omitempty"`
	LibraryIDs    []int     `json:"library_ids,omitempty"`
	CreateProfile bool      `json:"create_profile"`
	ShowTour      bool      `json:"show_tour"`
	Note          string    `json:"note,omitempty"`
	InvitedBy     int64     `json:"invited_by"`
	InvitedByName string    `json:"invited_by_name,omitempty"`
	Status        string    `json:"status"`
	ExpiresAt     time.Time `json:"expires_at"`
	AcceptedAt    *string   `json:"accepted_at,omitempty"`
	AcceptedUser  *int64    `json:"accepted_user_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

func toInvitationResponse(inv *models.Invitation, now time.Time) invitationResponse {
	resp := invitationResponse{
		ID:            inv.ID,
		Email:         inv.Email,
		Role:          inv.Role,
		AccessGroupID: inv.AccessGroupID,
		LibraryIDs:    inv.LibraryIDs,
		CreateProfile: inv.CreateProfile,
		ShowTour:      inv.ShowTour,
		Note:          inv.Note,
		InvitedBy:     inv.InvitedBy,
		InvitedByName: inv.InvitedByName,
		Status:        inv.Status(now),
		ExpiresAt:     inv.ExpiresAt,
		AcceptedUser:  inv.AcceptedUserID,
		CreatedAt:     inv.CreatedAt,
	}
	if inv.AcceptedAt != nil {
		accepted := inv.AcceptedAt.Format(time.RFC3339)
		resp.AcceptedAt = &accepted
	}
	return resp
}

func parseNotificationsLimit(r *http.Request, fallback int) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return fallback
	}
	return min(limit, notificationsMaxLimit)
}

type deviceResponse struct {
	DeviceID       string `json:"device_id"`
	DeviceName     string `json:"device_name"`
	DevicePlatform string `json:"device_platform"`
	LastSeenAt     string `json:"last_seen_at"`
	ProfileID      string `json:"profile_id"`
	ProfileName    string `json:"profile_name"`
	// IsCurrentDevice marks the device this request came from, so a client can
	// say "you're here" without repeating the header-matching rule.
	IsCurrentDevice bool `json:"is_current_device"`
	// ChangedCount is how many settings this (profile, device) pair overrides.
	// It is the one number a device list needs: everything else about a device
	// is either its identity or its last-seen time.
	ChangedCount int `json:"changed_count"`
}

type deviceKey struct {
	profileID string
	deviceID  string
}

// deviceOverrideCounts counts the preferences a (profile, device) pair
// overrides. ListAllSettingValues spans the whole account, so the caller filters
// the result to the profile it is answering for.
//
// Preferences, not rows: a key the contract mirrors onto its replacement is
// stored twice for the length of the overlap window, and a badge that read
// "2 settings changed" because the viewer picked one intro-skip mode would be
// counting the compatibility copy as a second choice.
func deviceOverrideCounts(r *http.Request, store userstore.UserStore) (map[deviceKey]int, error) {
	values, err := store.ListAllSettingValues(r.Context())
	if err != nil {
		return nil, err
	}
	logical := make(map[deviceKey]map[string]struct{})
	for _, value := range values {
		if value.Scope != settingscontract.ScopeProfileDevice {
			continue
		}
		id := deviceKey{profileID: value.ProfileID, deviceID: value.DeviceID}
		keys, ok := logical[id]
		if !ok {
			keys = make(map[string]struct{})
			logical[id] = keys
		}
		keys[settingscontract.LogicalKey(value.Key)] = struct{}{}
	}
	counts := make(map[deviceKey]int, len(logical))
	for id, keys := range logical {
		counts[id] = len(keys)
	}
	return counts, nil
}

// canManageHousehold reports whether the caller may act for the whole household
// — every profile on their own account — rather than only for themselves.
//
// The caller's active profile must be the one flagged is_primary, which is the
// household parent and is deliberately *not* the server-wide admin role: a
// household parent manages their family, an admin manages the server. The
// admin role does not widen this. A non-primary profile on an admin account is
// a household member like any other, exactly as RequireActingAdmin refuses it
// admin powers; otherwise a child profile on the admin's account could lift
// its own limits or clear the parent's PIN.
//
// When that primary profile has a PIN, management additionally requires a valid
// X-Profile-Token from /profiles/{id}/verify-pin. Without that, a client could
// walk past a profile lock by sending only X-Profile-Id.
//
// A request that names no profile manages the household only when it is an
// admin API key, or comes from an admin account whose household has no limited
// profile (see
// access.HouseholdRequiresProfile): first-run and admin tooling keep working,
// but a device signed into the account cannot drop X-Profile-Id to get past a
// child's limits or the parent's PIN.
//
// This is a policy boundary for well-behaved clients rather than a defense
// against the account holder: every profile on an account shares one login
// session, so X-Profile-Id is self-asserted (see the note in
// internal/api/middleware/auth.go). It is the same boundary profile management
// has always used, and it is applied here so household settings management is
// guarded and auditable rather than implicit.
func canManageHousehold(
	r *http.Request,
	store userstore.UserStore,
	tokens *access.ProfileTokenService,
) (bool, error) {
	return canManageHouseholdAs(r.Context(), store, apimw.ActiveProfileID(r), func(profile *userstore.Profile) error {
		return verifyProfileTokenFor(r, tokens, profile)
	})
}

// verifyProfileTokenFor checks the X-Profile-Token a PIN-locked profile must
// present against the profile row the household check just loaded, so the
// token must match that row's current PIN revision (see
// access.CheckProfileToken). A handler wired without a token service fails
// closed: "cannot verify" is not "verified".
func verifyProfileTokenFor(r *http.Request, tokens *access.ProfileTokenService, profile *userstore.Profile) error {
	if tokens == nil {
		return access.ErrProfileUnverified
	}
	claims := apimw.GetClaims(r.Context())
	if claims == nil || claims.SessionID == "" || claims.UserID == 0 {
		return access.ErrProfileUnverified
	}
	return access.CheckProfileToken(tokens, r.Header.Get("X-Profile-Token"), claims.UserID, claims.SessionID, profile)
}

const catalogSeedImportPrefix = "catalog-seeds/"

// canManageHouseholdProfiles reports whether the caller may create/update/delete
// profiles belonging to their user.
//
// The rule lives in household.go so the settings routes can apply the same one:
// a household parent who may edit a child's profile may also edit that child's
// device settings, and two definitions of "is this the household parent" would
// eventually disagree.
func (h *ProfileHandler) canManageHouseholdProfiles(r *http.Request, store userstore.UserStore) (bool, error) {
	return canManageHousehold(r, store, h.ProfileTokens)
}

func writeProfileManagementPermissionError(w http.ResponseWriter, err error) {
	if errors.Is(err, access.ErrProfileUnverified) {
		writeError(w, http.StatusForbidden, "forbidden", "Profile management requires verifying the primary profile PIN")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "Failed to check profile permissions")
}

// parsePagination extracts limit and offset from query parameters with defaults.
func parsePagination(r *http.Request) (int, int) {
	q := r.URL.Query()

	limit := 50
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	offset := 0
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	return limit, offset
}

const (
	notificationsDefaultLimit = 25
	notificationsSyncLimit    = 50
	notificationsMaxLimit     = 100
)

func (r accessGroupCreateRequest) toInput(w http.ResponseWriter) (access.CreateGroupInput, bool) {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name is required")
		return access.CreateGroupInput{}, false
	}
	maxPlaybackQuality, ok := normalizeAccessGroupQuality(w, r.MaxPlaybackQuality)
	if !ok {
		return access.CreateGroupInput{}, false
	}
	if !validateAccessGroupIDs(w, r.LibraryIDs.Value) {
		return access.CreateGroupInput{}, false
	}
	allowedPermissions, ok := normalizeAccessGroupPermissions(w, r.AllowedPermissions)
	if !ok {
		return access.CreateGroupInput{}, false
	}
	downloadAllowed := true
	if r.DownloadAllowed != nil {
		downloadAllowed = *r.DownloadAllowed
	}
	downloadTranscodeAllowed := true
	if r.DownloadTranscodeAllowed != nil {
		downloadTranscodeAllowed = *r.DownloadTranscodeAllowed
	}
	transcodeAllowed := true
	if r.TranscodeAllowed != nil {
		transcodeAllowed = *r.TranscodeAllowed
	}
	audioTranscodeAllowed := true
	if r.AudioTranscodeAllowed != nil {
		audioTranscodeAllowed = *r.AudioTranscodeAllowed
	}
	requestsAllowed := true
	if r.RequestsAllowed != nil {
		requestsAllowed = *r.RequestsAllowed
	}
	maxStreams := 0
	if r.MaxStreams != nil {
		maxStreams = *r.MaxStreams
	}
	maxTranscodes := 0
	if r.MaxTranscodes != nil {
		maxTranscodes = *r.MaxTranscodes
	}
	if maxStreams < 0 || maxTranscodes < 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "stream limits must be non-negative")
		return access.CreateGroupInput{}, false
	}
	return access.CreateGroupInput{
		Name:                     name,
		Description:              r.Description,
		LibraryIDs:               r.LibraryIDs.Value,
		MaxPlaybackQuality:       maxPlaybackQuality,
		DownloadAllowed:          downloadAllowed,
		DownloadTranscodeAllowed: downloadTranscodeAllowed,
		TranscodeAllowed:         transcodeAllowed,
		AudioTranscodeAllowed:    audioTranscodeAllowed,
		MaxStreams:               maxStreams,
		MaxTranscodes:            maxTranscodes,
		AllowedPermissions:       allowedPermissions,
		RequestsAllowed:          requestsAllowed,
		IsDefault:                r.IsDefault,
	}, true
}

func (r accessGroupUpdateRequest) toInput(w http.ResponseWriter) (access.UpdateGroupInput, bool) {
	if r.Name != nil {
		name := strings.TrimSpace(*r.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "name is required")
			return access.UpdateGroupInput{}, false
		}
		r.Name = &name
	}
	var maxPlaybackQuality *string
	if r.MaxPlaybackQuality != nil {
		normalized, ok := normalizeAccessGroupQuality(w, *r.MaxPlaybackQuality)
		if !ok {
			return access.UpdateGroupInput{}, false
		}
		maxPlaybackQuality = &normalized
	}
	if r.LibraryIDs.Set && !validateAccessGroupIDs(w, r.LibraryIDs.Value) {
		return access.UpdateGroupInput{}, false
	}
	if r.MaxStreams != nil && *r.MaxStreams < 0 || r.MaxTranscodes != nil && *r.MaxTranscodes < 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "stream limits must be non-negative")
		return access.UpdateGroupInput{}, false
	}
	var allowedPermissions *[]string
	if r.AllowedPermissions.Set {
		normalized, ok := normalizeAccessGroupPermissions(w, r.AllowedPermissions)
		if !ok {
			return access.UpdateGroupInput{}, false
		}
		allowedPermissions = &normalized
	}
	return access.UpdateGroupInput{
		Name:                     r.Name,
		Description:              r.Description,
		LibraryIDs:               r.LibraryIDs.Ptr(),
		MaxPlaybackQuality:       maxPlaybackQuality,
		DownloadAllowed:          r.DownloadAllowed,
		DownloadTranscodeAllowed: r.DownloadTranscodeAllowed,
		TranscodeAllowed:         r.TranscodeAllowed,
		AudioTranscodeAllowed:    r.AudioTranscodeAllowed,
		MaxStreams:               r.MaxStreams,
		MaxTranscodes:            r.MaxTranscodes,
		AllowedPermissions:       allowedPermissions,
		RequestsAllowed:          r.RequestsAllowed,
		IsDefault:                r.IsDefault,
	}, true
}

func (f accessGroupIntSliceField) Ptr() *[]int {
	if !f.Set {
		return nil
	}
	return new(slices.Clone(f.Value))
}

func normalizeAccessGroupQuality(w http.ResponseWriter, raw string) (string, bool) {
	normalized, ok := access.ParsePlaybackQualityPreset(raw)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid max_playback_quality")
		return "", false
	}
	return normalized, true
}

func normalizeAccessGroupPermissions(w http.ResponseWriter, field accessGroupStringSliceField) ([]string, bool) {
	if !field.Set || field.Value == nil {
		return nil, true
	}
	normalized, err := auth.NormalizePermissions(field.Value)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return nil, false
	}
	return normalized, true
}

func validateAccessGroupIDs(w http.ResponseWriter, ids []int) bool {
	for _, id := range ids {
		if id <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "library_ids must contain positive integers")
			return false
		}
	}
	return true
}
