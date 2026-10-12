package jellycompat

import (
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
)

func TestJellyfinHidesNativeAudioInMixedAudioLibrary(t *testing.T) {
	if !isCompatHiddenLibraryType("audiobooks") || !isCompatExcludedMediaType("audiobook") {
		t.Fatal("audiobook library/item became visible through Jellyfin")
	}
	if isCompatHiddenLibraryType("movies") || isCompatExcludedMediaType("movie") {
		t.Fatal("ordinary video compatibility changed")
	}
	native := &models.MediaFile{ID: 1, ContentID: "book", FilePath: "bloem-storage:track", BaseType: "audiobook", Container: "mp3", CodecAudio: "mp3"}
	if file, err := GuardNativeStorageFiles(nativeContainmentResolver{file: native}).GetByID(t.Context(), 1); file != nil || !errors.Is(err, scanner.ErrFileNotFound) {
		t.Fatalf("native=%+v err=%v", file, err)
	}
	local := *native
	local.ID = 2
	local.FilePath = "/fixture/book.mp3"
	if file, err := GuardNativeStorageFiles(nativeContainmentResolver{file: &local}).GetByID(t.Context(), 2); err != nil || file != &local {
		t.Fatalf("local=%+v err=%v", file, err)
	}
}
