package entitlements

import (
	"context"
	"fmt"
	"reflect"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/accesspolicy"
)

// RevisePreviewLibrariesInTransaction only changes the library selection of
// the protected template, joining preview account reapplication atomically.
func (s *Store) RevisePreviewLibrariesInTransaction(ctx context.Context, tx pgx.Tx, ids []int) (int64, error) {
	var current int64
	if err := tx.QueryRow(ctx, `SELECT current_revision FROM entitlement_templates WHERE key='bloem-preview' AND enabled AND NOT archived FOR UPDATE`).Scan(&current); err != nil {
		return 0, err
	}
	t, err := getTemplate(ctx, tx, accesspolicy.PreviewTemplateKey, current, false)
	if err != nil {
		return 0, err
	}
	t.Policy.LibraryIDs = ids
	p, err := normalizePolicy(t.Policy)
	if err != nil {
		return 0, err
	}
	p.AllowedPermissions = []string{}
	if len(ids) == 0 {
		p.LibraryIDs = []int{}
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM media_folders WHERE id=ANY($1) AND enabled`, p.LibraryIDs).Scan(&count); err != nil {
		return 0, err
	}
	if count != len(p.LibraryIDs) {
		return 0, fmt.Errorf("%w: preview libraries must exist and be enabled", ErrInvalidPolicy)
	}
	before, err := getTemplate(ctx, tx, accesspolicy.PreviewTemplateKey, current, false)
	if err != nil {
		return 0, err
	}
	if reflect.DeepEqual(before.Policy.LibraryIDs, p.LibraryIDs) {
		return current, nil
	}
	if err = insertRevision(ctx, tx, accesspolicy.PreviewTemplateKey, current+1, p); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE entitlement_templates SET current_revision=$1,updated_at=now() WHERE key='bloem-preview'`, current+1); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO entitlement_audit_events(action,template_key,template_revision) VALUES('entitlement_template.revised','bloem-preview',$1)`, current+1); err != nil {
		return 0, err
	}
	return current + 1, nil
}
