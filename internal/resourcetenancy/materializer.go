package resourcetenancy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type Actor struct {
	AccountID *int
	Service   string
}

type MaterializationResult struct {
	BundleID uuid.UUID
	Revision int64
	Created  int64
	Existing int64
}

type Materializer struct {
	pool *pgxpool.Pool
}

func NewMaterializer(pool *pgxpool.Pool) *Materializer {
	return &Materializer{pool: pool}
}

func (m *Materializer) MaterializeDefaultBundle(
	ctx context.Context,
	organizationID uuid.UUID,
	actor Actor,
) (result MaterializationResult, err error) {
	accountID, service, err := normalizeActor(actor)
	if err != nil {
		return MaterializationResult{}, err
	}
	if m == nil || m.pool == nil {
		return MaterializationResult{}, ErrResourceUnavailable
	}
	if organizationID == uuid.Nil {
		return MaterializationResult{}, ErrOrganizationUnavailable
	}

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return MaterializationResult{}, fmt.Errorf("%w: begin bundle materialization: %w", ErrResourceUnavailable, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	var organizationStatus string
	if err = tx.QueryRow(ctx, `
		SELECT status
		FROM organizations
		WHERE id=$1
		FOR UPDATE`, organizationID).Scan(&organizationStatus); errors.Is(err, pgx.ErrNoRows) {
		return MaterializationResult{}, ErrOrganizationUnavailable
	} else if err != nil {
		return MaterializationResult{}, fmt.Errorf("%w: lock organization: %w", ErrResourceUnavailable, err)
	}
	if organizationStatus != "active" {
		return MaterializationResult{}, ErrOrganizationUnavailable
	}

	grant, found, err := tenancy.GrantDefaultBundleInTransaction(ctx, tx, organizationID, accountID, service)
	if err != nil {
		return MaterializationResult{}, fmt.Errorf("%w: %w", ErrResourceUnavailable, err)
	}
	if !found {
		return MaterializationResult{}, ErrDefaultBundleUnavailable
	}
	result = MaterializationResult{BundleID: grant.BundleID, Revision: grant.Revision, Created: grant.Created, Existing: grant.Existing}

	if err = tx.Commit(ctx); err != nil {
		return MaterializationResult{}, fmt.Errorf("%w: commit bundle materialization: %w", ErrResourceUnavailable, err)
	}
	return result, nil
}

func normalizeActor(actor Actor) (accountID *int, service *string, err error) {
	serviceName := strings.TrimSpace(actor.Service)
	hasAccount := actor.AccountID != nil
	hasService := serviceName != ""
	if hasAccount == hasService || (hasAccount && *actor.AccountID <= 0) {
		return nil, nil, ErrInvalidActor
	}
	if hasAccount {
		value := *actor.AccountID
		return &value, nil, nil
	}
	return nil, &serviceName, nil
}
