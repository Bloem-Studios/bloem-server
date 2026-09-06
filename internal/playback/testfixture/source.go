// Package testfixture provisions isolated synthetic playback test data. It is
// not an account-enrollment API and is never called by ordinary playback starts.
package testfixture

import (
	"context"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SyntheticPlaybackSource struct {
	AccountID   int
	ProfileID   string
	Username    string
	Source      userstore.PlaybackSourceRef
	AdmissionID string
}

// ProvisionPostgres creates a fresh synthetic account and profile with an exact
// PG source in one transaction. It cannot select, overwrite or enroll an
// existing account. The caller owns cleanup of this isolated fixture account.
func ProvisionPostgres(ctx context.Context, pool *pgxpool.Pool) (SyntheticPlaybackSource, error) {
	var zero SyntheticPlaybackSource
	if pool == nil {
		return zero, userstore.ErrPlaybackSourceUnavailable
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	result := SyntheticPlaybackSource{Username: "synthetic-playback-" + uuid.NewString(), ProfileID: uuid.NewString(), AdmissionID: uuid.NewString()}
	if err := tx.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role,access_group_id) VALUES($1::text,$1::text||'@example.invalid','synthetic-disabled','user',(SELECT id FROM access_groups WHERE is_default)) RETURNING id`, result.Username).Scan(&result.AccountID); err != nil {
		return zero, err
	}
	if err := pgstore.NewPostgresProvider(pool).CreateProfileInTransaction(ctx, tx, result.AccountID, userstore.Profile{ID: result.ProfileID, Name: "Synthetic playback", IsPrimary: true}); err != nil {
		return zero, err
	}
	result.Source = userstore.PlaybackSourceRef{Backend: userstore.PlaybackSourcePostgres, AccountID: result.AccountID, SourceID: uuid.NewString(), SelectionGeneration: 1}
	// Match source writers' account gate even though this freshly inserted
	// account cannot yet be observed outside the transaction.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('playback-source:'||$1::integer::text,0))`, result.AccountID); err != nil {
		return zero, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO playback_source_markers(user_id,source_id,selection_generation,gate) VALUES($1,$2,1,'writable')`, result.AccountID, result.Source.SourceID); err != nil {
		return zero, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO playback_source_registrations(user_id,backend,source_id,selection_generation,admission_id,admission_state) VALUES($1,'postgres',$2,1,$3,'admitting')`, result.AccountID, result.Source.SourceID, result.AdmissionID); err != nil {
		return zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, err
	}
	return result, nil
}
