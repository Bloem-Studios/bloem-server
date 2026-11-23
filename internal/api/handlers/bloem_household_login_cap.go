package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// A household's login cap (Bloem household logins): the most logins a main
// account may have, set by the storefront beside the household's limits (PUT,
// GET and DELETE /api/bloem/v2/admin/platform/accounts/{account_id}/household-login-cap).
// A new login, made fresh or moved from a profile, is refused with
// household_login_cap_reached once the household has that many. Lowering the
// cap below the logins a household has leaves them working and blocks new
// ones; the view reports the household over its cap. Moving a login back to a
// profile is never refused. No cap is no limit beyond the household's places.
// See docs/architecture/bloem-household-logins.md.

const bloemHouseholdLoginCapBodyLimit = 4 << 10

// BloemHouseholdLoginCapRequest sets a household's login cap.
type BloemHouseholdLoginCapRequest struct {
	MaxLogins *int `json:"max_logins" required:"true" doc:"The most household logins the main account may have, made fresh or from a profile; 0 allows none."`
}

// BloemHouseholdLoginCapView is a household's login cap and its logins now.
type BloemHouseholdLoginCapView struct {
	AccountID int  `json:"account_id" doc:"The main account."`
	MaxLogins int  `json:"max_logins" doc:"The most household logins the main account may have; 0 allows none."`
	Logins    int  `json:"logins" doc:"The main account's household logins now."`
	OverCap   bool `json:"over_cap" doc:"The household has more logins than its cap: they keep working, and no new one can be made until it is back under."`
}

// HandleBloemPlatformHouseholdLoginCapPut serves
// PUT /api/bloem/v2/admin/platform/accounts/{account_id}/household-login-cap.
func (h *AdminHandler) HandleBloemPlatformHouseholdLoginCapPut(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mainID, ok := bloemHouseholdMainID(w, r)
	if !ok {
		return
	}
	var req BloemHouseholdLoginCapRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, bloemHouseholdLoginCapBodyLimit)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	if req.MaxLogins == nil || *req.MaxLogins < 0 {
		writeBloemPlatformAccountError(w, fieldError("max_logins", "max_logins is required: 0 or more"))
		return
	}
	view, err := h.setBloemHouseholdLoginCap(ctx, mainID, *req.MaxLogins)
	if err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// setBloemHouseholdLoginCap writes the cap with the main account locked
// against a concurrent delete, and only on an account that may have logins.
func (h *AdminHandler) setBloemHouseholdLoginCap(ctx context.Context, mainID, maxLogins int) (BloemHouseholdLoginCapView, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return BloemHouseholdLoginCapView{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := lockBloemHouseholdMain(ctx, tx, mainID); err != nil {
		return BloemHouseholdLoginCapView{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO bloem_household_login_caps (main_user_id, max_logins) VALUES ($1, $2)
		ON CONFLICT (main_user_id) DO UPDATE SET max_logins = EXCLUDED.max_logins, updated_at = now()`,
		mainID, maxLogins); err != nil {
		return BloemHouseholdLoginCapView{}, err
	}
	view, err := readBloemHouseholdLoginCap(ctx, tx, mainID)
	if err != nil {
		return BloemHouseholdLoginCapView{}, err
	}
	return view, tx.Commit(ctx)
}

// HandleBloemPlatformHouseholdLoginCapGet serves
// GET /api/bloem/v2/admin/platform/accounts/{account_id}/household-login-cap.
func (h *AdminHandler) HandleBloemPlatformHouseholdLoginCapGet(w http.ResponseWriter, r *http.Request) {
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
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	view, err := readBloemHouseholdLoginCap(ctx, tx, mainID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "household_login_cap_not_found", "This account has no login cap")
		return
	}
	if err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// HandleBloemPlatformHouseholdLoginCapDelete serves
// DELETE /api/bloem/v2/admin/platform/accounts/{account_id}/household-login-cap.
// Deleting a cap that is not there succeeds, so a repeated request is safe.
func (h *AdminHandler) HandleBloemPlatformHouseholdLoginCapDelete(w http.ResponseWriter, r *http.Request) {
	mainID, ok := bloemHouseholdMainID(w, r)
	if !ok {
		return
	}
	if _, err := h.pool.Exec(r.Context(), `DELETE FROM bloem_household_login_caps WHERE main_user_id = $1`, mainID); err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readBloemHouseholdLoginCap(ctx context.Context, tx pgx.Tx, mainID int) (BloemHouseholdLoginCapView, error) {
	view := BloemHouseholdLoginCapView{AccountID: mainID}
	if err := tx.QueryRow(ctx, `
		SELECT cap.max_logins, (SELECT count(*) FROM users WHERE household_main_user_id = cap.main_user_id)
		FROM bloem_household_login_caps AS cap WHERE cap.main_user_id = $1`, mainID).Scan(&view.MaxLogins, &view.Logins); err != nil {
		return BloemHouseholdLoginCapView{}, err
	}
	view.OverCap = view.Logins > view.MaxLogins
	return view, nil
}

// checkBloemHouseholdLoginCap refuses a new login once the household has as
// many as its cap allows. It locks the cap row, so two logins made at once
// under a cap are counted one after the other; without a cap it allows the
// login and locks nothing. Callers hold the main account (lockBloemHouseholdMain).
func checkBloemHouseholdLoginCap(ctx context.Context, tx pgx.Tx, mainID int) error {
	var maxLogins int
	err := tx.QueryRow(ctx, `SELECT max_logins FROM bloem_household_login_caps WHERE main_user_id = $1 FOR UPDATE`, mainID).Scan(&maxLogins)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var logins int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE household_main_user_id = $1`, mainID).Scan(&logins); err != nil {
		return err
	}
	if logins >= maxLogins {
		return apiError(http.StatusConflict, "household_login_cap_reached",
			fmt.Sprintf("This household's plan allows %d separate logins and it has %d, so it cannot have another", maxLogins, logins))
	}
	return nil
}
