package listcollections

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// CleanupTargets restores manual ownership after journal/grant deletion and
// tombstones bindings whose native target disappeared. It is bounded and safe
// across replicas. Native membership is retained; ordinary imported sources are
// untouched. Request dispatch independently checks target existence before send.
func (s *Service) CleanupTargets(ctx context.Context, limit int) error {
	if s == nil || s.Pool == nil || limit < 1 || limit > 500 {
		return ErrSnapshot
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `WITH missing AS (
 SELECT b.id FROM bloem_list_bindings b WHERE b.status<>'detached' AND (
 (b.scope='personal' AND NOT EXISTS(SELECT 1 FROM user_personal_collections c WHERE c.user_id=b.user_id AND c.id=b.target_id AND c.native AND c.collection_type='synced' AND c.source_config->>'bloem_list_binding'=b.id::text))
 OR (b.scope='server' AND NOT EXISTS(SELECT 1 FROM library_collections c WHERE c.id=b.target_id AND c.collection_type='manual' AND c.source_config->>'bloem_list_binding'=b.id::text)))
 ORDER BY b.id LIMIT $1 FOR UPDATE OF b SKIP LOCKED)
 UPDATE bloem_list_bindings b SET status='detached',generation=generation+1,grant_cipher='',failure='target_removed' FROM missing WHERE b.id=missing.id RETURNING b.id::text`, limit)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		if _, err = tx.Exec(ctx, `UPDATE bloem_list_jobs SET state='failed',failure='target_removed' WHERE binding_id=ANY($1::uuid[]) AND state IN('pending','running')`, ids); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE bloem_list_request_intents SET wanted=false,status=CASE WHEN status='delivered' THEN status ELSE 'cancelled' END WHERE binding_id=ANY($1::uuid[])`, ids); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `WITH orphan AS (
 SELECT c.user_id,c.id FROM user_personal_collections c WHERE c.native AND c.collection_type='synced' AND COALESCE(c.source_config->>'bloem_list_binding','')<>''
 AND NOT EXISTS(SELECT 1 FROM bloem_list_bindings b WHERE b.id::text=c.source_config->>'bloem_list_binding' AND b.scope='personal' AND b.user_id=c.user_id AND b.target_id=c.id AND b.status<>'detached')
 ORDER BY c.user_id,c.id LIMIT $1 FOR UPDATE OF c SKIP LOCKED)
 UPDATE user_personal_collections c SET collection_type='manual',source_config=source_config-'bloem_list_binding' FROM orphan WHERE c.user_id=orphan.user_id AND c.id=orphan.id`, limit); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `WITH orphan AS (
 SELECT c.id FROM library_collections c WHERE COALESCE(c.source_config->>'bloem_list_binding','')<>''
 AND NOT EXISTS(SELECT 1 FROM bloem_list_bindings b WHERE b.id::text=c.source_config->>'bloem_list_binding' AND b.scope='server' AND b.target_id=c.id AND b.status<>'detached')
 ORDER BY c.id LIMIT $1 FOR UPDATE OF c SKIP LOCKED)
 UPDATE library_collections c SET source_config=source_config-'bloem_list_binding' FROM orphan WHERE c.id=orphan.id`, limit); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
