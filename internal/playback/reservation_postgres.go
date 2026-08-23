package playback

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresReservationStore struct {
	pool *pgxpool.Pool
}

func NewPostgresReservationStore(pool *pgxpool.Pool) *PostgresReservationStore {
	return &PostgresReservationStore{pool: pool}
}

func (store *PostgresReservationStore) Acquire(ctx context.Context, request ReservationRequest) (Reservation, error) {
	now := time.Now()
	if store == nil || store.pool == nil || !request.valid(now) {
		return Reservation{}, ErrReservationInvalid
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Reservation{}, fmt.Errorf("begin playback reservation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Every contender takes its account lock before the optional tenant lock.
	// The common order prevents deadlocks while the tenant lock serializes
	// accounts drawing from one shared organization pool.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, fmt.Sprintf("playback-account:%d", request.AccountID)); err != nil {
		return Reservation{}, fmt.Errorf("lock playback account capacity: %w", err)
	}
	if request.TenantID != "" {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "playback-tenant:"+request.TenantID); err != nil {
			return Reservation{}, fmt.Errorf("lock playback tenant capacity: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM playback_capacity_reservations WHERE lease_until <= now()`); err != nil {
		return Reservation{}, fmt.Errorf("expire playback reservations: %w", err)
	}

	var streams, transcodes int
	if err := tx.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE is_transcode)
		FROM playback_capacity_reservations
		WHERE account_id = $1 AND session_id <> $2 AND lease_until > now()`,
		request.AccountID, request.SessionID,
	).Scan(&streams, &transcodes); err != nil {
		return Reservation{}, fmt.Errorf("count account playback reservations: %w", err)
	}
	if request.AccountStreams > 0 && streams >= request.AccountStreams {
		return Reservation{}, ErrTooManyStreams
	}
	if request.IsTranscode && request.AccountTranscodes > 0 && transcodes >= request.AccountTranscodes {
		return Reservation{}, ErrTooManyTranscodes
	}
	if request.IsTranscode && request.TenantID != "" && request.TenantTranscodes > 0 {
		var tenantTranscodes int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM playback_capacity_reservations
			WHERE tenant_id = $1 AND is_transcode AND session_id <> $2 AND lease_until > now()`,
			request.TenantID, request.SessionID,
		).Scan(&tenantTranscodes); err != nil {
			return Reservation{}, fmt.Errorf("count tenant playback reservations: %w", err)
		}
		if tenantTranscodes >= request.TenantTranscodes {
			return Reservation{}, ErrTenantTranscodesExceeded
		}
	}

	var generation int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO playback_capacity_reservations (
			session_id, account_id, profile_id, tenant_id, is_transcode, lease_until, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (session_id) DO UPDATE SET
			generation = nextval('playback_capacity_reservation_generation_seq'),
			account_id = EXCLUDED.account_id,
			profile_id = EXCLUDED.profile_id,
			tenant_id = EXCLUDED.tenant_id,
			is_transcode = EXCLUDED.is_transcode,
			lease_until = EXCLUDED.lease_until,
			updated_at = now()
		RETURNING generation`,
		request.SessionID, request.AccountID, request.ProfileID, request.TenantID, request.IsTranscode, request.LeaseUntil,
	).Scan(&generation); err != nil {
		return Reservation{}, fmt.Errorf("store playback reservation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Reservation{}, fmt.Errorf("commit playback reservation: %w", err)
	}
	return Reservation{SessionID: request.SessionID, Generation: generation, LeaseUntil: request.LeaseUntil}, nil
}

func (store *PostgresReservationStore) Renew(ctx context.Context, sessionID string, generation int64, leaseUntil time.Time) (Reservation, error) {
	if store == nil || store.pool == nil || sessionID == "" || generation <= 0 || !leaseUntil.After(time.Now()) {
		return Reservation{}, ErrReservationInvalid
	}
	var storedGeneration int64
	err := store.pool.QueryRow(ctx, `
		UPDATE playback_capacity_reservations
		SET lease_until = $3, updated_at = now()
		WHERE session_id = $1 AND generation = $2 AND lease_until > now()
		RETURNING generation`, sessionID, generation, leaseUntil,
	).Scan(&storedGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, ErrReservationGenerationMismatch
	}
	if err != nil {
		return Reservation{}, fmt.Errorf("renew playback reservation: %w", err)
	}
	return Reservation{SessionID: sessionID, Generation: storedGeneration, LeaseUntil: leaseUntil}, nil
}

func (store *PostgresReservationStore) Release(ctx context.Context, sessionID string, generation int64) error {
	if store == nil || store.pool == nil || sessionID == "" || generation <= 0 {
		return ErrReservationInvalid
	}
	result, err := store.pool.Exec(ctx, `
		DELETE FROM playback_capacity_reservations
		WHERE session_id = $1 AND generation = $2`, sessionID, generation)
	if err != nil {
		return fmt.Errorf("release playback reservation: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrReservationGenerationMismatch
	}
	return nil
}
