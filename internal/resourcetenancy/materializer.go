package resourcetenancy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
		return MaterializationResult{}, fmt.Errorf("%w: begin bundle materialization: %v", ErrResourceUnavailable, err)
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
		return MaterializationResult{}, fmt.Errorf("%w: lock organization: %v", ErrResourceUnavailable, err)
	}
	if organizationStatus != "active" {
		return MaterializationResult{}, ErrOrganizationUnavailable
	}

	if err = tx.QueryRow(ctx, `
		SELECT bundles.id, bundles.active_revision
		FROM entitlement_bundles AS bundles
		JOIN entitlement_bundle_versions AS versions
		  ON versions.bundle_id = bundles.id
		 AND versions.revision = bundles.active_revision
		WHERE bundles.is_organization_creation_default
		  AND bundles.status = 'active'
		FOR UPDATE OF bundles`).Scan(&result.BundleID, &result.Revision); errors.Is(err, pgx.ErrNoRows) {
		return MaterializationResult{}, ErrDefaultBundleUnavailable
	} else if err != nil {
		return MaterializationResult{}, fmt.Errorf("%w: lock default bundle: %v", ErrResourceUnavailable, err)
	}

	var memberCount int64
	if err = tx.QueryRow(ctx, `
		SELECT count(*)
		FROM entitlement_bundle_members
		WHERE bundle_id=$1 AND bundle_revision=$2`, result.BundleID, result.Revision).Scan(&memberCount); err != nil {
		return MaterializationResult{}, fmt.Errorf("%w: count default bundle members: %v", ErrResourceUnavailable, err)
	}

	command, err := tx.Exec(ctx, `
		INSERT INTO organization_entitlements (
			organization_id,
			entitlement_kind,
			root_kind,
			root_owner_id,
			media_folder_id,
			status,
			source_bundle_id,
			source_bundle_revision,
			granted_by_account_id,
			granted_by_service
		)
		SELECT
			$1,
			members.entitlement_kind,
			members.root_kind,
			members.root_owner_id,
			members.media_folder_id,
			'active',
			members.bundle_id,
			members.bundle_revision,
			$3,
			$4
		FROM entitlement_bundle_members AS members
		WHERE members.bundle_id=$2
		  AND members.bundle_revision=$5
		  AND members.root_kind='media_folder'
		ORDER BY members.media_folder_id
		ON CONFLICT (organization_id, media_folder_id)
			WHERE status IN ('active', 'suspended') AND media_folder_id IS NOT NULL
			DO NOTHING`, organizationID, result.BundleID, accountID, service, result.Revision)
	if err != nil {
		return MaterializationResult{}, fmt.Errorf("%w: materialize library entitlements: %v", ErrResourceUnavailable, err)
	}
	result.Created += command.RowsAffected()

	command, err = tx.Exec(ctx, `
		INSERT INTO organization_entitlements (
			organization_id,
			entitlement_kind,
			root_kind,
			root_owner_id,
			plugin_installation_id,
			status,
			source_bundle_id,
			source_bundle_revision,
			granted_by_account_id,
			granted_by_service
		)
		SELECT
			$1,
			members.entitlement_kind,
			members.root_kind,
			members.root_owner_id,
			members.plugin_installation_id,
			'active',
			members.bundle_id,
			members.bundle_revision,
			$3,
			$4
		FROM entitlement_bundle_members AS members
		WHERE members.bundle_id=$2
		  AND members.bundle_revision=$5
		  AND members.root_kind='plugin_installation'
		ORDER BY members.plugin_installation_id
		ON CONFLICT (organization_id, plugin_installation_id)
			WHERE status IN ('active', 'suspended') AND plugin_installation_id IS NOT NULL
			DO NOTHING`, organizationID, result.BundleID, accountID, service, result.Revision)
	if err != nil {
		return MaterializationResult{}, fmt.Errorf("%w: materialize plugin entitlements: %v", ErrResourceUnavailable, err)
	}
	result.Created += command.RowsAffected()

	var coveredCount int64
	if err = tx.QueryRow(ctx, `
		SELECT count(*)
		FROM entitlement_bundle_members AS members
		WHERE members.bundle_id=$1
		  AND members.bundle_revision=$2
		  AND EXISTS (
			SELECT 1
			FROM organization_entitlements AS entitlements
			WHERE entitlements.organization_id=$3
			  AND entitlements.status='active'
			  AND entitlements.root_owner_id=members.root_owner_id
			  AND entitlements.root_kind=members.root_kind
			  AND entitlements.media_folder_id IS NOT DISTINCT FROM members.media_folder_id
			  AND entitlements.plugin_installation_id IS NOT DISTINCT FROM members.plugin_installation_id
		  )`, result.BundleID, result.Revision, organizationID).Scan(&coveredCount); err != nil {
		return MaterializationResult{}, fmt.Errorf("%w: verify bundle materialization: %v", ErrResourceUnavailable, err)
	}
	if coveredCount != memberCount || result.Created > memberCount {
		return MaterializationResult{}, fmt.Errorf("%w: bundle coverage %d of %d", ErrResourceUnavailable, coveredCount, memberCount)
	}
	result.Existing = memberCount - result.Created

	if err = tx.Commit(ctx); err != nil {
		return MaterializationResult{}, fmt.Errorf("%w: commit bundle materialization: %v", ErrResourceUnavailable, err)
	}
	return result, nil
}

func normalizeActor(actor Actor) (accountID *int, service *string, err error) {
	serviceName := strings.TrimSpace(actor.Service)
	hasAccount := actor.AccountID != nil && *actor.AccountID > 0
	hasService := serviceName != ""
	if hasAccount == hasService {
		return nil, nil, ErrInvalidActor
	}
	if hasAccount {
		value := *actor.AccountID
		return &value, nil, nil
	}
	return nil, &serviceName, nil
}
