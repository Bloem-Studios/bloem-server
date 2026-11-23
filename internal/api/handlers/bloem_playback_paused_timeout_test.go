package handlers

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// nativeBatchFileResolver answers like the catalog's file repository, where
// file 9 lives in native storage.
type nativeBatchFileResolver struct{ batchFileResolver }

func (r *nativeBatchFileResolver) GetByIDs(ctx context.Context, ids []int) ([]*models.MediaFile, error) {
	files, err := r.batchFileResolver.GetByIDs(ctx, ids)
	for _, file := range files {
		if file.ID == 9 {
			file.FilePath = "bloem-storage:" + strings.Repeat("a", 64)
		}
	}
	return files, err
}

// The paused-timeout sweep reads a page's files through the handler's
// guarded resolver every lifecycle sweep. The guard keeps Silo's single
// batched lookup, and leaves a native storage file out as its GetByID does,
// so the sweep neither reads file by file nor warns about every paused
// native session on each sweep.
func TestPausedCandidateFilesBatchesThroughNativeGuard(t *testing.T) {
	resolver := &nativeBatchFileResolver{}
	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0), resolver)
	candidates := []pausedTimeoutCandidate{{sessionID: "a", mediaFileID: 7}, {sessionID: "b", mediaFileID: 9}}

	files := handler.pausedCandidateFiles(t.Context(), candidates)

	if len(resolver.batches) != 1 || !slices.Equal(resolver.batches[0], []int{7, 9}) || len(resolver.calls) != 0 {
		t.Fatalf("guarded resolver: batches %v, single calls %v; want one batch of [7 9]", resolver.batches, resolver.calls)
	}
	if files[7] == nil {
		t.Fatalf("files = %v, want the filesystem file 7", files)
	}
	if files[9] != nil {
		t.Fatalf("native storage file 9 reached the sweep: %+v", files[9])
	}
}
