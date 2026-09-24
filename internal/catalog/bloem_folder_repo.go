package catalog

import (
	"context"
	"fmt"
)

// releaseFolderTenancy releases the tenancy rows that reference a media
// folder so DeleteWithStats can delete the folder row.
//
// Bloem auto-entitles the default organization to every new media folder
// (trigger bloem_entitle_default_organization_media_folder, migration
// 20260813090000) and organization_entitlements references media_folders
// ON DELETE RESTRICT. Without this the folder delete fails with a foreign key
// violation and library deletion is impossible. The RESTRICT is deliberately
// left in place as a backstop so no other path can orphan an entitlement;
// this is the one flow that may clear them, and it does so explicitly rather
// than by cascade. An entitlement to a library that no longer exists has
// nothing to authorize. entitlement_audit_events carries no foreign key here,
// so the audit trail survives the delete.
func (r *FolderRepository) releaseFolderTenancy(ctx context.Context, id int) error {
	if err := retryOnDeadlock(ctx, func() error {
		_, e := r.pool.Exec(ctx, `DELETE FROM organization_entitlements WHERE media_folder_id = $1`, id)
		return e
	}); err != nil {
		return fmt.Errorf("releasing folder entitlements: %w", err)
	}
	return nil
}
