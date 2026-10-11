package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

func nativeVersionHandler(t *testing.T, files ...*models.MediaFile) (*PlaybackHandler, map[int]int, map[int]bool) {
	t.Helper()
	byID := make(map[int]*models.MediaFile)
	for _, file := range files {
		byID[file.ID] = file
	}
	h := NewPlaybackHandler(playback.NewSessionManager(0, 0), mapPlaybackFileResolver{files: byID})
	h.ItemAccess = allowAllPlaybackItemAccess{}
	h.FileVersionFetcher = testPlaybackFileVersionFetcher{byContent: map[string][]*models.MediaFile{files[0].ContentID: files}}
	h.SettingsRepo = &mutablePlaybackSettingsV3{values: map[string]string{"allow_4k_transcode": "false"}}
	h.JWTSecret = "native-version-fixture-secret"
	service := &NativeVideoService{Files: mapPlaybackFileResolver{files: byID}, Bindings: videoBindingFixture{}, Sessions: h.sessionMgr}
	h.NativeVideo = service
	opens, revoked := make(map[int]int), make(map[int]bool)
	closed := new(int)
	service.Open = func(ctx context.Context, file *models.MediaFile) (NativeVideoFile, error) {
		opens[file.ID]++
		current, err := h.ResolveNativeVideoFile(ctx, file.ID)
		if err != nil {
			return nil, err
		}
		if revoked[current.ID] {
			return nil, catalog.ErrItemNotFound
		}
		identity := nativestorage.PlaybackIdentity{FileID: current.ID, ContentID: current.ContentID, MediaFolderID: current.MediaFolderID, FilePath: current.FilePath, Reference: storagesource.PersistedRef{Revision: "pinned-version"}}
		return &videoFileFixture{Reader: bytes.NewReader([]byte("version")), id: identity, info: mediasource.Info{Size: 7, Revision: "pinned-version"}, closed: closed}, nil
	}
	return h, opens, revoked
}

func startNativeVersion(t *testing.T, h *PlaybackHandler, request playback.StartRequestV3) playback.DecisionResponseV3 {
	t.Helper()
	scope := access.Scope{UserID: 1, ProfileID: "profile-1", PlaybackAllowed: true, LibrariesRestricted: true, AllowedLibraryIDs: []int{1}}
	ctx := access.SetScope(newAuthorizedPlaybackContext(), scope)
	w := httptest.NewRecorder()
	h.HandleStartPlayback(w, httptest.NewRequest(http.MethodPost, "/api/v1/playback/start", strings.NewReader(marshalV3StartRequest(t, request))).WithContext(ctx))
	if w.Code != http.StatusCreated {
		t.Fatalf("start status=%d body=%s", w.Code, w.Body.String())
	}
	var response playback.DecisionResponseV3
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestNativeVideoAlternateVersionStartPreservesAuthority(t *testing.T) {
	for _, kind := range []string{"allowed", "library-denied", "source-denied", "unsupported"} {
		t.Run(kind, func(t *testing.T) {
			source := v3HandlerFixtureFile(t)
			source.MediaFolderID, source.FilePath = 1, "bloem-storage:requested"
			alternate := *source
			alternate.ID, alternate.FilePath = 84, "bloem-storage:alternate"
			source.CodecVideo, source.Resolution, source.Bitrate = "hevc", "2160p", 32_000
			source.VideoTracks = []models.VideoTrack{{Codec: "hevc", Profile: "main", Level: 150, Width: 3840, Height: 2160, FrameRate: "24000/1001", Bitrate: 32_000, BitDepth: 8, VideoRange: "SDR", VideoRangeType: "SDR"}}
			h, opens, revoked := nativeVersionHandler(t, source, &alternate)
			switch kind {
			case "library-denied":
				alternate.MediaFolderID = 2
			case "source-denied":
				revoked[alternate.ID] = true
			case "unsupported":
				alternate.CodecVideo, alternate.VideoTracks = "", nil
			}
			request := v3HandlerStartRequest()
			request.QualityPreference = "auto"
			request.ClientPlaybackContext.Deliveries[playback.DeliveryClassHLSV3] = playback.DeliveryCapabilityV3{Enabled: true, SupportedOnDevice: true, Containers: []string{"hls"}, VideoCodecs: []string{"h264"}, AudioDecodeCodecs: []string{"aac"}}
			response := startNativeVersion(t, h, request)
			if kind == "allowed" {
				if response.PlaybackPlan == nil || response.PlaybackPlan.EffectiveMediaFileID != alternate.ID || opens[alternate.ID] < 2 {
					t.Fatalf("native version not planned/preflighted/bound: terminal=%+v plan=%+v opens=%v", response.Terminal, response.PlaybackPlan, opens)
				}
				live, err := h.sessionMgr.GetSession(response.SessionID)
				if err != nil || live.MediaFileID != alternate.ID {
					t.Fatalf("selected version not committed: %v %v", live, err)
				}
			} else if response.PlaybackPlan != nil || response.Terminal == nil {
				t.Fatalf("unauthorized/unsupported version became playable: %+v", response)
			}
			if kind == "library-denied" || kind == "unsupported" {
				if opens[alternate.ID] != 0 {
					t.Fatal("filtered version reached source opener")
				}
			}
		})
	}
}

func TestNativeVideoMultipartStartMapsResumeAndPreflightsTarget(t *testing.T) {
	for _, kind := range []string{"allowed", "library-denied", "source-denied"} {
		t.Run(kind, func(t *testing.T) {
			first := v3HandlerFixtureFile(t)
			first.MediaFolderID, first.FilePath = 1, "bloem-storage:first-part"
			first.PresentationKind, first.PresentationGroupKey = "multipart", "movie-parts"
			first.PresentationPartTotal, first.PresentationPartIndex, first.Duration = 2, 1, 10
			second := *first
			second.ID, second.FilePath, second.PresentationPartIndex = 84, "bloem-storage:second-part", 2
			h, opens, revoked := nativeVersionHandler(t, first, &second)
			store := newPlaybackTestStore(t)
			if err := store.SetProgress(t.Context(), "profile-1", first.ContentID, 12, 20, userstore.ProgressThresholds{}); err != nil {
				t.Fatal(err)
			}
			h.StoreProvider = testUserStoreProvider{store: store}
			switch kind {
			case "library-denied":
				second.MediaFolderID = 2
			case "source-denied":
				revoked[second.ID] = true
			}
			if kind == "source-denied" {
				request := v3HandlerStartRequest()
				ctx := access.SetScope(newAuthorizedPlaybackContext(), access.Scope{UserID: 1, ProfileID: "profile-1", PlaybackAllowed: true, AllowedLibraryIDs: []int{1}})
				w := httptest.NewRecorder()
				h.HandleStartPlayback(w, httptest.NewRequest(http.MethodPost, "/api/v1/playback/start", strings.NewReader(marshalV3StartRequest(t, request))).WithContext(ctx))
				if w.Code == http.StatusCreated || opens[second.ID] == 0 {
					t.Fatalf("target source denial bypassed: status=%d opens=%v", w.Code, opens)
				}
				return
			}
			response := startNativeVersion(t, h, v3HandlerStartRequest())
			wantID, wantPosition := second.ID, 2.0
			if kind == "library-denied" {
				wantID, wantPosition = first.ID, 0
			}
			if response.PlaybackPlan == nil || response.PlaybackPlan.EffectiveMediaFileID != wantID || response.PlaybackPlan.Timeline.PlayerStartSeconds != wantPosition {
				t.Fatalf("multipart mapping=%+v want file=%d local=%v", response, wantID, wantPosition)
			}
			if kind == "allowed" && opens[second.ID] < 2 {
				t.Fatal("selected part was not preflighted and bound")
			}
			if kind == "library-denied" && opens[second.ID] != 0 {
				t.Fatal("inaccessible part reached source opener")
			}
		})
	}
}

func TestNativeVideoVersionRetrievalDoesNotMutateRepositoryParts(t *testing.T) {
	first := v3HandlerFixtureFile(t)
	first.FilePath, first.MediaFolderID = "bloem-storage:first", 1
	first.PresentationPartTotal, first.PresentationPartIndex, first.Duration, first.PresentationGroupKey = 2, 1, 10, "parts"
	second, outside := *first, *first
	second.ID, second.PresentationPartIndex = 84, 2
	outside.ID, outside.MediaFolderID = 99, 2
	files := []*models.MediaFile{first, &outside, &second}
	h, _, _ := nativeVersionHandler(t, files...)
	if target, local, err := h.multipartResumeFileV3(t.Context(), first, 12, catalog.AccessFilter{AllowedLibraryIDs: []int{1}}); err != nil || target != &second || local != 2 {
		t.Fatalf("mapping=%v,%v,%v", target, local, err)
	}
	for i, file := range files {
		if file == nil || file.ID != []int{42, 99, 84}[i] {
			t.Fatalf("repository slice mutated at %d", i)
		}
	}
}
