package literaryworks

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

func requireNativeWorkPhase(ctx context.Context, q catalog.NativePhaseQuery, workID string, ids []string) error {
	if !catalog.NativePhaseRequest(ctx) {
		return nil
	}
	ids = append([]string(nil), ids...)
	rows, err := q.Query(ctx, `SELECT content_id FROM literary_work_items WHERE work_id=$1`, workID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	return catalog.RequireNativePhase(ctx, q, catalog.NativePhaseTargets{ContentIDs: ids})
}
func requireNativeWorkLinks(ctx context.Context, q catalog.NativePhaseQuery, workID string, items []LinkItemParams) error {
	if !catalog.NativePhaseRequest(ctx) {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ContentID)
	}
	return requireNativeWorkPhase(ctx, q, workID, ids)
}
