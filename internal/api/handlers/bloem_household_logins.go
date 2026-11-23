package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/auditmutation"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/cache"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// Household logins (Bloem): an account of its own that belongs to a
// customer's main account. It signs in with its own username and password,
// has its own profiles, devices and sessions, sits in the main account's
// access group and organization with its own limits on top, follows the main
// account's enabled state (internal/auth/bloem_household.go) and is deleted
// with it. A storefront creates and lists them here
// (POST and GET /api/bloem/v2/admin/platform/accounts/{account_id}/logins);
// every other operation on a login is the ordinary account operation on the
// login's own ID. See docs/architecture/bloem-household-logins.md.

const bloemHouseholdLoginBodyLimit = 16 << 10

// BloemHouseholdLoginCreateRequest is the body of creating a login.
type BloemHouseholdLoginCreateRequest struct {
	Username         string `json:"username" doc:"Sign-in name of the login."`
	Email            string `json:"email,omitempty" doc:"Email address; omitted, the login has none and its password is reset by an administrator."`
	Password         string `json:"password" doc:"Initial password, 8 to 72 bytes."`
	ProfileName      string `json:"profile_name" doc:"Name of the login's first profile."`
	MaxStreams       int    `json:"max_streams" doc:"Concurrent streams the login may use; at least 1."`
	MaxTranscodes    *int   `json:"max_transcodes,omitempty" doc:"Concurrent transcodes the login may run. 0 is unlimited, so a login without transcodes sends transcode_allowed false instead."`
	TranscodeAllowed *bool  `json:"transcode_allowed,omitempty" doc:"Whether the login may transcode; omitted follows the plan."`
	MaxProfiles      int    `json:"max_profiles" doc:"Profiles the login may have; at least 1."`
	// RequirePasswordChange makes Password temporary (Bloem,
	// household_login_credentials_v1): the person chooses their own at their
	// first sign-in, before they can do anything else.
	RequirePasswordChange bool `json:"require_password_change,omitempty" doc:"Make the password temporary: the login must choose a new one at its first sign-in before it can do anything else."`
}

// BloemHouseholdLoginView is a login as the account service reads it, with its
// household link.
type BloemHouseholdLoginView struct {
	AdminUserView
	HouseholdMainUserID int  `json:"household_main_user_id" doc:"The main account this login belongs to."`
	HouseholdDisabled   bool `json:"household_disabled" doc:"The login is disabled because its main account is."`
}

// BloemHouseholdLoginSummary is one login of a main account.
type BloemHouseholdLoginSummary struct {
	ID                int    `json:"id"`
	Username          string `json:"username"`
	Enabled           bool   `json:"enabled"`
	HouseholdDisabled bool   `json:"household_disabled" doc:"Disabled because the main account is; it comes back with the main account."`
}

// BloemHouseholdLoginListResponse lists a main account's logins, oldest first.
type BloemHouseholdLoginListResponse struct {
	Logins []BloemHouseholdLoginSummary `json:"logins"`
}

// HandleBloemPlatformHouseholdLoginCreate serves
// POST /api/bloem/v2/admin/platform/accounts/{account_id}/logins.
func (h *AdminHandler) HandleBloemPlatformHouseholdLoginCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mainID, ok := bloemHouseholdMainID(w, r)
	if !ok {
		return
	}
	var req BloemHouseholdLoginCreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, bloemHouseholdLoginBodyLimit)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	req.Username = auth.NormalizeUsername(req.Username)
	req.Email = auth.NormalizeEmail(req.Email)
	req.ProfileName = strings.TrimSpace(req.ProfileName)
	if err := validateBloemHouseholdLogin(req); err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	loginID, mainEnabled, err := h.createBloemHouseholdLogin(ctx, mainID, req)
	if err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	view, err := h.GetAdminAccount(ctx, loginID)
	if err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, BloemHouseholdLoginView{AdminUserView: view.User, HouseholdMainUserID: mainID, HouseholdDisabled: !mainEnabled})
}

func validateBloemHouseholdLogin(req BloemHouseholdLoginCreateRequest) error {
	switch {
	case req.Username == "":
		return fieldError("username", "Username is required")
	case req.ProfileName == "":
		return fieldError("profile_name", "Profile name is required")
	case req.MaxStreams < 1:
		return fieldError("max_streams", "A login needs at least 1 stream")
	case req.MaxProfiles < 1:
		return fieldError("max_profiles", "A login needs at least 1 profile")
	case req.MaxTranscodes != nil && *req.MaxTranscodes < 0:
		return fieldError("max_transcodes", "Must be 0 (unlimited) or positive")
	}
	return validateAdminPassword(req.Password)
}

// createBloemHouseholdLogin creates the login, its profile and its household
// link in one transaction, so no login exists without its link. It reports
// whether the main account is enabled: a login created under a disabled main
// account starts disabled and comes back with it.
func (h *AdminHandler) createBloemHouseholdLogin(ctx context.Context, mainID int, req BloemHouseholdLoginCreateRequest) (int, bool, error) {
	if ready, transactional := h.AdminAccountCapabilities(); !ready || !transactional {
		return 0, false, apiError(http.StatusNotImplemented, "capability_unsupported", "Transactional account management is unavailable")
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `LOCK TABLE access_groups IN SHARE MODE`); err != nil {
		return 0, false, err
	}
	main, err := lockBloemHouseholdMain(ctx, tx, mainID)
	if err != nil {
		return 0, false, err
	}
	if err := checkBloemHouseholdLoginCap(ctx, tx, mainID); err != nil {
		return 0, false, err
	}
	if err := checkBloemHouseholdLoginEmail(ctx, tx, req.Email); err != nil {
		return 0, false, err
	}
	input := auth.CreateAccountInput{
		User: models.CreateUserInput{
			Username: req.Username, Email: req.Email, Password: req.Password, Role: models.RoleUser,
			AccessGroupID: main.accessGroupID, MaxStreams: &req.MaxStreams, MaxTranscodes: req.MaxTranscodes,
			TranscodeAllowed: req.TranscodeAllowed, MaxProfiles: &req.MaxProfiles,
			PasswordChangeRequired: req.RequirePasswordChange,
		},
		DefaultProfile: auth.DefaultProfileOptions{Enabled: true, Name: req.ProfileName},
	}
	user, err := h.accountProvisioner.CreateAccountInTransaction(tenancy.WithCreationOrganization(ctx, main.organizationID), tx, input)
	if err != nil {
		return 0, false, err
	}
	// Account creation stores a missing email as an empty string, which only
	// one account can hold; a login without one has none.
	if _, err = tx.Exec(ctx, `UPDATE users SET household_main_user_id = $2, enabled = $3, household_disabled = NOT $3,
		email = NULLIF(email, '') WHERE id = $1`, user.ID, mainID, main.enabled); err != nil {
		return 0, false, err
	}
	audit, err := auditmutation.RecordUserMutation(ctx, tx, nil, user, true)
	if err != nil {
		return 0, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, false, err
	}
	auditmutation.CommitMutation(ctx, audit)
	h.invalidateStats(ctx, cache.ChannelAdmin, cache.EventAdminStatsInvalidated, strconv.Itoa(user.ID))
	return user.ID, main.enabled, nil
}

type bloemHouseholdMain struct {
	enabled        bool
	accessGroupID  *int64
	organizationID uuid.UUID
}

// lockBloemHouseholdMain reads the main account a login is created under,
// locked against a concurrent enable, disable or delete. Only an ordinary
// account that is not itself a login can have logins.
func lockBloemHouseholdMain(ctx context.Context, tx pgx.Tx, mainID int) (bloemHouseholdMain, error) {
	var main bloemHouseholdMain
	var role string
	var nested *int
	err := tx.QueryRow(ctx, `SELECT u.role, u.enabled, u.access_group_id, u.household_main_user_id, m.organization_id
		FROM users u JOIN organization_memberships m ON m.account_id = u.id
		WHERE u.id = $1 FOR SHARE OF u`, mainID).Scan(&role, &main.enabled, &main.accessGroupID, &nested, &main.organizationID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return main, auth.ErrNotFound
	case err != nil:
		return main, err
	case nested != nil:
		return main, apiError(http.StatusConflict, "household_nested", "A login cannot have logins of its own")
	case role != models.RoleUser:
		return main, apiError(http.StatusConflict, "household_main_ineligible", "Only an ordinary account can have logins")
	}
	return main, nil
}

// HandleBloemPlatformHouseholdLoginList serves
// GET /api/bloem/v2/admin/platform/accounts/{account_id}/logins.
func (h *AdminHandler) HandleBloemPlatformHouseholdLoginList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mainID, ok := bloemHouseholdMainID(w, r)
	if !ok {
		return
	}
	var exists bool
	if err := h.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, mainID).Scan(&exists); err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	if !exists {
		writeBloemPlatformAccountError(w, auth.ErrNotFound)
		return
	}
	rows, err := h.pool.Query(ctx, `SELECT id, COALESCE(username, ''), enabled, household_disabled FROM users
		WHERE household_main_user_id = $1 ORDER BY created_at, id`, mainID)
	if err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	logins, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (BloemHouseholdLoginSummary, error) {
		var login BloemHouseholdLoginSummary
		err := row.Scan(&login.ID, &login.Username, &login.Enabled, &login.HouseholdDisabled)
		return login, err
	})
	if err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	if logins == nil {
		logins = []BloemHouseholdLoginSummary{}
	}
	writeJSON(w, http.StatusOK, BloemHouseholdLoginListResponse{Logins: logins})
}

func bloemHouseholdMainID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "account_id")))
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid account ID")
		return 0, false
	}
	return id, true
}

// deleteBloemHouseholdLogins deletes a main account's logins through the
// account service before the main account itself is deleted, so each gets the
// cleanup a deleted account gets. A conditional delete checks the main
// account's revision first, so a stale request deletes nothing.
// deleteBloemHouseholdLoginsFirst deletes a main account's logins before
// Silo deletes the account. It never stops Silo's delete: a stale revision
// leaves the logins, and Silo's own revision check then refuses the delete;
// any other failure leaves logins whose foreign key refuses it.
func (h *AdminHandler) deleteBloemHouseholdLoginsFirst(ctx context.Context, mainID int, revision int64) {
	if err := h.deleteBloemHouseholdLogins(ctx, mainID, revision); err != nil && !errors.Is(err, auth.ErrAdminUserRevision) {
		slog.WarnContext(ctx, "delete household logins before their main account failed", "component", "api", "user_id", mainID, "error", err)
	}
}

func (h *AdminHandler) deleteBloemHouseholdLogins(ctx context.Context, mainID int, revision int64) error {
	if h.pool == nil {
		return nil
	}
	rows, err := h.pool.Query(ctx, `SELECT id FROM users WHERE household_main_user_id = $1 ORDER BY id`, mainID)
	if err != nil {
		return err
	}
	logins, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil || len(logins) == 0 {
		return err
	}
	if revision != -1 {
		view, err := h.GetAdminAccount(ctx, mainID)
		if err != nil {
			return err
		}
		if view.Revision != revision {
			return auth.ErrAdminUserRevision
		}
	}
	for _, login := range logins {
		if err := h.DeleteAdminAccount(ctx, login, -1, 0); err != nil && !errors.Is(err, auth.ErrNotFound) {
			return err
		}
	}
	return nil
}

// endBloemHouseholdLoginSessions ends the live connections of the logins an
// update switched off with their main account, as the update does for the
// main account itself.
func (h *AdminHandler) endBloemHouseholdLoginSessions(ctx context.Context, mainID int, input models.UpdateUserInput) {
	if input.Enabled == nil || *input.Enabled || h.OnUserSessionsRevoked == nil || h.pool == nil {
		return
	}
	rows, err := h.pool.Query(ctx, `SELECT id FROM users WHERE household_main_user_id = $1 AND household_disabled`, mainID)
	if err != nil {
		return
	}
	logins, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return
	}
	for _, login := range logins {
		h.OnUserSessionsRevoked(ctx, login)
	}
}

// bloemHouseholdUpdateError answers enabling a household login whose main
// account is disabled as the conflict it is: the login comes back with its
// main account.
func bloemHouseholdUpdateError(err error) error {
	if errors.Is(err, auth.ErrHouseholdMainDisabled) {
		return apiError(http.StatusConflict, "household_main_disabled", "This login's main account is disabled; the login comes back when the main account is enabled")
	}
	return err
}
