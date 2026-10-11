package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/scanner"
)

// An unsupported playback request must not make an available storage ebook
// missing or send its opaque catalog identity to filesystem probe repair.
func TestNativePlaybackStartDoesNotMarkStorageEbookMissing(t *testing.T) {
	for _, surface := range []string{"v1", "v2"} {
		for _, location := range []struct{ name, path string }{{"catalog-key", "bloem-storage:" + strings.Repeat("a", 64)}, {"malformed-key", "bloem-storage:"}} {
			t.Run(surface+"/"+location.name, func(t *testing.T) {
				manager := playback.NewSessionManager(0, 0)
				file := &models.MediaFile{ID: 42, ContentID: "ebook-1", BaseType: "ebook", FilePath: location.path, Container: "epub", FileSize: 1024, Duration: 10, ProbeSource: "storage"}
				marker := &recordingMissingMarker{}
				probe := &blockingProbeEnsurer{t: t}
				handler := NewPlaybackHandler(manager, testPlaybackFileResolver{file: file})
				handler.ItemAccess = allowAllPlaybackItemAccess{}
				handler.SettingsRepo = &mutablePlaybackSettingsV3{values: map[string]string{}}
				handler.MissingMarker = marker
				handler.ProbeEnsurer = probe
				if surface == "v1" {
					request := httptest.NewRequest(http.MethodPost, "/api/v1/playback/start",
						strings.NewReader(marshalV3StartRequest(t, v3HandlerStartRequest()))).WithContext(newAuthorizedPlaybackContext())
					response := httptest.NewRecorder()
					handler.HandleStartPlayback(response, request)
					if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"error":"not_found"`) {
						t.Errorf("status = %d, body = %s", response.Code, response.Body.String())
					}
				} else {
					handler.InstallationID = serviceInstallation
					_, err := handler.StartPlaybackV2(newAuthorizedPlaybackContext(), PlaybackCaller{UserID: 1, ProfileID: "profile-1", InstallationID: serviceInstallation}, v3HandlerStartRequest())
					assertPlaybackOperationError(t, err, http.StatusNotFound, "not_found")
				}
				if file.MissingSince != nil || len(marker.ids) != 0 {
					t.Errorf("unsupported playback marked storage ebook missing: missing=%v, marked=%v", file.MissingSince != nil, marker.ids)
				}
				if calls := probe.calls(); calls != 0 {
					t.Errorf("filesystem probe repair received storage identity %d times", calls)
				}
				if sessions := manager.AllSessions(); len(sessions) != 0 {
					t.Errorf("unsupported playback created %d sessions", len(sessions))
				}
			})
		}
	}
}

func TestNativePlaybackStreamSourcesNeverMarkMissing(t *testing.T) {
	for _, endpoint := range []string{"direct-get", "direct-head", "subtitle", "fonts"} {
		t.Run(endpoint, func(t *testing.T) {
			file := &models.MediaFile{ID: 42, ContentID: "ebook-1", FilePath: "bloem-storage:available-ebook", Container: "epub", SubtitleTracks: []models.SubtitleTrack{{Index: 1, Codec: "ass"}}}
			manager := playback.NewSessionManager(0, 0)
			session, err := manager.StartSession(1, "profile-1", file.ID, playback.PlayDirect, false)
			if err != nil {
				t.Fatal(err)
			}
			handler := NewStreamHandler(manager, testPlaybackFileResolver{file: file})
			marker := &recordingMissingMarker{}
			handler.MissingMarker = marker
			response := httptest.NewRecorder()
			params := map[string]string{"session_id": session.ID, "track": "0.ass"}
			switch endpoint {
			case "subtitle":
				handler.HandleSubtitle(response, playbackTestRequest(http.MethodGet, "/api/v1/stream/"+session.ID+"/subtitles/0.ass", nil, params))
			case "fonts":
				handler.HandleSubtitleFonts(response, playbackTestRequest(http.MethodGet, "/api/v1/stream/"+session.ID+"/subtitles/0/fonts", nil, params))
			default:
				method := http.MethodGet
				if endpoint == "direct-head" {
					method = http.MethodHead
				}
				handler.HandleStream(response, playbackTestRequest(method, "/api/v1/stream/"+session.ID, nil, params))
			}
			if response.Code != http.StatusNotFound {
				t.Errorf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if file.MissingSince != nil || len(marker.ids) != 0 {
				t.Errorf("unsupported delivery marked native file missing: missing=%v, marked=%v", file.MissingSince != nil, marker.ids)
			}
		})
	}
}

func TestNativePlaybackPreferredEditionSkipsStorageIdentity(t *testing.T) {
	native := &models.MediaFile{ID: 42, ContentID: "movie-1", FilePath: "bloem-storage:edition"}
	local := v3HandlerFixtureFile(t)
	local.ID = 84
	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0), mapPlaybackFileResolver{files: map[int]*models.MediaFile{42: native, 84: local}})
	if file, err := handler.loadFileByPreferredID(t.Context(), 42, 0); file != nil || !errors.Is(err, scanner.ErrFileNotFound) {
		t.Errorf("native requested edition = %v, %v; want typed not found", file, err)
	}
	if file, err := handler.loadFileByPreferredID(t.Context(), 42, 84); err != nil || file != local {
		t.Errorf("fallback edition = %v, %v; want local edition", file, err)
	}
}

func TestNativePlaybackAlternatesFilterContentAndEpisodeSources(t *testing.T) {
	for _, episode := range []bool{false, true} {
		t.Run(map[bool]string{false: "content", true: "episode"}[episode], func(t *testing.T) {
			source := v3HandlerFixtureFile(t)
			local := *source
			local.ID = 84
			native := local
			native.ID, native.FilePath = 99, "bloem-storage:alternate"
			malformed := native
			malformed.ID, malformed.FilePath = 100, "bloem-storage:"
			files := []*models.MediaFile{source, &native, &local, &malformed}
			handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
			if episode {
				source.EpisodeID = "episode-1"
				handler.FileVersionFetcher = testPlaybackFileVersionFetcher{byEpisode: map[string][]*models.MediaFile{"episode-1": files}}
			} else {
				handler.FileVersionFetcher = testPlaybackFileVersionFetcher{byContent: map[string][]*models.MediaFile{source.ContentID: files}}
			}
			got, err := handler.findAlternateFiles(t.Context(), source, catalog.AccessFilter{})
			if err != nil || len(got) != 1 || got[0] != &local {
				t.Errorf("alternates = %v, %v; want only local edition", got, err)
			}
			if len(files) != 4 || files[1] != &native || files[3] != &malformed {
				t.Fatal("filter mutated the repository's candidate list")
			}
		})
	}
}

func TestNativePlaybackMultipartResumeRefusesStoragePart(t *testing.T) {
	for _, native := range []bool{true, false} {
		t.Run(map[bool]string{true: "native-part", false: "local-parts"}[native], func(t *testing.T) {
			first := v3HandlerFixtureFile(t)
			first.PresentationPartTotal, first.PresentationPartIndex, first.Duration = 2, 1, 10
			first.PresentationGroupKey = "book-parts"
			second := *first
			second.ID, second.PresentationPartIndex = 84, 2
			if native {
				second.FilePath = "bloem-storage:part-two"
			}
			handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
			handler.FileVersionFetcher = testPlaybackFileVersionFetcher{byContent: map[string][]*models.MediaFile{first.ContentID: {first, &second}}}
			file, position, err := handler.multipartResumeFileV3(t.Context(), first, 12, catalog.AccessFilter{})
			if native {
				if file != nil || err == nil {
					t.Errorf("resume selected native part: file=%v position=%v error=%v", file, position, err)
				}
			} else if err != nil || file != &second || position != 2 {
				t.Errorf("local resume = %v, %v, %v; want second part at 2 seconds", file, position, err)
			}
		})
	}
}

func TestNativePlaybackReplanDoesNotMarkNativeSourceMissing(t *testing.T) {
	file := v3HandlerFixtureFile(t)
	manager := playback.NewSessionManager(0, 0)
	handler := NewPlaybackHandler(manager, testPlaybackFileResolver{file: file})
	handler.ItemAccess = allowAllPlaybackItemAccess{}
	handler.SettingsRepo = &mutablePlaybackSettingsV3{values: map[string]string{}}
	marker := &recordingMissingMarker{}
	handler.MissingMarker = marker
	start := v3HandlerStartRequest()
	response := httptest.NewRecorder()
	handler.HandleStartPlayback(response, httptest.NewRequest(http.MethodPost, "/api/v1/playback/start", strings.NewReader(marshalV3StartRequest(t, start))).WithContext(newAuthorizedPlaybackContext()))
	var started playback.DecisionResponseV3
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &started) != nil || started.PlaybackPlan == nil {
		t.Fatalf("local start = %d %s", response.Code, response.Body.String())
	}
	file.FilePath = "bloem-storage:retained-source"
	key := playback.PlanAttemptKeyV3(*started.PlaybackPlan, start.ClientPlaybackContext.Output.OutputContextID, nil)
	body, err := json.Marshal(playback.ReplanRequestV3{ProtocolVersion: playback.ProtocolV3, Operation: playback.ReplanOperationSeekReanchorV3,
		PlaybackAttemptID: start.PlaybackAttemptID, ReplanRequestID: "native-source-replan-0001", FailedPlanID: started.PlaybackPlan.PlanID,
		PlanAttemptID: "native-source-plan-attempt-0001", PlanAttemptKey: key, AttemptedPlanKeys: []string{key}, AttemptCount: 1,
		PositionSeconds: 2, SelectedTracks: started.PlaybackPlan.SelectedTracks, QualityPreference: start.QualityPreference,
		Capabilities: start.Capabilities, ClientPlaybackContext: start.ClientPlaybackContext})
	if err != nil {
		t.Fatal(err)
	}
	request := withPlaybackRouteParam(httptest.NewRequest(http.MethodPost, "/api/v1/playback/"+started.SessionID+"/replan", strings.NewReader(string(body))).WithContext(newAuthorizedPlaybackContext()), "session_id", started.SessionID)
	response = httptest.NewRecorder()
	handler.HandleReplanPlaybackV3(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "source_unavailable") {
		t.Errorf("replan = %d %s; want source-unavailable terminal", response.Code, response.Body.String())
	}
	if file.MissingSince != nil || len(marker.ids) != 0 {
		t.Errorf("replan marked native file missing: missing=%v, marked=%v", file.MissingSince != nil, marker.ids)
	}
}

func TestNativePlaybackGuardPreservesFilesystemStartAndRanges(t *testing.T) {
	file := v3HandlerFixtureFile(t)
	file.FilePath = writePlaybackTestMediaFile(t, "bloem-storage:local.mp4")
	manager := playback.NewSessionManager(0, 0)
	handler := NewPlaybackHandler(manager, testPlaybackFileResolver{file: file})
	handler.ItemAccess = allowAllPlaybackItemAccess{}
	handler.SettingsRepo = &mutablePlaybackSettingsV3{values: map[string]string{}}
	response := httptest.NewRecorder()
	handler.HandleStartPlayback(response, httptest.NewRequest(http.MethodPost, "/api/v1/playback/start", strings.NewReader(marshalV3StartRequest(t, v3HandlerStartRequest()))).WithContext(newAuthorizedPlaybackContext()))
	var started playback.DecisionResponseV3
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &started) != nil || started.PlaybackPlan == nil {
		t.Fatalf("local start = %d %s", response.Code, response.Body.String())
	}
	stream := NewStreamHandler(manager, testPlaybackFileResolver{file: file})
	request := playbackTestRequest(http.MethodGet, "/api/v1/stream/"+started.SessionID, nil, map[string]string{"session_id": started.SessionID})
	request.Header.Set("Range", "bytes=1-3")
	response = httptest.NewRecorder()
	stream.HandleStream(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != "ide" {
		t.Fatalf("local range = %d %q; want 206 and ide", response.Code, response.Body.String())
	}
}

func TestNativePlaybackGuardPreservesLookupErrors(t *testing.T) {
	want := errors.New("repository unavailable")
	for _, test := range []struct {
		resolver FilePathResolver
		want     error
	}{{errStreamFileResolver{err: want}, want}, {mapPlaybackFileResolver{}, nil}} {
		for _, build := range []func(FilePathResolver) FilePathResolver{
			func(r FilePathResolver) FilePathResolver {
				return NewPlaybackHandler(playback.NewSessionManager(0, 0), r).fileResolver
			},
			func(r FilePathResolver) FilePathResolver {
				return NewStreamHandler(playback.NewSessionManager(0, 0), r).fileResolver
			},
		} {
			file, err := build(test.resolver).GetByID(context.Background(), 42)
			if file != nil || !errors.Is(err, test.want) {
				t.Fatalf("lookup = %v, %v; want nil file and %v", file, err, test.want)
			}
		}
	}
}
