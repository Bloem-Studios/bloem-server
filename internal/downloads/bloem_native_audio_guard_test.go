package downloads

import (
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

func TestDownloadsRefuseNativeAudio(t *testing.T) {
	native := &models.MediaFile{ID: 1, ContentID: "book", FilePath: "bloem-storage:track", BaseType: "audiobook", CodecAudio: "mp3", Container: "mp3"}
	local := &models.MediaFile{ID: 2, ContentID: "book", FilePath: "/fixture/book.mp3", BaseType: "audiobook", CodecAudio: "mp3", Container: "mp3"}
	raw := &nativeContainmentFiles{single: native, rows: []*models.MediaFile{native, local}}
	guarded := GuardNativeStorageFiles(raw)
	if file, err := guarded.GetByID(t.Context(), native.ID); file != nil || !errors.Is(err, catalog.ErrItemNotFound) {
		t.Fatalf("native=%+v err=%v", file, err)
	}
	candidates, err := guarded.GetByContentID(t.Context(), "book")
	if err != nil || len(candidates) != 1 || candidates[0] != local {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	raw.single = local
	if file, err := guarded.GetByID(t.Context(), local.ID); err != nil || file != local {
		t.Fatalf("local=%+v err=%v", file, err)
	}
}
