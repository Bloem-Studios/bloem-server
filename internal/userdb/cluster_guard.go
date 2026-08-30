package userdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/nodeidentity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	sqliteOwnerAdvisoryLock int64 = 0x564f4e44454c4442
	sqliteHeartbeatTTL            = 45 * time.Second
)

var (
	ErrSQLiteOwnershipInvalid   = errors.New("SQLite user database ownership is invalid")
	ErrSQLiteOwnedByAnotherNode = errors.New("SQLite user database is owned by another node")
	ErrSQLiteNodeAlreadyActive  = errors.New("SQLite user database node is already active")
)

// EnforceClusterSafeBackend reserves the local SQLite user-state backend for
// one durable node identity and one active process. PostgreSQL user state is
// shared and needs no local ownership claim.
func EnforceClusterSafeBackend(ctx context.Context, pool *pgxpool.Pool, backend, nodeID, nodeType string) error {
	if !strings.EqualFold(strings.TrimSpace(backend), "sqlite") {
		return nil
	}
	nodeID = strings.TrimSpace(nodeID)
	nodeType = strings.TrimSpace(nodeType)
	if ctx == nil || pool == nil || nodeID == "" || (nodeType != "integrated" && nodeType != "api") {
		return ErrSQLiteOwnershipInvalid
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin SQLite ownership claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, sqliteOwnerAdvisoryLock); err != nil {
		return fmt.Errorf("lock SQLite ownership claim: %w", err)
	}

	var ownerNode string
	err = tx.QueryRow(ctx, `SELECT node_id FROM userdb_sqlite_owner WHERE singleton = TRUE FOR UPDATE`).Scan(&ownerNode)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `INSERT INTO userdb_sqlite_owner (singleton, node_id) VALUES (TRUE, $1)`, nodeID); err != nil {
			return fmt.Errorf("create SQLite ownership claim: %w", err)
		}
		ownerNode = nodeID
	} else if err != nil {
		return fmt.Errorf("read SQLite ownership claim: %w", err)
	}
	if ownerNode != nodeID {
		return fmt.Errorf("%w: owner=%q requested=%q", ErrSQLiteOwnedByAnotherNode, ownerNode, nodeID)
	}

	var active bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM node_heartbeats
			WHERE node_id = $1 AND updated_at > NOW() - $2::interval
		)`, nodeID, sqliteHeartbeatTTL.String()).Scan(&active); err != nil {
		return fmt.Errorf("check active SQLite owner: %w", err)
	}
	if active {
		return fmt.Errorf("%w: node=%q", ErrSQLiteNodeAlreadyActive, nodeID)
	}

	// register_membership_policy_heartbeat rejects heartbeats that do not declare
	// the membership_policy_v1 capability once the authority is finalized. This
	// write already runs inside the caller's transaction, so the marker can be
	// set directly.
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.schema_capability_writer', 'v1', true)`); err != nil {
		return fmt.Errorf("mark schema capability writer: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO node_heartbeats (node_id, node_type, updated_at, schema_capabilities, instance_id)
		VALUES ($1, $2, NOW(), ARRAY['membership_policy_v1'], $3::uuid)
		ON CONFLICT (node_id) DO UPDATE SET
			node_type = EXCLUDED.node_type,
			node_url = NULL,
			updated_at = NOW(),
			schema_capabilities = EXCLUDED.schema_capabilities,
			instance_id = EXCLUDED.instance_id`, nodeID, nodeType, nodeidentity.InstanceID()); err != nil {
		return fmt.Errorf("reserve active SQLite owner: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE userdb_sqlite_owner SET updated_at = NOW() WHERE singleton = TRUE`); err != nil {
		return fmt.Errorf("refresh SQLite ownership claim: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit SQLite ownership claim: %w", err)
	}
	return nil
}
