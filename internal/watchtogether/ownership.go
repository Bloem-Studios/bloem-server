package watchtogether

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrRoomOwned                   = errors.New("watch together room has another owner")
	ErrRoomOwnerGenerationMismatch = errors.New("watch together room owner generation mismatch")
	ErrRoomOwnershipInvalid        = errors.New("watch together room ownership is invalid")
)

type Ownership struct {
	RoomID     string
	NodeID     string
	Generation int64
	LeaseUntil time.Time
}

type RoomOwner interface {
	Acquire(context.Context, string, string, time.Time) (Ownership, error)
	Renew(context.Context, Ownership, time.Time) (Ownership, error)
	Release(context.Context, Ownership) error
}

type PostgresRoomOwner struct {
	pool *pgxpool.Pool
}

func NewPostgresRoomOwner(pool *pgxpool.Pool) *PostgresRoomOwner {
	return &PostgresRoomOwner{pool: pool}
}

func (owner *PostgresRoomOwner) Acquire(ctx context.Context, roomID, nodeID string, leaseUntil time.Time) (Ownership, error) {
	if owner == nil || owner.pool == nil || roomID == "" || nodeID == "" || !leaseUntil.After(time.Now()) {
		return Ownership{}, ErrRoomOwnershipInvalid
	}
	tx, err := owner.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Ownership{}, fmt.Errorf("begin room ownership: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "watch-together-owner:"+roomID); err != nil {
		return Ownership{}, fmt.Errorf("lock room ownership: %w", err)
	}

	var currentNode string
	var currentGeneration int64
	var currentLease time.Time
	err = tx.QueryRow(ctx, `
		SELECT node_id, generation, lease_until
		FROM watch_together_room_owners WHERE room_id = $1`, roomID,
	).Scan(&currentNode, &currentGeneration, &currentLease)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Ownership{}, fmt.Errorf("read room ownership: %w", err)
	}
	if err == nil && currentLease.After(time.Now()) && currentNode != nodeID {
		return Ownership{}, ErrRoomOwned
	}

	var generation int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO watch_together_room_owners (room_id, node_id, lease_until, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (room_id) DO UPDATE SET
			node_id = EXCLUDED.node_id,
			generation = CASE
				WHEN watch_together_room_owners.node_id = EXCLUDED.node_id
				 AND watch_together_room_owners.lease_until > now()
				THEN watch_together_room_owners.generation
				ELSE nextval('watch_together_room_owner_generation_seq')
			END,
			lease_until = EXCLUDED.lease_until,
			updated_at = now()
		RETURNING generation`, roomID, nodeID, leaseUntil.UTC(),
	).Scan(&generation); err != nil {
		return Ownership{}, fmt.Errorf("store room ownership: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Ownership{}, fmt.Errorf("commit room ownership: %w", err)
	}
	return Ownership{RoomID: roomID, NodeID: nodeID, Generation: generation, LeaseUntil: leaseUntil.UTC()}, nil
}

func (owner *PostgresRoomOwner) Renew(ctx context.Context, ownership Ownership, leaseUntil time.Time) (Ownership, error) {
	if owner == nil || owner.pool == nil || ownership.RoomID == "" || ownership.NodeID == "" || ownership.Generation <= 0 || !leaseUntil.After(time.Now()) {
		return Ownership{}, ErrRoomOwnershipInvalid
	}
	var generation int64
	err := owner.pool.QueryRow(ctx, `
		UPDATE watch_together_room_owners
		SET lease_until = $4, updated_at = now()
		WHERE room_id = $1 AND node_id = $2 AND generation = $3 AND lease_until > now()
		RETURNING generation`, ownership.RoomID, ownership.NodeID, ownership.Generation, leaseUntil.UTC(),
	).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return Ownership{}, ErrRoomOwnerGenerationMismatch
	}
	if err != nil {
		return Ownership{}, fmt.Errorf("renew room ownership: %w", err)
	}
	return Ownership{RoomID: ownership.RoomID, NodeID: ownership.NodeID, Generation: generation, LeaseUntil: leaseUntil.UTC()}, nil
}

func (owner *PostgresRoomOwner) Release(ctx context.Context, ownership Ownership) error {
	if owner == nil || owner.pool == nil || ownership.RoomID == "" || ownership.NodeID == "" || ownership.Generation <= 0 {
		return ErrRoomOwnershipInvalid
	}
	result, err := owner.pool.Exec(ctx, `
		DELETE FROM watch_together_room_owners
		WHERE room_id = $1 AND node_id = $2 AND generation = $3`, ownership.RoomID, ownership.NodeID, ownership.Generation)
	if err != nil {
		return fmt.Errorf("release room ownership: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrRoomOwnerGenerationMismatch
	}
	return nil
}
