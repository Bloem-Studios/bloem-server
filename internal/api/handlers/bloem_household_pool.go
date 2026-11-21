package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// A household's shared pool (Bloem): the concurrent streams and transcodes a
// main account and its household logins draw on together while they play
// (internal/playback/bloem_household_reservation.go). A storefront sets it on
// the main account (PUT, GET and DELETE
// /api/bloem/v2/admin/platform/accounts/{account_id}/household-pool). It
// applies only while the main account has a login, so a household of one
// plays as it always has. See docs/architecture/bloem-household-logins.md.

const bloemHouseholdPoolBodyLimit = 4 << 10

// BloemHouseholdPoolRequest sets a household's pool. Both values are
// required: a missing one never quietly means unlimited.
type BloemHouseholdPoolRequest struct {
	MaxStreams    *int `json:"max_streams" required:"true" doc:"Concurrent streams the household's accounts may use together; 0 is unlimited."`
	MaxTranscodes *int `json:"max_transcodes" required:"true" doc:"Concurrent transcodes the household's accounts may run together; 0 is unlimited."`
}

// BloemHouseholdPoolView is a household's pool and what its accounts use now.
type BloemHouseholdPoolView struct {
	AccountID       int `json:"account_id" doc:"The main account."`
	MaxStreams      int `json:"max_streams" doc:"Concurrent streams the household's accounts may use together; 0 is unlimited."`
	MaxTranscodes   int `json:"max_transcodes" doc:"Concurrent transcodes the household's accounts may run together; 0 is unlimited."`
	StreamsInUse    int `json:"streams_in_use" doc:"Streams the main account and its logins hold now."`
	TranscodesInUse int `json:"transcodes_in_use" doc:"Of those, the ones transcoding."`
	Logins          int `json:"logins" doc:"The main account's household logins. The pool applies only while there is at least one."`
}

// HandleBloemPlatformHouseholdPoolPut serves
// PUT /api/bloem/v2/admin/platform/accounts/{account_id}/household-pool.
func (h *AdminHandler) HandleBloemPlatformHouseholdPoolPut(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mainID, ok := bloemHouseholdMainID(w, r)
	if !ok {
		return
	}
	var req BloemHouseholdPoolRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, bloemHouseholdPoolBodyLimit)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	switch {
	case req.MaxStreams == nil || *req.MaxStreams < 0:
		writeBloemPlatformAccountError(w, fieldError("max_streams", "max_streams is required: 0 (unlimited) or more"))
		return
	case req.MaxTranscodes == nil || *req.MaxTranscodes < 0:
		writeBloemPlatformAccountError(w, fieldError("max_transcodes", "max_transcodes is required: 0 (unlimited) or more"))
		return
	}
	view, err := h.setBloemHouseholdPool(ctx, mainID, *req.MaxStreams, *req.MaxTranscodes)
	if err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// setBloemHouseholdPool writes the pool with the main account locked against
// a concurrent delete, and only on an account that may have logins.
func (h *AdminHandler) setBloemHouseholdPool(ctx context.Context, mainID, streams, transcodes int) (BloemHouseholdPoolView, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return BloemHouseholdPoolView{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := lockBloemHouseholdMain(ctx, tx, mainID); err != nil {
		return BloemHouseholdPoolView{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO bloem_household_pools (main_user_id, max_streams, max_transcodes) VALUES ($1, $2, $3)
		ON CONFLICT (main_user_id) DO UPDATE SET
			max_streams = EXCLUDED.max_streams, max_transcodes = EXCLUDED.max_transcodes, updated_at = now()`,
		mainID, streams, transcodes); err != nil {
		return BloemHouseholdPoolView{}, err
	}
	view, err := readBloemHouseholdPool(ctx, tx, mainID)
	if err != nil {
		return BloemHouseholdPoolView{}, err
	}
	return view, tx.Commit(ctx)
}

// HandleBloemPlatformHouseholdPoolGet serves
// GET /api/bloem/v2/admin/platform/accounts/{account_id}/household-pool.
func (h *AdminHandler) HandleBloemPlatformHouseholdPoolGet(w http.ResponseWriter, r *http.Request) {
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
	view, err := readBloemHouseholdPool(ctx, tx, mainID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "household_pool_not_found", "This account has no household pool")
		return
	}
	if err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// HandleBloemPlatformHouseholdPoolDelete serves
// DELETE /api/bloem/v2/admin/platform/accounts/{account_id}/household-pool.
// Deleting a pool that is not there succeeds, so a repeated request is safe.
func (h *AdminHandler) HandleBloemPlatformHouseholdPoolDelete(w http.ResponseWriter, r *http.Request) {
	mainID, ok := bloemHouseholdMainID(w, r)
	if !ok {
		return
	}
	if _, err := h.pool.Exec(r.Context(), `DELETE FROM bloem_household_pools WHERE main_user_id = $1`, mainID); err != nil {
		writeBloemPlatformAccountError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readBloemHouseholdPool reads a pool and the live reservations of its main
// account and logins. pgx.ErrNoRows: the account has no pool.
func readBloemHouseholdPool(ctx context.Context, tx pgx.Tx, mainID int) (BloemHouseholdPoolView, error) {
	view := BloemHouseholdPoolView{AccountID: mainID}
	err := tx.QueryRow(ctx, `
		SELECT household.max_streams, household.max_transcodes,
		       (SELECT count(*) FROM users WHERE household_main_user_id = household.main_user_id),
		       usage.streams, usage.transcodes
		FROM bloem_household_pools AS household
		CROSS JOIN LATERAL (
			SELECT count(*) AS streams, count(*) FILTER (WHERE reservation.is_transcode) AS transcodes
			FROM playback_capacity_reservations AS reservation
			WHERE reservation.lease_until > now()
			  AND (reservation.account_id = household.main_user_id
			       OR reservation.account_id IN (SELECT id FROM users WHERE household_main_user_id = household.main_user_id))
		) AS usage
		WHERE household.main_user_id = $1`, mainID).
		Scan(&view.MaxStreams, &view.MaxTranscodes, &view.Logins, &view.StreamsInUse, &view.TranscodesInUse)
	return view, err
}
