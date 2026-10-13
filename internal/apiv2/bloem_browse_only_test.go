package apiv2

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	catalogpkg "github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/themesongs"
)

type browseOnlyViewer struct{ allowed bool }

func (v browseOnlyViewer) Resolve(ctx context.Context, in access.ResolveInput) (access.Scope, error) {
	scope, err := fakeResolver{}.Resolve(ctx, in)
	scope.PlaybackAllowed = v.allowed
	scope.DownloadAllowed = v.allowed
	return scope, err
}
func TestBloemBrowseOnlyV2Content(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, allowed := range []bool{false, true} {
		tp := &fakeTrickplay{}
		theme := &fakeThemeSongs{}
		converter := &ebookFileConverter{result: path}
		checker := &ebookFileAccess{}
		reader := handlers.NewEbookReaderHandler(&handlers.MediaFileAuthorizer{FileResolver: ebookFileResolver{file: &models.MediaFile{ID: 42, ContentID: "book", BaseType: "ebook", Container: "mobi", FilePath: path}}, ItemAccess: checker})
		reader.Conversion = &handlers.EbookConversion{Converter: converter, Enabled: func(context.Context) bool { return true }}
		deps, _ := catalogDeps(t)
		deps.Watch = &fakeWatch{}
		deps.Trickplay = tp
		deps.ThemeSongs = theme
		deps.EbookFiles = handlers.NewNativeEbookFileService(reader, nil)
		deps.ViewerAccess = apimw.NewViewerAccessMiddleware(browseOnlyViewer{allowed: allowed})
		h := newTestHandler(t, deps)
		owner := themeSongHeaders()
		for _, tc := range []struct{ method, path string }{
			{"GET", "/api/v2/watch/movie:heat-1995/trickplay?file_id=42"},
			{"POST", "/api/v2/catalog/items/movie:heat-1995/themes/7/playback"},
			{"GET", "/api/v2/ebooks/book/files/42/read"},
			{"HEAD", "/api/v2/ebooks/book/files/42/read"},
		} {
			rec := do(t, h, tc.method, tc.path, "", with(owner, "Range", "bytes=2-4"))
			if !allowed {
				if rec.Code != http.StatusForbidden {
					t.Errorf("%s %s: %d %s", tc.method, tc.path, rec.Code, rec.Body)
				}
			} else if rec.Code != 200 && rec.Code != 206 {
				t.Errorf("allowed %s: %d %s", tc.path, rec.Code, rec.Body)
			}
		}
		rec := do(t, h, "GET", "/api/v2/catalog/items/movie:heat-1995", "", owner)
		if rec.Code != 200 {
			t.Fatalf("theme descriptions inaccessible: %d %s", rec.Code, rec.Body)
		}
		if !allowed && (len(tp.calls) != 0 || theme.identity.UserID != 0 || converter.calls != 0) {
			t.Fatal("denied request minted access or converted content")
		}
	}
}

type themePlaybackViewer struct{ allowed bool }

func (v *themePlaybackViewer) Resolve(_ context.Context, in access.ResolveInput) (access.Scope, error) {
	return access.Scope{UserID: in.UserID, ProfileID: in.ProfileID, ProfileVerified: true, PlaybackAllowed: v.allowed}, nil
}

type themePlaybackSessions struct{}

func (themePlaybackSessions) IsValid(context.Context, string) (bool, error) { return true, nil }

type themePlaybackFiles struct{ file themesongs.File }

func (f themePlaybackFiles) Resolve(_ context.Context, id string, _ bool, _ catalogpkg.AccessFilter) (string, []themesongs.File, error) {
	return id, []themesongs.File{f.file}, nil
}
func TestBloemThemeGrantHTTPRevocation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "theme.mp3")
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file := themesongs.File{Song: themesongs.Song{ID: "7", Container: "mp3"}, OwnerPath: dir, Path: path, Size: stat.Size(), Modified: stat.ModTime().Truncate(time.Microsecond)}
	viewer := &themePlaybackViewer{allowed: true}
	service := &handlers.BloemThemeSongsHandler{ThemeSongsHandler: &handlers.ThemeSongsHandler{Service: themesongs.NewService(themePlaybackFiles{file}, "test secret"), Sessions: themePlaybackSessions{}, Users: fakeUsers{users: map[int]*models.User{1: {ID: 1, Enabled: true, AccessPolicyRevision: 2}}}, Resolver: viewer}}
	identity := themesongs.Identity{UserID: 1, ProfileID: "p-owner", SessionID: "session", PolicyRevision: 2}
	grant, err := service.Authorize(t.Context(), identity, "movie:heat-1995", "7", catalogpkg.AccessFilter{}, nil, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	deps, _ := catalogDeps(t)
	deps.ThemeSongs = service
	h := newTestHandler(t, deps)
	target := "/api/v2/catalog/items/movie:heat-1995/themes/7/audio?token=" + url.QueryEscape(grant.Grant)
	rec := do(t, h, "GET", target, "", map[string]string{"Range": "bytes=2-4"})
	if rec.Code != 206 || rec.Body.String() != "234" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	viewer.allowed = false
	for _, delivery := range []themesongs.Delivery{themesongs.DeliveryOriginal, themesongs.DeliveryConverted} {
		token, _, err := service.Mint(identity, "movie:heat-1995", file, delivery, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		for _, method := range []string{"GET", "HEAD"} {
			target = "/api/v2/catalog/items/movie:heat-1995/themes/7/audio?token=" + url.QueryEscape(token)
			rec = do(t, h, method, target, "", map[string]string{"Range": "bytes=2-4", "If-None-Match": "*"})
			if rec.Code != 401 || rec.Header().Get("Content-Range") != "" || rec.Header().Get("ETag") != "" || strings.Contains(rec.Body.String(), "234") {
				t.Fatalf("revoked %s %s: %d %v %s", delivery, method, rec.Code, rec.Header(), rec.Body)
			}
		}
	}
}
