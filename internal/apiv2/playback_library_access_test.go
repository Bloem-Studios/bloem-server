package apiv2

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/organizations"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

type deliveryLibraryFiles map[int]*models.MediaFile

func (f deliveryLibraryFiles) GetByID(_ context.Context, id int) (*models.MediaFile, error) {
	return f[id], nil
}

// An owned session must not bypass the current library ceiling, even when
// another file for the same catalog title remains accessible.
func TestPlaybackDeliveryRechecksLibraryAccess(t *testing.T) {
	deps, _ := catalogDeps(t)
	scope := organizations.ViewerBoundary{OrganizationID: 10, AccessRevision: 1, AllowedLibraryIDs: []int{11, 33}}.Restrict(access.Scope{})
	deps.ViewerAccess = apimw.NewViewerAccessMiddleware(policyResolver{scope: &scope})
	manager := playback.NewSessionManager(0, 0)
	files := deliveryLibraryFiles{}
	sessions := map[int]string{}
	for _, library := range []int{11, 22, 33} {
		path := filepath.Join(t.TempDir(), "movie.mp4")
		if err := os.WriteFile(path, []byte(fmt.Sprintf("library-%d", library)), 0600); err != nil {
			t.Fatal(err)
		}
		subtitle := path + ".srt"
		if err := os.WriteFile(subtitle, []byte(fmt.Sprintf("1\n00:00:01,000 --> 00:00:02,000\nlibrary-%d\n", library)), 0600); err != nil {
			t.Fatal(err)
		}
		files[library] = &models.MediaFile{ID: library, ContentID: "same-title", MediaFolderID: library, FilePath: path, SubtitleTracks: []models.SubtitleTrack{{Codec: "srt"}}, ExternalSubtitles: []models.ExternalSubtitle{{Path: subtitle, Format: "srt"}}}
		session, err := manager.StartSession(1, "p-owner", library, playback.PlayDirect, false)
		if err != nil {
			t.Fatal(err)
		}
		sessions[library] = session.ID
	}
	stream := handlers.NewStreamHandler(manager, files)
	deps.PlaybackMedia = &PlaybackMediaHandlers{Original: http.HandlerFunc(stream.HandleStream), Subtitle: http.HandlerFunc(stream.HandleSubtitle), SubtitleFonts: stream}
	h := newTestHandler(t, deps)
	for _, library := range []int{11, 22, 33} {
		for _, suffix := range []string{"", "/subtitles/0.srt"} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				t.Run(fmt.Sprintf("library-%d/%s/%s", library, method, suffix), func(t *testing.T) {
					response := do(t, h, method, Prefix+"/stream/"+sessions[library]+suffix, "", viewerHeaders())
					if library == 22 {
						requireProblem(t, response, TypeNotFound)
						return
					}
					if response.Code != 200 {
						t.Fatalf("authorized delivery: %d %s", response.Code, response.Body.String())
					}
					if method == http.MethodGet && !strings.Contains(response.Body.String(), fmt.Sprintf("library-%d", library)) {
						t.Fatalf("wrong source: %q", response.Body.String())
					}
				})
			}
		}
	}
	// Revoke the shared-library grant after the session has already served bytes.
	scope = organizations.ViewerBoundary{OrganizationID: 10, AccessRevision: 2, AllowedLibraryIDs: []int{11}}.Restrict(access.Scope{})
	for _, suffix := range []string{"", "/subtitles/0.srt", "/subtitles/1/fonts"} {
		t.Run("revoked"+suffix, func(t *testing.T) {
			requireProblem(t, do(t, h, http.MethodGet, Prefix+"/stream/"+sessions[33]+suffix, "", viewerHeaders()), TypeNotFound)
		})
	}
}

// Both live sessions and signed restart recipes must be bounded before a
// remote worker receives a manifest/segment request.
func TestTranscodeDeliveryRechecksLibraryAccess(t *testing.T) {
	const secret = "library-delivery-test"
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("worker-bytes")) }))
	defer worker.Close()
	for _, reconstructed := range []bool{false, true} {
		for _, suffix := range []string{"master.m3u8", "segment/0.ts"} {
			t.Run(fmt.Sprintf("reconstructed-%t/%s", reconstructed, suffix), func(t *testing.T) {
				deps, _ := catalogDeps(t)
				scope := access.Scope{LibrariesRestricted: true, AllowedLibraryIDs: []int{33}}
				deps.ViewerAccess = apimw.NewViewerAccessMiddleware(policyResolver{scope: &scope})
				manager := playback.NewSessionManager(0, 0)
				session := &playback.Session{ID: deliveryTestSession, UserID: 1, ProfileID: "p-owner", MediaFileID: 33, PlayMethod: playback.PlayTranscode, TranscodeNodeURL: worker.URL}
				if !reconstructed {
					manager.RegisterReconstructed(session)
				}
				card := playback.NewDirectRecipeCard(deliveryTestSession, 1, "p-owner", 33)
				card.PlayMethod = playback.PlayTranscode
				card.TranscodeNodeURL = worker.URL
				token, err := streamtoken.Sign(card.ToClaims(), secret, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				handler := handlers.NewPlaybackHandler(manager, deliveryLibraryFiles{33: &models.MediaFile{ID: 33, MediaFolderID: 33, ContentID: "same-title", Resolution: "2160p"}})
				handler.JWTSecret = secret
				deps.PlaybackMedia = &PlaybackMediaHandlers{Manifest: http.HandlerFunc(handler.HandleGetTranscodeManifest), Segment: http.HandlerFunc(handler.HandleGetTranscodeSegment)}
				h := newTestHandler(t, deps)
				path := Prefix + "/playback/transcode/" + deliveryTestSession + "/" + suffix + "?st=" + token
				response := do(t, h, http.MethodGet, path, "", viewerHeaders())
				if response.Code != 200 || response.Body.String() != "worker-bytes" {
					t.Fatalf("authorized: %d %s", response.Code, response.Body.String())
				}
				scope.AllowedLibraryIDs = []int{11}
				if reconstructed {
					_ = manager.StopSession(deliveryTestSession)
				}
				requireProblem(t, do(t, h, http.MethodGet, path, "", viewerHeaders()), TypeNotFound)
			})
		}
	}
}

func TestStreamDeliveryDoesNotReviveRevokedLibraryRecipe(t *testing.T) {
	for _, suffix := range []string{"", "/subtitles/0.srt", "/subtitles/0/fonts"} {
		t.Run(suffix, func(t *testing.T) {
			deps, _ := catalogDeps(t)
			scope := access.Scope{LibrariesRestricted: true, AllowedLibraryIDs: []int{11}}
			deps.ViewerAccess = apimw.NewViewerAccessMiddleware(policyResolver{scope: &scope})
			manager := playback.NewSessionManager(0, 0)
			stream := handlers.NewStreamHandler(manager, deliveryLibraryFiles{33: &models.MediaFile{ID: 33, MediaFolderID: 33, ContentID: "same-title"}})
			stream.JWTSecret = "revoked-recipe-test"
			stream.TM = playback.NewTranscodeManager()
			stream.TM.Sessions = manager
			card := playback.NewDirectRecipeCard(deliveryTestSession, 1, "p-owner", 33)
			token, err := streamtoken.Sign(card.ToClaims(), stream.JWTSecret, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			deps.PlaybackMedia = &PlaybackMediaHandlers{Original: http.HandlerFunc(stream.HandleStream), Subtitle: http.HandlerFunc(stream.HandleSubtitle), SubtitleFonts: stream}
			requireProblem(t, do(t, newTestHandler(t, deps), http.MethodGet, Prefix+"/stream/"+deliveryTestSession+suffix+"?st="+token, "", viewerHeaders()), TypeNotFound)
			if _, err := manager.GetSession(deliveryTestSession); err == nil {
				t.Fatal("revoked recipe consumed a playback session slot")
			}
		})
	}
}
