package metadata

import (
	"context"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

type nativeMetadataBasisKey struct{}

func nativeMetadataBasis(ctx context.Context, req ProcessRequest) context.Context {
	if !catalog.NativePhaseRequest(ctx) {
		return ctx
	}
	basis := catalog.NativePhaseProspective{}
	if req.ContentID != "" {
		basis.SourceIDs = []string{req.ContentID}
	}
	if folder, err := strconv.Atoi(req.FolderID); err == nil && folder > 0 {
		basis.LibraryIDs = []int{folder}
	}
	return context.WithValue(ctx, nativeMetadataBasisKey{}, basis)
}
func (s *MetadataService) requireNativeMetadataPhase(ctx context.Context, q catalog.NativePhaseQuery, id string) error {
	if !catalog.NativePhaseRequest(ctx) || id == "" {
		return nil
	}
	if q == nil && s.dbPool != nil {
		q = s.dbPool
	}
	basis, _ := ctx.Value(nativeMetadataBasisKey{}).(catalog.NativePhaseProspective)
	basis.ContentID = id
	selected := catalog.NativePhaseTargets{Prospective: []catalog.NativePhaseProspective{basis}}
	if q != nil {
		rows, err := q.Query(ctx, `SELECT content_id FROM seasons WHERE series_id=$1 UNION SELECT content_id FROM episodes WHERE series_id=$1`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var child string
			if err := rows.Scan(&child); err != nil {
				rows.Close()
				return err
			}
			selected.ContentIDs = append(selected.ContentIDs, child)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return catalog.RequireNativePhase(ctx, q, selected)
}

// Accepted provider IDs are discovery. Only the actual selected owner is admitted;
// rejected/quarantined IDs have already been removed by Process's existing logic.
func (s *MetadataService) requireNativeProviderPhase(ctx context.Context, req ProcessRequest, ids map[string]string, kind string) error {
	if !catalog.NativePhaseRequest(ctx) {
		return nil
	}
	ctx = nativeMetadataBasis(ctx, req)
	existing, err := s.findExistingByProviderIDs(ctx, ids, kind, req.ContentID)
	if err != nil {
		return err
	}
	targets := catalog.NativePhaseTargets{}
	if req.ContentID != "" {
		targets.ContentIDs = append(targets.ContentIDs, req.ContentID)
	}
	if existing != nil {
		targets.ContentIDs = append(targets.ContentIDs, existing.ContentID)
	}
	if folder, err := strconv.Atoi(req.FolderID); err == nil && folder > 0 {
		targets.LibraryIDs = []int{folder}
	}
	return catalog.RequireNativePhase(ctx, s.dbPool, targets)
}
func (s *MetadataService) nativeAutoTranslate(ctx context.Context, contentID, language string) {
	if !catalog.NativePhaseRequest(ctx) {
		s.autoTranslator.AutoEnqueue(context.WithoutCancel(ctx), contentID, language)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), metadataOnDemandRefreshTimeout)
	defer cancel()
	s.autoTranslator.AutoEnqueue(ctx, contentID, language)
}

// Keep the original hook decoration (including trailer failure observation).
func nativeRefreshHooks(ctx context.Context, hooks onDemandRefreshHooks) onDemandRefreshHooks {
	decorate := hooks.decorateContext
	hooks.decorateContext = func(detached context.Context) context.Context {
		detached = catalog.CarryNativePhaseOrigin(ctx, detached)
		if decorate != nil {
			detached = decorate(detached)
		}
		return detached
	}
	return hooks
}

func nativeOptionalRefreshHooks(origin []context.Context) onDemandRefreshHooks {
	if len(origin) == 0 {
		return onDemandRefreshHooks{}
	}
	return nativeRefreshHooks(origin[0], onDemandRefreshHooks{})
}
