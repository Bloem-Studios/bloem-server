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
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/auditmutation"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/cache"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/sessioninvalidation"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// Bloem: moving a person between a profile of a main account and a household
// login of their own, keeping the profile with everything it holds
// (pgstore.MoveProfileInTx). Each move is one transaction.
// See docs/architecture/bloem-household-logins.md, "Moving a person".

// BloemHouseholdLoginMoveProfileRequest makes a login out of one of the main
// account's profiles.
type BloemHouseholdLoginMoveProfileRequest struct {
	ProfileID        string `json:"profile_id" doc:"The main account's profile that becomes the login's only profile, with its history, lists, settings and PIN. Not the main account's primary profile."`
	Username         string `json:"username" doc:"Sign-in name of the login."`
	Email            string `json:"email,omitempty" doc:"Email address; omitted, the login has none and its password is reset by an administrator."`
	Password         string `json:"password" doc:"Initial password, 8 to 72 bytes."`
	MaxStreams       int    `json:"max_streams" doc:"Concurrent streams the login may use; at least 1."`
	MaxTranscodes    *int   `json:"max_transcodes,omitempty" doc:"Concurrent transcodes the login may run. 0 is unlimited, so a login without transcodes sends transcode_allowed false instead."`
	TranscodeAllowed *bool  `json:"transcode_allowed,omitempty" doc:"Whether the login may transcode; omitted follows the plan."`
	MaxProfiles      int    `json:"max_profiles" doc:"Profiles the login may have; at least 1."`
}

// BloemHouseholdLoginMoveToMainResponse names the profiles that moved back
// onto the main account.
type BloemHouseholdLoginMoveToMainResponse struct {
	ProfileIDs []string `json:"profile_ids" doc:"The login's profiles, now the main account's, in the order they moved."`
}

// HandleBloemPlatformHouseholdLoginMoveProfile serves
// POST /api/bloem/v2/admin/platform/accounts/{account_id}/logins/move-profile.
func (h *AdminHandler) HandleBloemPlatformHouseholdLoginMoveProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mainID, ok := bloemHouseholdMainID(w, r)
	if !ok {
		return
	}
	var req BloemHouseholdLoginMoveProfileRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, bloemHouseholdLoginBodyLimit)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	req.ProfileID = strings.TrimSpace(req.ProfileID)
	create := BloemHouseholdLoginCreateRequest{
		Username: auth.NormalizeUsername(req.Username), Email: auth.NormalizeEmail(req.Email), Password: req.Password,
		ProfileName: req.ProfileID, MaxStreams: req.MaxStreams, MaxTranscodes: req.MaxTranscodes,
		TranscodeAllowed: req.TranscodeAllowed, MaxProfiles: req.MaxProfiles,
	}
	if req.ProfileID == "" {
		writeBloemPlatformAccountError(w, fieldError("profile_id", "Profile ID is required"))
		return
	}
	if err := validateBloemHouseholdLogin(create); err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	loginID, mainEnabled, err := h.moveBloemProfileToNewLogin(ctx, mainID, req.ProfileID, create)
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

// moveBloemProfileToNewLogin creates the login with no profile of its own,
// links it, and moves the profile into it, in one transaction, as
// createBloemHouseholdLogin does for a new person.
func (h *AdminHandler) moveBloemProfileToNewLogin(ctx context.Context, mainID int, profileID string, req BloemHouseholdLoginCreateRequest) (int, bool, error) {
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
	// A login's only profile is its primary, which manages the login's
	// profiles, its own included, and which Silo holds to no library limit;
	// so a profile with parental limits stays a profile.
	var primary, restricted bool
	err = tx.QueryRow(ctx, `SELECT is_primary, is_child OR library_restrictions_enabled OR max_content_rating <> ''
			OR max_advisory_age IS NOT NULL OR require_advisory_age OR requests_blocked
		FROM user_profiles WHERE user_id = $1 AND id = $2`, mainID, profileID).Scan(&primary, &restricted)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, false, bloemProfileMoveError(pgstore.ErrProfileMoveNotFound)
	case err != nil:
		return 0, false, err
	case primary:
		return 0, false, apiError(http.StatusConflict, "primary_profile", "The main account's primary profile manages the household and cannot have a login of its own")
	case restricted:
		return 0, false, apiError(http.StatusConflict, "restricted_profile", "A profile with parental limits cannot have a login of its own: as its login's main profile it would manage its own limits")
	}
	user, err := h.accountProvisioner.CreateAccountInTransaction(tenancy.WithCreationOrganization(ctx, main.organizationID), tx, auth.CreateAccountInput{
		User: models.CreateUserInput{
			Username: req.Username, Email: req.Email, Password: req.Password, Role: models.RoleUser,
			AccessGroupID: main.accessGroupID, MaxStreams: &req.MaxStreams, MaxTranscodes: req.MaxTranscodes,
			TranscodeAllowed: req.TranscodeAllowed, MaxProfiles: &req.MaxProfiles,
		},
	})
	if err != nil {
		return 0, false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET household_main_user_id = $2, enabled = $3, household_disabled = NOT $3,
		email = NULLIF(email, '') WHERE id = $1`, user.ID, mainID, main.enabled); err != nil {
		return 0, false, err
	}
	if err = pgstore.MoveProfileInTx(ctx, tx, mainID, user.ID, profileID, true); err != nil {
		return 0, false, bloemProfileMoveError(err)
	}
	audit, err := auditmutation.RecordUserMutation(ctx, tx, nil, user, true)
	if err != nil {
		return 0, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, false, err
	}
	auditmutation.CommitMutation(ctx, audit)
	// The move ended the main account's sign-ins as the profile in the
	// database; every replica also drops the Jellyfin-compatible ones it
	// holds in memory, as a profile's removal from an organization does. The
	// move itself has landed, so a failure here is logged, not answered.
	if h.OnUserProfileSessionsRevoked != nil {
		if err := sessioninvalidation.Run(ctx, func(invalidationCtx context.Context) error {
			return h.OnUserProfileSessionsRevoked(invalidationCtx, mainID, []string{profileID})
		}); err != nil {
			slog.WarnContext(ctx, "household login move: could not announce the profile's ended sign-ins", "account_id", mainID, "error", err)
		}
	}
	h.invalidateStats(ctx, cache.ChannelAdmin, cache.EventAdminStatsInvalidated, strconv.Itoa(user.ID))
	return user.ID, main.enabled, nil
}

// HandleBloemPlatformHouseholdLoginMoveToMain serves
// POST /api/bloem/v2/admin/platform/accounts/{account_id}/logins/{login_id}/move-to-main.
func (h *AdminHandler) HandleBloemPlatformHouseholdLoginMoveToMain(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mainID, ok := bloemHouseholdMainID(w, r)
	if !ok {
		return
	}
	loginID, err := strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "login_id")))
	if err != nil || loginID <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid login ID")
		return
	}
	moved, err := h.moveBloemLoginToMain(ctx, mainID, loginID)
	if err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, BloemHouseholdLoginMoveToMainResponse{ProfileIDs: moved})
}

// moveBloemLoginToMain moves every profile of the login onto the main
// account and deletes the login, inside the account service's own delete
// transaction, so the login's sign-ins are revoked and its deletion audited
// as any account's are.
func (h *AdminHandler) moveBloemLoginToMain(ctx context.Context, mainID, loginID int) ([]string, error) {
	repo, ok := h.userRepo.(adminAccountRepository)
	if !ok || h.pool == nil {
		return nil, apiError(http.StatusNotImplemented, "capability_unsupported", "Guarded account management is unavailable")
	}
	notLogin := apiError(http.StatusNotFound, "login_not_found", "This account has no such login")
	moved := []string{}
	_, err := repo.MutateAdminAccount(ctx, loginID, -1, nil, func(current *models.User, tx pgx.Tx) (bool, error) {
		if current.HouseholdMainUserID == nil || *current.HouseholdMainUserID != mainID {
			return false, notLogin
		}
		if _, err := lockBloemHouseholdMain(ctx, tx, mainID); err != nil {
			return false, err
		}
		// Silo keeps an account's profile names unique (case-insensitively);
		// two alike would make sign-in by profile name ambiguous.
		var clash bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM user_profiles AS moving JOIN user_profiles AS staying
				ON lower(btrim(staying.name)) = lower(btrim(moving.name))
			WHERE moving.user_id = $1 AND staying.user_id = $2)`, loginID, mainID).Scan(&clash); err != nil {
			return false, err
		}
		if clash {
			return false, apiError(http.StatusConflict, "profile_name_conflict", "The main account already has a profile with the name of one of the login's profiles; rename one first")
		}
		rows, err := tx.Query(ctx, `SELECT id FROM user_profiles WHERE user_id = $1 ORDER BY created_at, id`, loginID)
		if err != nil {
			return false, err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return false, err
		}
		for _, id := range ids {
			if err := pgstore.MoveProfileInTx(ctx, tx, loginID, mainID, id, false); err != nil {
				return false, bloemProfileMoveError(err)
			}
		}
		moved = ids
		return true, nil
	})
	if errors.Is(err, auth.ErrNotFound) {
		return nil, notLogin
	}
	if err != nil {
		return nil, err
	}
	if h.OnUserSessionsRevoked != nil {
		h.OnUserSessionsRevoked(ctx, loginID)
	}
	h.sweepWatchlistTitles(ctx, loginID)
	h.invalidateStats(ctx, cache.ChannelAdmin, cache.EventAdminStatsInvalidated, strconv.Itoa(loginID))
	return moved, nil
}

// bloemProfileMoveError answers a move's refusals as what they are.
func bloemProfileMoveError(err error) error {
	switch {
	case errors.Is(err, pgstore.ErrProfileMoveNotFound):
		return apiError(http.StatusNotFound, "profile_not_found", "The account has no such profile")
	case errors.Is(err, pgstore.ErrProfileMoveLimit):
		return apiError(http.StatusConflict, "profile_limit", "The account has no room for another profile")
	case errors.Is(err, pgstore.ErrProfileMoveConflict):
		return apiError(http.StatusConflict, "profile_conflict", "The account already has this profile or a primary profile")
	}
	return err
}
