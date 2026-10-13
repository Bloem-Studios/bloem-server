package handlers

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/livetv"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/themesongs"
)

func TestNativeReaderBrowseOnly(t *testing.T) {
	for _, source := range []string{"local", "native"} {
		for _, format := range []string{"epub", "pdf", "cbz", "cbr", "mobi"} {
			for _, method := range []string{"GET", "HEAD"} {
				t.Run(source+"/"+format+"/"+method, func(t *testing.T) {
					h, opener, file := nativeReaderTestSetup(t)
					file.Container = format
					if source == "local" {
						file.FilePath = filepath.Join(t.TempDir(), "book."+format)
						if err := os.WriteFile(file.FilePath, []byte("0123456789"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					for _, conditional := range []bool{false, true} {
						r := nativeReadRequest(method)
						r = r.WithContext(access.SetScope(r.Context(), access.Scope{LiveTVAllowed: true}))
						r.Header.Set("Range", "bytes=2-4")
						if conditional {
							r.Header.Set("If-None-Match", "*")
						}
						w := httptest.NewRecorder()
						h.HandleReadFile(w, r)
						if w.Code != 403 || opener.calls != 0 || w.Header().Get("Content-Range") != "" || w.Header().Get("ETag") != "" || w.Header().Get("Content-Disposition") != "" {
							t.Fatalf("denied reader: %d headers=%v opens=%d body=%s", w.Code, w.Header(), opener.calls, w.Body)
						}
					}
				})
			}
		}
	}
}

func TestLiveTVStartBrowseOnly(t *testing.T) {
	for _, native := range []bool{false, true} {
		h := NewBloemLiveTVHandler(livetv.NewService(nil), "test-secret")
		h.native = native
		r := httptest.NewRequest("POST", "/livetv/channels/1/session", nil)
		r = r.WithContext(access.SetScope(r.Context(), access.Scope{LiveTVAllowed: true}))
		w := httptest.NewRecorder()
		h.HandleStartChannelSession(w, r)
		if w.Code != 403 {
			t.Fatalf("native=%v status=%d body=%s", native, w.Code, w.Body)
		}
	}
}

func TestThemeBrowseOnlyAndRevocation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "theme.mp3")
	if err := os.WriteFile(path, []byte("theme bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	store := &themeFileFixture{file: themesongs.File{Song: themesongs.Song{ID: "7", Container: "mp3"}, OwnerPath: dir, Path: path, Size: info.Size(), Modified: info.ModTime().Truncate(time.Microsecond)}}
	viewer := &socketViewerFixture{scope: access.Scope{UserID: 7, ProfileID: "profile", ProfileVerified: true, PlaybackAllowed: true}}
	h := &BloemThemeSongsHandler{ThemeSongsHandler: &ThemeSongsHandler{Service: themesongs.NewService(store, "test secret"), Sessions: &socketSessionFixture{valid: true}, Users: &socketUserFixture{user: models.User{ID: 7, Enabled: true, AccessPolicyRevision: 2}}, Resolver: viewer}}
	identity := themesongs.Identity{UserID: 7, ProfileID: "profile", SessionID: "session", PolicyRevision: 2}
	denied := access.SetScope(context.Background(), access.Scope{})
	grant, err := h.Authorize(denied, identity, "movie", "7", catalog.AccessFilter{PlaybackDenied: true}, nil, time.Now().Add(time.Minute))
	if err == nil || grant.Grant != "" || grant.URL != "" {
		t.Error("browse-only minted theme access")
	}
	allowed := access.SetScope(context.Background(), viewer.scope)
	grant, err = h.Authorize(allowed, identity, "movie", "7", catalog.AccessFilter{}, nil, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	viewer.scope.PlaybackAllowed = false
	_, _, f, err := h.OpenGrant(context.Background(), "movie", "7", grant.Grant)
	if f != nil {
		_ = f.Close()
	}
	if err == nil || f != nil {
		t.Fatal("revoked playback opened old theme grant")
	}
}

// Playback and download are independent transport decisions. Neither changes
// which catalog libraries remain browsable.
func TestBloemContentFilterPreservesBrowseAndDownloadOverride(t *testing.T) {
	for _, play := range []bool{false, true} {
		for _, download := range []bool{false, true} {
			base := catalog.AccessFilter{AllowedLibraryIDs: []int{3}, DisabledLibraryIDs: []int{4}, ProfileID: "profile", DeviceID: "reader"}
			ctx := access.SetScope(t.Context(), access.Scope{PlaybackAllowed: play, DownloadAllowed: download})
			got := BloemContentAccessFilter(ctx, base)
			if got.PlaybackDenied == play || got.DownloadDenied == download || got.AllowedLibraryIDs[0] != 3 || got.DisabledLibraryIDs[0] != 4 || got.ProfileID != "profile" || got.DeviceID != "reader" {
				t.Fatalf("filter: %+v", got)
			}
		}
	}
}
