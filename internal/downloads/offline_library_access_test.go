package downloads

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/subtitles"
)

type librarySubtitleSource struct{ fakeSubtitleSource }

func (librarySubtitleSource) GetSubtitleContent(context.Context, int) (*subtitles.DownloadedSubtitle, []byte, error) {
	return &subtitles.DownloadedSubtitle{MediaFileID: 33, Format: "srt"}, []byte("shared-subtitle"), nil
}

// An accessible canonical title must not authorize subtitles belonging to a
// different file whose library grant has been removed.
func TestManagedSubtitleRechecksFileLibraryPostgres(t *testing.T) {
	repo := statusEventTestRepo(t)
	now := time.Now()
	dl := &Download{ID: "entry", UserID: 1, ProfileID: "profile", DeviceID: "device", ContentID: "same-title", MediaFileID: 33, Kind: KindQueued, Status: StatusReady, CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(t.Context(), dl); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "movie.srt")
	if err := os.WriteFile(path, []byte("shared-subtitle"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &Service{repo: repo, cfg: config.DownloadConfig{Enabled: true}, itemAccess: &syncAccess{}, fileRepo: fakeFileResolver{file: &models.MediaFile{ID: 33, ContentID: "same-title", MediaFolderID: 33, ExternalSubtitles: []models.ExternalSubtitle{{Path: path, Format: "srt"}}}}, subtitleSource: librarySubtitleSource{}}
	for _, ref := range []string{"external:0", "downloaded:1"} {
		t.Run(ref, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			allowed := httptest.NewRecorder()
			if err := svc.ServeSubtitle(t.Context(), allowed, request, 1, "profile", "device", "entry", ref, catalog.AccessFilter{AllowedLibraryIDs: []int{11, 33}}); err != nil || allowed.Body.String() != "shared-subtitle" {
				t.Fatalf("allowed: body=%q err=%v", allowed.Body.String(), err)
			}
			denied := httptest.NewRecorder()
			err := svc.ServeSubtitle(t.Context(), denied, request, 1, "profile", "device", "entry", ref, catalog.AccessFilter{AllowedLibraryIDs: []int{11}})
			if !errors.Is(err, catalog.ErrItemNotFound) || denied.Body.Len() != 0 {
				t.Fatalf("revoked: body=%q err=%v", denied.Body.String(), err)
			}
		})
	}
}
