package resourcetenancy

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func nativeManagementActorShape(a auth.AdminContextClaims) bool {
	if a.AccountID <= 0 || a.AccountIncarnationID == uuid.Nil || a.SessionID == "" || a.ExpiresAt.IsZero() || !a.ExpiresAt.After(time.Now()) {
		return false
	}
	switch a.Scope {
	case auth.AdminScopePlatform:
		return a.OrganizationID == uuid.Nil && a.MembershipID == uuid.Nil && a.PolicyRevision == 0 && a.SecurityRevision == 0 && a.EffectiveAuthority == ""
	case auth.AdminScopeOrganization:
		return a.OrganizationID != uuid.Nil && a.MembershipID != uuid.Nil && a.PolicyRevision > 0 && a.SecurityRevision > 0 && (a.EffectiveAuthority == "organization_admin" || a.EffectiveAuthority == "platform_admin")
	}
	return false
}

// The addressing reads below grant no authority. All of their identities are
// compared again after actor and resource locks in the frozen writer order.
func nativeManagementOwners(ctx context.Context, tx pgx.Tx, ids []uuid.UUID, lock bool) (map[uuid.UUID]Owner, error) {
	query := "SELECT id,kind,organization_id,revision FROM resource_owners WHERE id=ANY($1::uuid[]) ORDER BY id"
	if lock {
		query += " FOR SHARE"
	}
	rows, err := tx.Query(ctx, query, ids)
	if err != nil {
		return nil, nativeScanError(err)
	}
	result := map[uuid.UUID]Owner{}
	for rows.Next() {
		var o Owner
		if err = rows.Scan(&o.ID, &o.Kind, &o.OrganizationID, &o.Revision); err != nil {
			rows.Close()
			return nil, nativeScanError(err)
		}
		if !nativeScanOwnerValid(o) {
			rows.Close()
			return nil, ErrResourceHidden
		}
		result[o.ID] = o
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nativeScanError(err)
	}
	for _, id := range ids {
		if _, ok := result[id]; !ok {
			return nil, ErrResourceHidden
		}
	}
	return result, nil
}

func retainNativeManagementActor(ctx context.Context, tx pgx.Tx, a auth.AdminContextClaims, owners map[uuid.UUID]Owner) error {
	if !nativeManagementActorShape(a) {
		return ErrInvalidActor
	}
	ids := []uuid.UUID{}
	if a.Scope == auth.AdminScopeOrganization {
		ids = append(ids, a.OrganizationID)
	}
	for _, o := range owners {
		if o.OrganizationID != nil {
			ids = append(ids, *o.OrganizationID)
		}
	}
	// DISTINCT in ANY's predicate; ORDER BY makes the independent org lock order explicit.
	rows, err := tx.Query(ctx, "SELECT id,policy_revision,status FROM organizations WHERE id=ANY($1::uuid[]) ORDER BY id FOR SHARE", ids)
	if err != nil {
		return nativeScanError(err)
	}
	policies := map[uuid.UUID]int64{}
	for rows.Next() {
		var id uuid.UUID
		var rev int64
		var status string
		if err = rows.Scan(&id, &rev, &status); err != nil {
			rows.Close()
			return nativeScanError(err)
		}
		if status != "active" || rev <= 0 {
			rows.Close()
			return ErrResourceHidden
		}
		policies[id] = rev
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nativeScanError(err)
	}
	for _, id := range ids {
		if _, ok := policies[id]; !ok {
			return ErrResourceHidden
		}
	}
	if a.Scope == auth.AdminScopeOrganization && policies[a.OrganizationID] != a.PolicyRevision {
		return ErrInvalidActor
	}
	var accountID int
	var incarnation uuid.UUID
	var enabled bool
	var role string
	err = tx.QueryRow(ctx, "SELECT id,account_incarnation_id,enabled,role FROM users WHERE id=$1 FOR SHARE", a.AccountID).Scan(&accountID, &incarnation, &enabled, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidActor
	}
	if err != nil {
		return nativeScanError(err)
	}
	if accountID != a.AccountID || incarnation != a.AccountIncarnationID || !enabled {
		return ErrInvalidActor
	}
	if a.Scope == auth.AdminScopePlatform || a.EffectiveAuthority == "platform_admin" {
		if role != "admin" {
			return ErrInvalidActor
		}
	}
	if a.Scope == auth.AdminScopeOrganization {
		var id, organization uuid.UUID
		var account int
		var status, legacyRole string
		var revision int64
		err = tx.QueryRow(ctx, `SELECT id,organization_id,account_id,status,legacy_role,security_revision
   FROM organization_memberships WHERE organization_id=$1 AND account_id=$2 FOR SHARE`, a.OrganizationID, a.AccountID).Scan(&id, &organization, &account, &status, &legacyRole, &revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidActor
		}
		if err != nil {
			return nativeScanError(err)
		}
		if id != a.MembershipID || organization != a.OrganizationID || account != a.AccountID || status != "active" || revision != a.SecurityRevision || (a.EffectiveAuthority == "organization_admin" && legacyRole != "admin") {
			return ErrInvalidActor
		}
	}
	var userID int
	var profile *string
	var revoked *time.Time
	var expires, now time.Time
	err = tx.QueryRow(ctx, "SELECT user_id,profile_id,revoked_at,expires_at,clock_timestamp() FROM auth_sessions WHERE id=$1 FOR SHARE", a.SessionID).Scan(&userID, &profile, &revoked, &expires, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidActor
	}
	if err != nil {
		return nativeScanError(err)
	}
	if userID != a.AccountID || profile != nil || revoked != nil || !expires.After(now) || !a.ExpiresAt.After(now) {
		return ErrInvalidActor
	}
	return nil
}
func nativeManagementOwnersUnchanged(before, after map[uuid.UUID]Owner) bool {
	if len(before) != len(after) {
		return false
	}
	for id, b := range before {
		a, ok := after[id]
		if !ok || a.Kind != b.Kind || a.Revision != b.Revision || (a.OrganizationID == nil) != (b.OrganizationID == nil) {
			return false
		}
		if a.OrganizationID != nil && *a.OrganizationID != *b.OrganizationID {
			return false
		}
	}
	return true
}
func nativeManagementOwnerAccessTx(ctx context.Context, tx pgx.Tx, a auth.AdminContextClaims, o Owner, root RootRef, mutate bool) error {
	if a.Scope == auth.AdminScopePlatform {
		return nil
	}
	if o.Kind == OwnerOrganization {
		if o.OrganizationID != nil && *o.OrganizationID == a.OrganizationID {
			return nil
		}
		return ErrResourceHidden
	}
	if mutate || root.ID <= 0 {
		return ErrResourceHidden
	}
	query := `SELECT id FROM organization_entitlements WHERE organization_id=$1 AND root_owner_id=$2 AND status='active' AND security_revision>0`
	switch root.Kind {
	case RootPluginInstallation:
		query += ` AND root_kind='plugin_installation' AND entitlement_kind='plugin_availability' AND plugin_installation_id=$3 FOR SHARE`
	case RootMediaFolder:
		query += ` AND root_kind='media_folder' AND entitlement_kind='library_access' AND media_folder_id=$3 FOR SHARE`
	default:
		return ErrResourceHidden
	}
	var id uuid.UUID
	return nativeScanError(tx.QueryRow(ctx, query, a.OrganizationID, o.ID, root.ID).Scan(&id))
}

func (s *Store) RequireNativeManagementTx(ctx context.Context, tx pgx.Tx, actor auth.AdminContextClaims, sourceKey uuid.UUID, installationID *int64, ownerID uuid.UUID, folderID *int64, mutate bool) error {
	_, err := s.retainNativeResources(ctx, tx, actor, sourceKey, installationID, ownerID, folderID, mutate, mutate)
	return err
}

func (s *Store) retainNativeResources(ctx context.Context, tx pgx.Tx, actor auth.AdminContextClaims, key uuid.UUID, installationID *int64, ownerID uuid.UUID, folderID *int64, sourceMutate, folderMutate bool) (map[uuid.UUID]Owner, error) {
	if s == nil || s.pool == nil || tx == nil {
		return nil, ErrResourceUnavailable
	}
	if !nativeManagementActorShape(actor) {
		return nil, ErrInvalidActor
	}
	if ownerID == uuid.Nil || (key == uuid.Nil && folderID == nil) {
		return nil, ErrResourceHidden
	}
	var source storagesource.SourceConfig
	sourceQuery := `SELECT key,owner_id,installation_id,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled FROM bloem_storage_sources WHERE key=$1`
	if key != uuid.Nil {
		if err := tx.QueryRow(ctx, sourceQuery, key).Scan(&source.Key, &source.OwnerID, &source.InstallationID, &source.PluginID, &source.ProviderSourceID, &source.RootEntryID, &source.ConfigurationRevision, &source.Enabled); err != nil {
			return nil, nativeScanError(err)
		}
		if source.OwnerID != ownerID {
			return nil, ErrResourceHidden
		}
	}
	selectedInstall := installationID
	if selectedInstall == nil && key != uuid.Nil {
		selectedInstall = source.InstallationID
	}
	ids := []uuid.UUID{ownerID}
	var folderOwner uuid.UUID
	if folderID != nil {
		if *folderID <= 0 {
			return nil, ErrResourceHidden
		}
		if err := tx.QueryRow(ctx, "SELECT owner_id FROM media_folders WHERE id=$1", *folderID).Scan(&folderOwner); err != nil {
			return nil, nativeScanError(err)
		}
		ids = append(ids, folderOwner)
	}
	before, err := nativeManagementOwners(ctx, tx, ids, false)
	if err != nil {
		return nil, err
	}
	if err = retainNativeManagementActor(ctx, tx, actor, before); err != nil {
		return nil, err
	}
	var installOwner uuid.UUID
	var plugin, kind string
	var installEnabled bool
	var generation int64
	if selectedInstall != nil {
		if *selectedInstall <= 0 {
			return nil, ErrResourceHidden
		}
		lock := " FOR SHARE"
		if sourceMutate {
			lock = " FOR NO KEY UPDATE"
		}
		err = tx.QueryRow(ctx, "SELECT owner_id,plugin_id,kind,enabled,runtime_generation FROM plugin_installations WHERE id=$1"+lock, *selectedInstall).Scan(&installOwner, &plugin, &kind, &installEnabled, &generation)
		if err != nil {
			return nil, nativeScanError(err)
		}
		var markerOwner uuid.UUID
		var protocol int
		err = tx.QueryRow(ctx, "SELECT owner_id,protocol_version FROM bloem_storage_installations WHERE installation_id=$1 FOR SHARE", *selectedInstall).Scan(&markerOwner, &protocol)
		if err != nil {
			return nil, nativeScanError(err)
		}
		if installOwner != ownerID || markerOwner != ownerID || kind != "plugin" || protocol != 1 || generation <= 0 || plugin != source.PluginID {
			return nil, ErrResourceHidden
		}
	}
	if key != uuid.Nil {
		lock := " FOR SHARE"
		if sourceMutate {
			lock = " FOR UPDATE"
		}
		// One ordered statement locks siblings and the detached retained command key.
		rows, e := tx.Query(ctx, sourceQuery[:len(sourceQuery)-len(" WHERE key=$1")]+" WHERE key=$1 OR installation_id=$2 ORDER BY key"+lock, key, selectedInstall)
		if e != nil {
			return nil, nativeScanError(e)
		}
		found := false
		for rows.Next() {
			var actual storagesource.SourceConfig
			if e = rows.Scan(&actual.Key, &actual.OwnerID, &actual.InstallationID, &actual.PluginID, &actual.ProviderSourceID, &actual.RootEntryID, &actual.ConfigurationRevision, &actual.Enabled); e != nil {
				rows.Close()
				return nil, nativeScanError(e)
			}
			if actual.Key == key {
				found = true
				identity := source
				identity.ConfigurationRevision = actual.ConfigurationRevision
				identity.Enabled = actual.Enabled
				if !nativeManagementSourceEqual(actual, identity) {
					rows.Close()
					return nil, ErrAuthorizationStateChanged
				}
				source = actual
			}
			if selectedInstall != nil && actual.InstallationID != nil && *actual.InstallationID == *selectedInstall && (actual.OwnerID != ownerID || actual.PluginID != plugin) {
				rows.Close()
				return nil, ErrResourceHidden
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, nativeScanError(e)
		}
		if !found {
			return nil, ErrResourceHidden
		}
		if selectedInstall != nil && ((source.InstallationID != nil && *source.InstallationID != *selectedInstall) || (source.InstallationID == nil && (source.Enabled || installEnabled))) {
			return nil, ErrResourceHidden
		}
		if selectedInstall != nil && source.InstallationID == nil {
			// Owner/plugin equality does not identify a detached installation.
			// The source row is already retained in the ordered resource lock set.
			var latest *int64
			if err = tx.QueryRow(ctx, "SELECT latest_installation_id FROM bloem_storage_sources WHERE key=$1", key).Scan(&latest); err != nil {
				return nil, nativeScanError(err)
			}
			if latest == nil || *latest != *selectedInstall {
				return nil, ErrResourceHidden
			}
		}
	}
	if folderID != nil {
		var acquired bool
		err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1,8500002))", "bloem:native-library:"+strconv.FormatInt(*folderID, 10)).Scan(&acquired)
		if err != nil {
			return nil, nativeScanError(err)
		}
		if !acquired {
			return nil, ErrResourceUnavailable
		}
		var actualOwner uuid.UUID
		err = tx.QueryRow(ctx, "SELECT owner_id FROM media_folders WHERE id=$1 FOR UPDATE NOWAIT", *folderID).Scan(&actualOwner)
		if err != nil {
			return nil, nativeScanError(err)
		}
		if actualOwner != folderOwner {
			return nil, ErrAuthorizationStateChanged
		}
		rows, e := tx.Query(ctx, "SELECT folder_id FROM bloem_native_libraries WHERE folder_id=$1 FOR UPDATE NOWAIT", *folderID)
		if e != nil {
			return nil, nativeScanError(e)
		}
		exists := rows.Next()
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, nativeScanError(e)
		}
		if !exists {
			return nil, &catalog.NativeOnboardingError{Code: "native_library_required"}
		}
		rows, e = tx.Query(ctx, "SELECT id FROM bloem_storage_bindings WHERE folder_id=$1 ORDER BY id FOR SHARE NOWAIT", *folderID)
		if e != nil {
			return nil, nativeScanError(e)
		}
		for rows.Next() {
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, nativeScanError(e)
		}
	}
	after, err := nativeManagementOwners(ctx, tx, ids, true)
	if err != nil {
		return nil, err
	}
	if !nativeManagementOwnersUnchanged(before, after) {
		return nil, ErrAuthorizationStateChanged
	}
	if key != uuid.Nil {
		root := RootRef{Kind: RootPluginInstallation}
		if selectedInstall != nil {
			root.ID = *selectedInstall
		}
		if err = nativeManagementOwnerAccessTx(ctx, tx, actor, after[ownerID], root, sourceMutate); err != nil {
			return nil, err
		}
	}
	if folderID != nil {
		if err = nativeManagementOwnerAccessTx(ctx, tx, actor, after[folderOwner], RootRef{Kind: RootMediaFolder, ID: *folderID}, folderMutate); err != nil {
			return nil, err
		}
	}
	// Reload the same already-retained actor rows after all waits, including the
	// database clock for session expiry. This acquires no new actor/target locks.
	if err = retainNativeManagementActor(ctx, tx, actor, after); err != nil {
		return nil, err
	}
	return after, nil
}
func nativeManagementSourceEqual(a, b storagesource.SourceConfig) bool {
	if a.Key != b.Key || a.OwnerID != b.OwnerID || a.PluginID != b.PluginID || a.ProviderSourceID != b.ProviderSourceID || a.RootEntryID != b.RootEntryID || a.ConfigurationRevision != b.ConfigurationRevision || a.Enabled != b.Enabled || (a.InstallationID == nil) != (b.InstallationID == nil) {
		return false
	}
	return a.InstallationID == nil || *a.InstallationID == *b.InstallationID
}
