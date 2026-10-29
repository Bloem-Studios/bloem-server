package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func TestNativeAudioSupportedAdmission(t *testing.T) {
	for _, container := range []string{"mp4", "m4a", "m4b", "mp3", "flac", "ogg", "opus", "wav"} {
		f := &models.MediaFile{FilePath: "bloem-storage:track", BaseType: "audiobook", Container: container, CodecAudio: "aac"}
		if !NativeAudioSupported(f) || !NativeVideoSupported(f) {
			t.Fatalf("container %s refused", container)
		}
		f.CodecVideo = "h264"
		if NativeAudioSupported(f) {
			t.Fatal("video admitted as audio")
		}
	}
	f := &models.MediaFile{FilePath: "/local/book.m4b", BaseType: "audiobook", Container: "mp4", CodecAudio: "aac"}
	if NativeAudioSupported(f) {
		t.Fatal("local track admitted as native")
	}
}

func TestNativeAudioRefusesNonOriginalPlans(t *testing.T) {
	file := &models.MediaFile{FilePath: "bloem-storage:track", BaseType: "audiobook", Container: "mp3", CodecAudio: "mp3"}
	h := &PlaybackHandler{}
	for _, delivery := range []playback.DeliveryV3{playback.DeliveryRemuxProgressiveV3, playback.DeliveryRemuxHLSV3, playback.DeliveryTranscodeHLSV3} {
		result := playback.PlannerResultV3{Plan: &playback.PlanV3{Delivery: delivery}}
		_, err := h.prepareNativeVideoTransportV3(httptest.NewRequest("POST", "/", nil), nil, file, result, mediaAuthModeV3{}, config.PlaybackRoutingPolicy{})
		if err == nil || err.reason != routeCapabilityUnavailableReasonV3 || err.retryable {
			t.Fatalf("delivery=%s error=%+v", delivery, err)
		}
		if _, err := h.nativeVideoTimelineV3(context.Background(), nil, file, result); err == nil || err.reason != routeCapabilityUnavailableReasonV3 {
			t.Fatalf("timeline refusal=%+v", err)
		}
	}
	if _, _, err := (&NativeVideoService{}).InputSource(context.Background(), &playback.Session{}, file); err == nil {
		t.Fatal("audio entered FFmpeg")
	}
}

func TestNativeAudioStartIsPinnedToRequestedFile(t *testing.T) {
	s, f, _, _, _ := nativeVideoFixture(t)
	f.BaseType = "audiobook"
	f.CodecAudio = "mp3"
	f.Container = "mp3"
	f.VideoTracks = nil
	s.AudioReady = true
	s.Presentation = func(context.Context, *models.MediaFile) error { return nil }
	h := &PlaybackHandler{NativeVideo: s}
	req := playback.StartRequestV3{FileID: f.ID}
	h.bloemNegotiateClientFeaturesV3(context.Background(), &req)
	if req.AllowAlternateVersions == nil || *req.AllowAlternateVersions {
		t.Fatal("persisted start allows alternates")
	}
	r := httptest.NewRequest("POST", "/", nil).WithContext(withNativeVideoSnapshots(context.Background()))
	if err := h.nativeVideoPreflight(r, f); err != nil {
		t.Fatal(err)
	}
	local := &models.MediaFile{ID: 100, FilePath: "/local/book.m4b", BaseType: "audiobook", Container: "mp4", CodecAudio: "aac"}
	if err := h.nativeVideoPreflight(r, local); !errors.Is(err, catalog.ErrItemNotFound) {
		t.Fatalf("alternate admitted=%v", err)
	}
}

func TestNativeAudioServeRangesWithAudioContentType(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		s, f, session, _, _ := nativeVideoFixture(t)
		f.BaseType = "audiobook"
		f.CodecAudio = "mp3"
		f.Container = "mp3"
		f.VideoTracks = nil
		s.AudioReady = true
		if err := s.Bind(context.Background(), session, f); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, "/stream", nil)
		r.Header.Set("Range", "bytes=3-6")
		w := httptest.NewRecorder()
		if err := s.Serve(w, r, session, f); err != nil {
			t.Fatal(err)
		}
		if w.Code != 206 || w.Header().Get("Content-Type") != "audio/mpeg" {
			t.Fatalf("status=%d headers=%v", w.Code, w.Header())
		}
		if method == http.MethodGet && w.Body.String() != "3456" {
			t.Fatalf("body=%s", w.Body.String())
		}
		if method == http.MethodHead && w.Body.Len() != 0 {
			t.Fatal("HEAD returned bytes")
		}
	}
}

func TestNativeAudioExcludedFromVersionCandidates(t *testing.T) {
	local := &models.MediaFile{ID: 1, FilePath: "/local/book.m4b", BaseType: "audiobook", Container: "mp4", CodecAudio: "aac"}
	audio := &models.MediaFile{ID: 2, FilePath: "bloem-storage:audio", BaseType: "audiobook", Container: "mp3", CodecAudio: "mp3"}
	video := &models.MediaFile{ID: 3, FilePath: "bloem-storage:video", BaseType: "movie", Container: "mp4", CodecVideo: "h264"}
	files := nativeVideoVersionCandidates([]*models.MediaFile{local, audio, video})
	if len(files) != 2 || files[0] != local || files[1] != video {
		t.Fatalf("candidates=%v", files)
	}
}

func TestNativeAudioDisabledStaysHidden(t *testing.T) {
	s, f, _, _, _ := nativeVideoFixture(t)
	f.BaseType = "audiobook"
	f.CodecAudio = "mp3"
	f.Container = "mp3"
	f.VideoTracks = nil
	h := &PlaybackHandler{NativeVideo: s}
	r := httptest.NewRequest("POST", "/", nil).WithContext(withNativeVideoSnapshots(context.Background()))
	var missing *playbackFilePreflightError
	if err := h.nativeVideoPreflight(r, f); !errors.As(err, &missing) || !missing.notFound {
		t.Fatalf("disabled preflight=%v", err)
	}
	s.AudioReady = true
	s.Presentation = func(context.Context, *models.MediaFile) error { return catalog.ErrItemNotFound }
	if err := h.nativeVideoPreflight(r, f); !errors.As(err, &missing) || !missing.notFound {
		t.Fatalf("broken presentation=%v", err)
	}
}

func TestNativeAudioPublicCapabilityToken(t *testing.T) {
	h := NewBloemSystemHandler(nil)
	for _, ready := range []bool{false, true} {
		h.SetNativeAudioReadiness(func(context.Context) bool { return ready })
		w := httptest.NewRecorder()
		h.HandleCapabilities(w, httptest.NewRequest("GET", "/", nil))
		if strings.Contains(w.Body.String(), "native_audiobook_presentations_v1") != ready {
			t.Fatalf("ready=%v body=%s", ready, w.Body.String())
		}
	}
}

func TestNativeAudioReplanKeepsBoundFile(t *testing.T) {
	for _, multipart := range []bool{false, true} {
		t.Run(fmt.Sprintf("multipart=%v", multipart), func(t *testing.T) {
			a := newNativeAcceptance(t, "mp4")
			a.file.BaseType = "audiobook"
			a.file.CodecVideo = ""
			a.file.VideoTracks = nil
			a.service.AudioReady = true
			a.service.Presentation = func(context.Context, *models.MediaFile) error { return nil }
			if multipart {
				a.file.PresentationKind = "multipart"
				a.file.PresentationGroupKey = "bloem-storage:audiobook:fixture"
				a.file.PresentationPartIndex = 1
				a.file.PresentationPartTotal = 2
			}
			local := &models.MediaFile{ID: 43, ContentID: a.file.ContentID, FilePath: "/fixture/local.m4b", BaseType: "audiobook", Container: "mp4", CodecAudio: "aac", Duration: 8}
			localPart := *local
			localPart.ID = 44
			localPart.PresentationKind = "multipart"
			localPart.PresentationGroupKey = "local-parts"
			localPart.PresentationPartIndex = 1
			localPart.PresentationPartTotal = 2
			a.h.FileVersionFetcher = testPlaybackFileVersionFetcher{byContent: map[string][]*models.MediaFile{a.file.ContentID: {a.file, local, &localPart}}}
			start := v3HandlerStartRequest()
			start.PlaybackAttemptID = "native-audio-fixed-attempt"
			start.QualityPreference = "auto"
			start.ClientFeatures = append(start.ClientFeatures, playback.FeatureHeaderAuthenticatedMediaV3)
			w := httptest.NewRecorder()
			a.h.HandleStartPlayback(w, httptest.NewRequest("POST", "/api/v1/playback/start", strings.NewReader(marshalV3StartRequest(t, start))).WithContext(newAuthorizedPlaybackContext()))
			var started playback.DecisionResponseV3
			if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &started) != nil || started.PlaybackPlan == nil || started.PlaybackPlan.Delivery != playback.DeliveryOriginalHTTPV3 {
				t.Fatalf("start=%d %s", w.Code, w.Body.String())
			}
			t.Cleanup(func() { _ = a.h.stopPlaybackSessionByID(newAuthorizedPlaybackContext(), started.SessionID, "") })
			record, err := a.h.PlanStoreV3.GetAttempt(t.Context(), started.SessionID)
			if err != nil || record.NormalizedRequest.AllowAlternateVersions == nil || record.NormalizedRequest.AllowsAlternateVersions() {
				t.Fatalf("stored request=%+v err=%v", record, err)
			}
			key := playback.PlanAttemptKeyV3(*started.PlaybackPlan, start.ClientPlaybackContext.Output.OutputContextID, nil)
			replanned := postPlaybackReplanV3(t, a.h, started.SessionID, playback.ReplanRequestV3{ProtocolVersion: playback.ProtocolV3, Operation: playback.ReplanOperationFailureRecoveryV3, PlaybackAttemptID: start.PlaybackAttemptID, ReplanRequestID: "native-audio-recovery-0001", FailedPlanID: started.PlaybackPlan.PlanID, PlanAttemptID: "native-audio-plan-0001", PlanAttemptKey: key, AttemptedPlanKeys: []string{key}, AttemptCount: 1, QualityPreference: "auto", SelectedTracks: started.PlaybackPlan.SelectedTracks, Failure: playback.FailureV3{Classification: "playback_error"}, Capabilities: start.Capabilities, ClientPlaybackContext: start.ClientPlaybackContext})
			if replanned.PlaybackPlan != nil {
				t.Fatalf("replan produced delivery=%s", replanned.PlaybackPlan.Delivery)
			}
			record, err = a.h.PlanStoreV3.GetAttempt(t.Context(), started.SessionID)
			if err != nil || record.EffectiveMediaFileID != a.file.ID || record.NormalizedRequest.AllowsAlternateVersions() {
				t.Fatalf("replan changed file: %+v err=%v", record, err)
			}
			session, err := a.sessions.GetSession(started.SessionID)
			if err == nil && session.MediaFileID != a.file.ID {
				t.Fatalf("session changed file=%d", session.MediaFileID)
			}
		})
	}
}

func TestNativeAudioRevisionChangeDuringRangeReadAborts(t *testing.T) {
	s, file, session, _, _ := nativeVideoFixture(t)
	file.BaseType = "audiobook"
	file.CodecAudio = "mp3"
	file.Container = "mp3"
	file.VideoTracks = nil
	s.AudioReady = true
	if err := s.Bind(t.Context(), session, file); err != nil {
		t.Fatal(err)
	}
	changed := false
	s.AuthorizeBound = func(context.Context, NativeVideoBinding) error {
		if changed {
			return storagesource.ErrReferenceConflict
		}
		return nil
	}
	open := s.Open
	s.Open = func(ctx context.Context, file *models.MediaFile) (NativeVideoFile, error) {
		reader, err := open(ctx, file)
		if err != nil {
			return nil, err
		}
		return &videoBoundReadFixture{NativeVideoFile: reader, read: func() { changed = true }}, nil
	}
	reader, _, err := s.OpenBound(t.Context(), session, file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	buffer := bytes.Repeat([]byte{0xff}, 4)
	if n, err := reader.ReadAt(buffer, 2); n != 0 || !errors.Is(err, storagesource.ErrReferenceConflict) || !bytes.Equal(buffer, make([]byte, 4)) {
		t.Fatalf("changed revision n=%d err=%v bytes=%x", n, err, buffer)
	}
	changed = false
	if n, err := reader.Read(buffer); n != 0 || !errors.Is(err, storagesource.ErrReferenceConflict) {
		t.Fatalf("non-sticky failure n=%d err=%v", n, err)
	}
}

type audioCapabilityFixture struct{ audio bool }

func (audioCapabilityFixture) NativeStorageReady(context.Context) bool           { return true }
func (audioCapabilityFixture) LibraryLocationBrowsingReady(context.Context) bool { return true }
func (audioCapabilityFixture) LibraryLocationEditingReady(context.Context) bool  { return true }
func (c audioCapabilityFixture) NativeAudioReady(context.Context) bool           { return c.audio }
func TestNativeAudioCapabilitySourceCombinations(t *testing.T) {
	for _, ready := range []bool{false, true} {
		h := &BloemNativeStorageManagementHandler{Capabilities: audioCapabilityFixture{audio: ready}}
		caps := h.nativeStorageCapabilities(t.Context())
		if caps["native_audio_libraries"] != ready || caps["native_audio_playback"] != ready {
			t.Fatalf("audio flags=%+v", caps)
		}
		combinations := caps["supported_source_media"].([]nativeStorageSourceMedia)
		for _, kind := range []string{"s3", "bookwarehouse"} {
			found, advertised := false, false
			for _, combination := range combinations {
				if combination.SourceKind == kind {
					found = true
					for _, media := range combination.MediaKinds {
						if media == "audiobooks" {
							advertised = true
						}
					}
				}
			}
			if !found || advertised != ready {
				t.Fatalf("source=%s found=%v audio=%v ready=%v", kind, found, advertised, ready)
			}
		}
	}
}
