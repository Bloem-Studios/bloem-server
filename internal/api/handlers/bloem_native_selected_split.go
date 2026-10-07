package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

func requireNativeSplitPhase(ctx context.Context, q catalog.NativePhaseQuery, source string, target splitTarget, moved []splitFile) error {
	if !catalog.NativePhaseRequest(ctx) {
		return nil
	}
	selected := catalog.NativePhaseTargets{ContentIDs: []string{source}, LibraryIDs: append(distinctFolderIDs(moved), target.folderIDs...)}
	for _, file := range moved {
		selected.FileIDs = append(selected.FileIDs, file.ID)
	}
	selected.Prospective = []catalog.NativePhaseProspective{{ContentID: target.contentID, SourceIDs: []string{source}, LibraryIDs: target.folderIDs}}
	return catalog.RequireNativePhase(ctx, q, selected)
}
