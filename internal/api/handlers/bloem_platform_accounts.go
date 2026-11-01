package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/auth/passwordhash"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/models"
)

// Creating and updating an account on a Bloem plan, for a storefront
// (POST and PUT /api/bloem/v1/admin/platform/accounts). Silo's
// /api/v2/admin/users takes no plan, so these routes sit on top of the same
// Silo account service that route uses (CreateAdminAccount, UpdateAdminAccount,
// GetAdminAccount and DeleteAdminAccount in admin_users_service.go) and add one
// Bloem step: putting the account on an exact plan revision. Nothing here is
// Silo's /api/v1 code, so the routes keep working when Silo retires /api/v1.

const bloemPlatformAccountBodyLimit = 64 << 10

// BloemPlatformAccountCreateRequest is the create body. Unknown fields are
// ignored, so a storefront that still sends the old /api/v1 body is accepted.
type BloemPlatformAccountCreateRequest struct {
	Username                    string   `json:"username" doc:"Sign-in name."`
	Email                       string   `json:"email" doc:"Email address."`
	Password                    string   `json:"password" doc:"Initial password."`
	Role                        string   `json:"role,omitempty" doc:"user (the default) or admin."`
	Enabled                     *bool    `json:"enabled,omitempty" doc:"A new account is enabled; false disables it straight after it is created."`
	RequirePasswordChange       bool     `json:"require_password_change,omitempty" doc:"The account must replace its password at first sign-in."`
	Permissions                 []string `json:"permissions,omitempty"`
	LibraryIDs                  []int    `json:"library_ids,omitempty" doc:"Library IDs; null or omitted means every library."`
	AccessGroupID               *int64   `json:"access_group_id,omitempty"`
	MaxProfiles                 *int     `json:"max_profiles,omitempty"`
	MaxPlaybackQuality          *string  `json:"max_playback_quality,omitempty"`
	MaxStreams                  *int     `json:"max_streams,omitempty"`
	MaxTranscodes               *int     `json:"max_transcodes,omitempty"`
	TranscodeAllowed            *bool    `json:"transcode_allowed,omitempty"`
	AudioTranscodeAllowed       *bool    `json:"audio_transcode_allowed,omitempty"`
	DownloadAllowed             *bool    `json:"download_allowed,omitempty"`
	DownloadTranscodeAllowed    *bool    `json:"download_transcode_allowed,omitempty"`
	RequestsAllowed             *bool    `json:"requests_allowed,omitempty"`
	CreateDefaultProfile        bool     `json:"create_default_profile,omitempty"`
	DefaultProfileName          string   `json:"default_profile_name,omitempty"`
	EntitlementTemplateKey      string   `json:"entitlement_template_key,omitempty" doc:"With entitlement_template_revision, the plan to put the account on."`
	EntitlementTemplateRevision int64    `json:"entitlement_template_revision,omitempty" doc:"The exact plan revision."`
}

// BloemPlatformAccountUpdateRequest is the update body: a field left out is
// not changed, and a nullable policy field set to null clears it.
type BloemPlatformAccountUpdateRequest struct {
	Username                    *string                    `json:"username"`
	Email                       *string                    `json:"email"`
	Password                    *string                    `json:"password"`
	Role                        *string                    `json:"role"`
	Enabled                     *bool                      `json:"enabled"`
	Permissions                 *[]string                  `json:"permissions"`
	LibraryIDs                  bloemOptionalField[[]int]  `json:"library_ids"`
	AccessGroupID               bloemOptionalField[int64]  `json:"access_group_id"`
	MaxProfiles                 bloemOptionalField[int]    `json:"max_profiles"`
	MaxPlaybackQuality          bloemOptionalField[string] `json:"max_playback_quality"`
	MaxStreams                  bloemOptionalField[int]    `json:"max_streams"`
	MaxTranscodes               bloemOptionalField[int]    `json:"max_transcodes"`
	TranscodeAllowed            bloemOptionalField[bool]   `json:"transcode_allowed"`
	AudioTranscodeAllowed       bloemOptionalField[bool]   `json:"audio_transcode_allowed"`
	DownloadAllowed             bloemOptionalField[bool]   `json:"download_allowed"`
	DownloadTranscodeAllowed    bloemOptionalField[bool]   `json:"download_transcode_allowed"`
	RequestsAllowed             bloemOptionalField[bool]   `json:"requests_allowed"`
	EntitlementTemplateKey      string                     `json:"entitlement_template_key"`
	EntitlementTemplateRevision int64                      `json:"entitlement_template_revision"`
}

// bloemOptionalField decodes a nullable policy field into Silo's tri-state
// models.Optional: absent leaves it, null clears it, a value sets it.
type bloemOptionalField[T any] struct{ models.Optional[T] }

func (o *bloemOptionalField[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if strings.TrimSpace(string(data)) == "null" {
		o.Value = nil
		return nil
	}
	value := new(T)
	if err := json.Unmarshal(data, value); err != nil {
		return err
	}
	o.Value = value
	return nil
}

// HandleBloemPlatformAccountCreate serves POST /api/bloem/v1/admin/platform/accounts.
func (h *AdminHandler) HandleBloemPlatformAccountCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req BloemPlatformAccountCreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, bloemPlatformAccountBodyLimit)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	planned, ok := h.bloemPlatformPlanRequested(w, &req.EntitlementTemplateKey, req.EntitlementTemplateRevision)
	if !ok {
		return
	}
	role := req.Role
	if role == "" {
		role = models.RoleUser
	}
	input := auth.CreateAccountInput{
		User: models.CreateUserInput{
			Username: req.Username, Email: req.Email, Password: req.Password,
			PasswordChangeRequired: req.RequirePasswordChange, Role: role, Permissions: req.Permissions,
			LibraryIDs: req.LibraryIDs, AccessGroupID: req.AccessGroupID, MaxProfiles: req.MaxProfiles,
			MaxPlaybackQuality: req.MaxPlaybackQuality, MaxStreams: req.MaxStreams, MaxTranscodes: req.MaxTranscodes,
			TranscodeAllowed: req.TranscodeAllowed, AudioTranscodeAllowed: req.AudioTranscodeAllowed,
			DownloadAllowed: req.DownloadAllowed, DownloadTranscodeAllowed: req.DownloadTranscodeAllowed,
			RequestsAllowed: req.RequestsAllowed,
		},
		// On a plan the default profile follows the plan, as on /api/v1.
		DefaultProfile: auth.DefaultProfileOptions{Enabled: req.CreateDefaultProfile && !planned, Name: req.DefaultProfileName},
	}
	id, err := h.CreateAdminAccount(ctx, input)
	if err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	var applied int64
	if planned {
		if applied, ok = h.putNewBloemPlatformAccountOnPlan(w, r, id, req, input); !ok {
			return
		}
	}
	if req.Enabled != nil && !*req.Enabled {
		if _, err := h.UpdateAdminAccount(ctx, id, -1, 0, models.UpdateUserInput{Enabled: req.Enabled}); err != nil {
			h.discardBloemPlatformAccount(ctx, id)
			writeBloemPlatformAccountError(w, err)
			return
		}
	}
	h.writeBloemPlatformAccount(w, ctx, id, applied, http.StatusCreated)
}

// putNewBloemPlatformAccountOnPlan puts an account this request created on its
// plan, then creates its default profile if asked. On failure it deletes the
// account again and writes the response.
func (h *AdminHandler) putNewBloemPlatformAccountOnPlan(w http.ResponseWriter, r *http.Request, id int, req BloemPlatformAccountCreateRequest, input auth.CreateAccountInput) (int64, bool) {
	ctx := r.Context()
	result, err := h.directEntitlements.ApplyDefaultAccountTemplate(ctx, requestAccountID(r), id, req.EntitlementTemplateKey, req.EntitlementTemplateRevision, false)
	if err != nil {
		h.discardBloemPlatformAccount(ctx, id)
		writeBloemPlatformPlanError(w, err)
		return 0, false
	}
	if req.CreateDefaultProfile {
		input.DefaultProfile.Enabled = true
		if err := h.accountProvisioner.CreateDefaultProfile(ctx, id, input); err != nil {
			h.discardBloemPlatformAccount(ctx, id)
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create default profile")
			return 0, false
		}
	}
	return result.TemplateRevision, true
}

// HandleBloemPlatformAccountUpdate serves PUT /api/bloem/v1/admin/platform/accounts/{account_id}.
func (h *AdminHandler) HandleBloemPlatformAccountUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "account_id")))
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid account ID")
		return
	}
	var req BloemPlatformAccountUpdateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, bloemPlatformAccountBodyLimit)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	planned, ok := h.bloemPlatformPlanRequested(w, &req.EntitlementTemplateKey, req.EntitlementTemplateRevision)
	if !ok {
		return
	}
	input := models.UpdateUserInput{
		Username: req.Username, Email: req.Email, Password: req.Password, Role: req.Role,
		Permissions: req.Permissions, Enabled: req.Enabled,
		LibraryIDs: req.LibraryIDs.Optional, AccessGroupID: req.AccessGroupID.Optional,
		MaxProfiles: req.MaxProfiles.Optional, MaxPlaybackQuality: req.MaxPlaybackQuality.Optional,
		MaxStreams: req.MaxStreams.Optional, MaxTranscodes: req.MaxTranscodes.Optional,
		TranscodeAllowed: req.TranscodeAllowed.Optional, AudioTranscodeAllowed: req.AudioTranscodeAllowed.Optional,
		DownloadAllowed: req.DownloadAllowed.Optional, DownloadTranscodeAllowed: req.DownloadTranscodeAllowed.Optional,
		RequestsAllowed: req.RequestsAllowed.Optional,
	}
	// Revision -1: the storefront states the account it wants, as /api/v1
	// let it, rather than holding Silo's optimistic-concurrency token.
	if _, err := h.UpdateAdminAccount(ctx, id, -1, 0, input); err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	var applied int64
	if planned {
		result, err := h.directEntitlements.ApplyDefaultAccountTemplate(ctx, requestAccountID(r), id, req.EntitlementTemplateKey, req.EntitlementTemplateRevision, false)
		if err != nil {
			writeBloemPlatformPlanError(w, err)
			return
		}
		applied = result.TemplateRevision
	}
	h.writeBloemPlatformAccount(w, ctx, id, applied, http.StatusOK)
}

// bloemPlatformPlanRequested validates the plan fields: both or neither.
func (h *AdminHandler) bloemPlatformPlanRequested(w http.ResponseWriter, key *string, revision int64) (bool, bool) {
	*key = strings.TrimSpace(*key)
	planned := *key != "" || revision != 0
	if planned && (*key == "" || revision <= 0) {
		writeError(w, http.StatusBadRequest, "bad_request", "entitlement_template_key and a positive entitlement_template_revision are required together")
		return false, false
	}
	if planned && h.directEntitlements == nil {
		writeError(w, http.StatusServiceUnavailable, "entitlements_unavailable", "Entitlement templates are not configured")
		return false, false
	}
	return planned, true
}

// writeBloemPlatformAccount answers with the account as Silo's account service
// reads it, plus the plan revision applied (zero omits it).
func (h *AdminHandler) writeBloemPlatformAccount(w http.ResponseWriter, ctx context.Context, id int, applied int64, status int) {
	view, err := h.GetAdminAccount(ctx, id)
	if err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	writeJSON(w, status, withAppliedEntitlementRevision(view.User, applied))
}

// discardBloemPlatformAccount deletes an account created by this request whose
// plan could not be applied, so a retry can create it again.
func (h *AdminHandler) discardBloemPlatformAccount(ctx context.Context, id int) {
	ctx = context.WithoutCancel(ctx)
	view, err := h.GetAdminAccount(ctx, id)
	if err != nil {
		return
	}
	_ = h.DeleteAdminAccount(ctx, id, view.Revision, view.GroupRevision)
}

func writeBloemPlatformAccountError(w http.ResponseWriter, err error) {
	var apiErr *APIError
	switch {
	case errors.As(err, &apiErr):
		writeAPIError(w, apiErr)
	case errors.Is(err, auth.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Account not found")
	case errors.Is(err, auth.ErrDuplicate):
		writeError(w, http.StatusConflict, "duplicate", "A user with that username or email already exists")
	case errors.Is(err, auth.ErrTransactionalProfileUnavailable):
		writeError(w, http.StatusNotImplemented, "capability_unsupported", "The profile store cannot provision atomically")
	case errors.Is(err, passwordhash.ErrBusy):
		writeAPIError(w, busyError(err))
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Account operation failed")
	}
}

func writeBloemPlatformPlanError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, entitlements.ErrTemplateNotFound), errors.Is(err, entitlements.ErrTemplateUnavailable):
		writeError(w, http.StatusUnprocessableEntity, "entitlement_template_unavailable", "Entitlement template revision is unavailable")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to apply entitlement template")
	}
}
