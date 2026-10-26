package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DefaultBundleGrant is what an organization received from the platform's
// creation-default entitlement bundle.
type DefaultBundleGrant struct {
	BundleID uuid.UUID
	Revision int64
	Created  int64
	Existing int64
}

// defaultBundleGrantService names organization creation as the grantor when
// no administrator account made the organization.
const defaultBundleGrantService = "organization-creation"

// GrantDefaultBundleInTransaction grants organizationID every platform library
// and plugin installation in the active revision of the creation-default
// entitlement bundle, inside the caller's transaction. Exactly one of
// accountID and service names the grantor. found is false, and nothing
// changes, when no active creation-default bundle exists. Rows the
// organization already holds are left as they are, so a repeat grant is a
// no-op; a coverage shortfall afterwards is an error, and the caller's
// transaction must roll back.
func GrantDefaultBundleInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID *int, service *string) (grant DefaultBundleGrant, found bool, err error) {
	if err = tx.QueryRow(ctx, `
		SELECT bundles.id, bundles.active_revision
		FROM entitlement_bundles AS bundles
		JOIN entitlement_bundle_versions AS versions
		  ON versions.bundle_id = bundles.id
		 AND versions.revision = bundles.active_revision
		WHERE bundles.is_organization_creation_default
		  AND bundles.status = 'active'
		FOR UPDATE OF bundles`).Scan(&grant.BundleID, &grant.Revision); errors.Is(err, pgx.ErrNoRows) {
		return DefaultBundleGrant{}, false, nil
	} else if err != nil {
		return DefaultBundleGrant{}, false, fmt.Errorf("lock default bundle: %w", err)
	}

	var memberCount int64
	if err = tx.QueryRow(ctx, `
		SELECT count(*)
		FROM entitlement_bundle_members
		WHERE bundle_id=$1 AND bundle_revision=$2`, grant.BundleID, grant.Revision).Scan(&memberCount); err != nil {
		return DefaultBundleGrant{}, true, fmt.Errorf("count default bundle members: %w", err)
	}
	for _, statement := range []struct{ label, sql string }{
		{"library", grantBundleLibrariesSQL},
		{"plugin", grantBundlePluginsSQL},
	} {
		command, err := tx.Exec(ctx, statement.sql, organizationID, grant.BundleID, accountID, service, grant.Revision)
		if err != nil {
			return DefaultBundleGrant{}, true, fmt.Errorf("materialize %s entitlements: %w", statement.label, err)
		}
		grant.Created += command.RowsAffected()
	}

	var coveredCount int64
	if err = tx.QueryRow(ctx, bundleCoverageSQL, grant.BundleID, grant.Revision, organizationID).Scan(&coveredCount); err != nil {
		return DefaultBundleGrant{}, true, fmt.Errorf("verify bundle materialization: %w", err)
	}
	if coveredCount != memberCount || grant.Created > memberCount {
		return DefaultBundleGrant{}, true, fmt.Errorf("bundle coverage %d of %d", coveredCount, memberCount)
	}
	grant.Existing = memberCount - grant.Created
	return grant, true, nil
}

// grantBundleLibrariesSQL and grantBundlePluginsSQL grant ($1) the bundle
// ($2, revision $5) members of one root kind, granted by account $3 or
// service $4, leaving live rows the organization already holds alone.
const grantBundleLibrariesSQL = `
	INSERT INTO organization_entitlements (
		organization_id, entitlement_kind, root_kind, root_owner_id, media_folder_id, status,
		source_bundle_id, source_bundle_revision, granted_by_account_id, granted_by_service
	)
	SELECT $1, members.entitlement_kind, members.root_kind, members.root_owner_id, members.media_folder_id,
	       'active', members.bundle_id, members.bundle_revision, $3, $4
	FROM entitlement_bundle_members AS members
	WHERE members.bundle_id=$2
	  AND members.bundle_revision=$5
	  AND members.root_kind='media_folder'
	ORDER BY members.media_folder_id
	ON CONFLICT (organization_id, media_folder_id)
		WHERE status IN ('active', 'suspended') AND media_folder_id IS NOT NULL
		DO NOTHING`

const grantBundlePluginsSQL = `
	INSERT INTO organization_entitlements (
		organization_id, entitlement_kind, root_kind, root_owner_id, plugin_installation_id, status,
		source_bundle_id, source_bundle_revision, granted_by_account_id, granted_by_service
	)
	SELECT $1, members.entitlement_kind, members.root_kind, members.root_owner_id, members.plugin_installation_id,
	       'active', members.bundle_id, members.bundle_revision, $3, $4
	FROM entitlement_bundle_members AS members
	WHERE members.bundle_id=$2
	  AND members.bundle_revision=$5
	  AND members.root_kind='plugin_installation'
	ORDER BY members.plugin_installation_id
	ON CONFLICT (organization_id, plugin_installation_id)
		WHERE status IN ('active', 'suspended') AND plugin_installation_id IS NOT NULL
		DO NOTHING`

// bundleCoverageSQL counts the bundle ($1, revision $2) members the
// organization ($3) holds an active entitlement for.
const bundleCoverageSQL = `
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
	  )`

// grantCreationDefaultBundle gives a newly created organization the
// creation-default bundle. Creation does not depend on a bundle existing.
func grantCreationDefaultBundle(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int) error {
	var account *int
	var service *string
	if accountID > 0 {
		account = &accountID
	} else {
		name := defaultBundleGrantService
		service = &name
	}
	if _, _, err := GrantDefaultBundleInTransaction(ctx, tx, organizationID, account, service); err != nil {
		return fmt.Errorf("tenancy: grant default entitlement bundle: %w", err)
	}
	return nil
}
