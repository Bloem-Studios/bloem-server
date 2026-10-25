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
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/markers"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

type videoBindingFixture map[string]NativeVideoBinding

func (b videoBindingFixture) Put(_ context.Context, id string, value NativeVideoBinding) error {
	if prior, ok := b[id]; ok && prior != value {
		return storagesource.ErrReferenceConflict
	}
	b[id] = value
	return nil
}
func (b videoBindingFixture) Get(_ context.Context, id string, file int) (NativeVideoBinding, error) {
	v, ok := b[id]
	if !ok || v.Identity.FileID != file {
		return NativeVideoBinding{}, storagesource.ErrReferenceConflict
	}
	return v, nil
}

func (b videoBindingFixture) RevokeSession(_ context.Context, id string) error {
	delete(b, id)
	return nil
}

type videoFileFixture struct {
	*bytes.Reader
	id     nativestorage.PlaybackIdentity
	info   mediasource.Info
	closed *int
}

func (f *videoFileFixture) Info() mediasource.Info                   { return f.info }
func (f *videoFileFixture) Identity() nativestorage.PlaybackIdentity { return f.id }
func (f *videoFileFixture) Context() context.Context                 { return context.Background() }
func (f *videoFileFixture) Cancel()                                  {}
func (f *videoFileFixture) Reauthorize(context.Context) error        { return nil }
func (f *videoFileFixture) Err() error                               { return nil }
func (f *videoFileFixture) Close() error                             { *f.closed++; return nil }

func nativeVideoFixture(t *testing.T) (*NativeVideoService, *models.MediaFile, *playback.Session, *nativestorage.PlaybackIdentity, *int) {
	t.Helper()
	f := &models.MediaFile{ID: 42, ContentID: "movie", MediaFolderID: 7, FilePath: "bloem-storage:movie", Container: "mp4", VideoTracks: []models.VideoTrack{{Codec: "h264"}}, Duration: 10}
	m := playback.NewSessionManager(0, 0)
	session, err := m.StartSession(1, "profile-1", 42, playback.PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	identity := &nativestorage.PlaybackIdentity{FileID: 42, MediaFolderID: 7, ContentID: "movie", FilePath: f.FilePath, Reference: storagesource.PersistedRef{Revision: "version-1"}}
	closed := new(int)
	s := &NativeVideoService{Files: testPlaybackFileResolver{file: f}, Bindings: videoBindingFixture{}, Sessions: m}
	s.Open = func(context.Context, *models.MediaFile) (NativeVideoFile, error) {
		return &videoFileFixture{Reader: bytes.NewReader([]byte("0123456789")), id: *identity, info: mediasource.Info{Name: "movie.mp4", Size: 10, Revision: identity.Reference.Revision}, closed: closed}, nil
	}
	return s, f, session, identity, closed
}

func TestNativeVideoDirectRanges(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			s, f, session, _, closed := nativeVideoFixture(t)
			if err := s.Bind(context.Background(), session, f); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(method, "/stream/"+session.ID, nil)
			r.Header.Set("Range", "bytes=3-6")
			w := httptest.NewRecorder()
			if err := s.Serve(w, r, session, f); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusPartialContent || w.Header().Get("Content-Range") != "bytes 3-6/10" {
				t.Fatalf("status %d headers %v", w.Code, w.Header())
			}
			want := "3456"
			if method == http.MethodHead {
				want = ""
			}
			if w.Body.String() != want {
				t.Fatalf("body %q", w.Body.String())
			}
			if *closed != 2 {
				t.Fatalf("closed %d", *closed)
			}
		})
	}
}
func TestNativeVideoSessionNeverReopensReplacement(t *testing.T) {
	s, f, session, identity, _ := nativeVideoFixture(t)
	if err := s.Bind(context.Background(), session, f); err != nil {
		t.Fatal(err)
	}
	identity.Reference.Revision = "version-2"
	w := httptest.NewRecorder()
	err := s.Serve(w, httptest.NewRequest(http.MethodGet, "/stream/"+session.ID, nil), session, f)
	if !errors.Is(err, storagesource.ErrReferenceConflict) || w.Body.Len() != 0 {
		t.Fatalf("err %v bytes %d", err, w.Body.Len())
	}
}

type videoBoundReadFixture struct {
	NativeVideoFile
	read      func()
	cancelled atomic.Bool
}

func (f *videoBoundReadFixture) Read(p []byte) (int, error) {
	n, err := f.NativeVideoFile.Read(p)
	if f.read != nil {
		f.read()
	}
	return n, err
}
func (f *videoBoundReadFixture) ReadAt(p []byte, offset int64) (int, error) {
	n, err := f.NativeVideoFile.ReadAt(p, offset)
	if f.read != nil {
		f.read()
	}
	return n, err
}
func (f *videoBoundReadFixture) Cancel() { f.cancelled.Store(true); f.NativeVideoFile.Cancel() }

func TestNativeVideoBoundReadsDiscardBytesAfterRevocation(t *testing.T) {
	for _, positional := range []bool{false, true} {
		t.Run(fmt.Sprint("positional=", positional), func(t *testing.T) {
			s, file, session, _, _ := nativeVideoFixture(t)
			if err := s.Bind(t.Context(), session, file); err != nil {
				t.Fatal(err)
			}
			revoked := false
			s.AuthorizeBound = func(_ context.Context, binding NativeVideoBinding) error {
				if binding.SessionID != session.ID || binding.Identity.FileID != file.ID {
					return storagesource.ErrReferenceConflict
				}
				if revoked {
					return errPlaybackSessionEnded
				}
				return nil
			}
			open := s.Open
			var provider *videoBoundReadFixture
			s.Open = func(ctx context.Context, file *models.MediaFile) (NativeVideoFile, error) {
				opened, err := open(ctx, file)
				provider = &videoBoundReadFixture{NativeVideoFile: opened, read: func() { revoked = true }}
				return provider, err
			}
			opened, _, err := s.OpenBound(t.Context(), session, file)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			buffer := bytes.Repeat([]byte{0x7f}, 16)
			var n int
			if positional {
				n, err = opened.ReadAt(buffer, 0)
			} else {
				n, err = opened.Read(buffer)
			}
			if n != 0 || !errors.Is(err, errPlaybackSessionEnded) || !bytes.Equal(buffer, make([]byte, len(buffer))) || !provider.cancelled.Load() {
				t.Fatalf("revoked read returned n=%d err=%v buffer=%x cancelled=%v", n, err, buffer, provider.cancelled.Load())
			}
			revoked = false
			if n, err = opened.Read(buffer); n != 0 || !errors.Is(err, errPlaybackSessionEnded) {
				t.Fatalf("terminal denial was not sticky: n=%d err=%v", n, err)
			}
		})
	}
}

func TestNativeVideoBoundValidationChecksSealedAuthority(t *testing.T) {
	s, file, session, _, closed := nativeVideoFixture(t)
	if err := s.Bind(t.Context(), session, file); err != nil {
		t.Fatal(err)
	}
	s.AuthorizeBound = func(context.Context, NativeVideoBinding) error { return errPlaybackSessionEnded }
	if err := s.Validate(t.Context(), session); !errors.Is(err, errPlaybackSessionEnded) {
		t.Fatalf("revoked artifact admitted: %v", err)
	}
	if *closed != 2 {
		t.Fatalf("refused bound source not closed: %d", *closed)
	}
}

func TestNativeVideoReplanKeepsOriginalDelegation(t *testing.T) {
	s, file, session, identity, _ := nativeVideoFixture(t)
	expires := time.Now().Add(time.Hour)
	s.Delegation = func(context.Context) (NativeVideoAuthority, error) {
		return NativeVideoAuthority{UserID: session.UserID, ProfileID: session.ProfileID, ExpiresAt: expires}, nil
	}
	if err := s.Bind(context.Background(), session, file); err != nil {
		t.Fatal(err)
	}
	original, err := s.Bindings.Get(context.Background(), session.ID, file.ID)
	if err != nil {
		t.Fatal(err)
	}
	expires = expires.Add(time.Minute)
	if err := s.Bind(context.Background(), session, file); err != nil {
		t.Fatalf("same revision replan: %v", err)
	}
	current, err := s.Bindings.Get(context.Background(), session.ID, file.ID)
	if err != nil || current != original {
		t.Fatal("replan replaced sealed delegation")
	}
	identity.Reference.Revision = "replacement"
	if err := s.Bind(context.Background(), session, file); !errors.Is(err, storagesource.ErrReferenceConflict) {
		t.Fatalf("replacement bind: %v", err)
	}
}
func TestNativeVideoRefusesEbook(t *testing.T) {
	s, f, session, _, _ := nativeVideoFixture(t)
	f.Container = "epub"
	f.VideoTracks = nil
	if err := s.Bind(context.Background(), session, f); err == nil {
		t.Fatal("ebook acquired video session binding")
	}
}

func TestNativeVideoStartAndStream(t *testing.T) {
	s, f, _, identity, _ := nativeVideoFixture(t)
	s.Bindings = NewNativeVideoBindingStore(nil)
	f.CodecVideo = "h264"
	f.CodecAudio = "aac"
	f.Resolution = "1080p"
	f.Bitrate = 1000
	f.ProbeSource = "storage"
	f.VideoTracks = []models.VideoTrack{{Codec: "h264", Profile: "high", Level: 41, Width: 1920, Height: 1080, FrameRate: "24/1", BitDepth: 8, VideoRange: "SDR", VideoRangeType: "SDR"}}
	f.AudioTracks = []models.AudioTrack{{Codec: "aac", Channels: 2, Layout: "stereo", Default: true}}
	f.AudioChannels = 2
	h := NewPlaybackHandler(s.Sessions, s.Files)
	h.NativeVideo = s
	h.ItemAccess = allowAllPlaybackItemAccess{}
	h.MissingMarker = &recordingMissingMarker{}
	h.ProbeEnsurer = &blockingProbeEnsurer{t: t}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/playback/start", strings.NewReader(marshalV3StartRequest(t, v3HandlerStartRequest()))).WithContext(newAuthorizedPlaybackContext())
	w := httptest.NewRecorder()
	h.HandleStartPlayback(w, request)
	if w.Code != http.StatusCreated {
		t.Fatalf("start %d %s", w.Code, w.Body.String())
	}
	var decision playback.DecisionResponseV3
	if err := json.Unmarshal(w.Body.Bytes(), &decision); err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != playback.OutcomePlayableV3 || decision.PlaybackPlan == nil || decision.PlaybackPlan.Delivery != playback.DeliveryOriginalHTTPV3 {
		t.Fatalf("decision %+v", decision)
	}
	stream := NewStreamHandler(s.Sessions, s.Files)
	stream.NativeVideo = s
	r := playbackTestRequest(http.MethodGet, "/api/v1/stream/"+decision.SessionID, nil, map[string]string{"session_id": decision.SessionID})
	r.Header.Set("Range", "bytes=2-4")
	w = httptest.NewRecorder()
	stream.HandleStream(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "234" {
		t.Fatalf("stream %d %s", w.Code, w.Body.String())
	}
	identity.Reference.Revision = "version-2"
	w = httptest.NewRecorder()
	stream.HandleStream(w, r)
	if w.Code != http.StatusConflict || strings.Contains(w.Body.String(), "012345") {
		t.Fatalf("replaced stream %d %s", w.Code, w.Body.String())
	}
	if f.MissingSince != nil {
		t.Fatal("native identity marked missing")
	}
}

func TestNativeVideoStopDuringTransportAdmissionReturnsNoBytes(t *testing.T) {
	s, f, session, _, _ := nativeVideoFixture(t)
	if err := s.Bind(context.Background(), session, f); err != nil {
		t.Fatal(err)
	}
	manager := &hookedSessionManager{SessionManager: s.Sessions.(*playback.SessionManager)}
	manager.beginTransportHook = func() { _ = manager.StopSession(session.ID) }
	h := NewStreamHandler(manager, s.Files)
	h.NativeVideo = s
	w := httptest.NewRecorder()
	r := playbackTestRequest(http.MethodGet, "/api/v1/stream/"+session.ID, nil, map[string]string{"session_id": session.ID})
	h.HandleStream(w, r)
	if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "0123456789") {
		t.Fatalf("stopped session served %d %s", w.Code, w.Body.String())
	}
}

type nativeVideoThumbnailRecorder struct{ calls int }

func (r *nativeVideoThumbnailRecorder) QueuePriorityFileAtPosition(context.Context, int, float64) {
	r.calls++
}

func TestNativeVideoStartEffectsAvoidFilesystemJobs(t *testing.T) {
	s, file, session, _, _ := nativeVideoFixture(t)
	file.EpisodeID = "episode"
	h := NewPlaybackHandler(s.Sessions, s.Files)
	h.NativeVideo = s
	thumbnails := new(nativeVideoThumbnailRecorder)
	analyzer := &fakePlaybackIntroAnalyzer{started: make(chan struct{}, 1)}
	h.ChapterThumbnailQueuer = thumbnails
	h.SettingsRepo = &mutablePlaybackSettingsV3{values: map[string]string{markers.SettingLazyPlayback: "true", markers.SettingMode: "local"}}
	h.IntroRepository = fakePlaybackIntroEligibility{eligible: true}
	h.IntroAnalyzer = analyzer
	h.MarkerLazyContext = context.Background()
	h.EpisodeLookup = testEpisodeLookup{episode: &models.Episode{ContentID: file.EpisodeID, SeriesID: "series"}}
	h.enqueuePlaybackStartSideEffectsV3(context.Background(), session, file, session.UserID, session.ProfileID, new(0))
	h.waitForPlaybackStartSideEffectsV3(context.Background(), session.ID)
	if thumbnails.calls != 0 || analyzer.callCount() != 0 {
		t.Fatalf("native source sent to filesystem jobs: thumbnails %d markers %d", thumbnails.calls, analyzer.callCount())
	}
	racer := new(recordingCopySafetyRacer)
	h.CopySafetyRacer = racer
	h.raceCopySafetyV3(file.ID, &playback.PlanV3{Delivery: playback.DeliveryRemuxHLSV3})
	if len(racer.files) != 0 {
		t.Fatalf("native source sent to filesystem copy scanner: %v", racer.files)
	}
}

func TestNativeVideoDetachedSourceUsesDurableDelegation(t *testing.T) {
	s, file, session, _, _ := nativeVideoFixture(t)
	if err := s.Bind(context.Background(), session, file); err != nil {
		t.Fatal(err)
	}
	original := s.Open
	s.Open = func(context.Context, *models.MediaFile) (NativeVideoFile, error) {
		return nil, errors.New("HTTP credential no longer exists")
	}
	delegated := false
	s.OpenDelegated = func(ctx context.Context, file *models.MediaFile, binding NativeVideoBinding) (NativeVideoFile, error) {
		if binding.SessionID != session.ID || binding.UserID != session.UserID || binding.ProfileID != session.ProfileID {
			t.Fatal("unbound delegation")
		}
		delegated = true
		return original(ctx, file)
	}
	source, format, err := s.InputSource(context.Background(), session, file)
	if err != nil {
		t.Fatal(err)
	}
	input, err := source.Open(context.Background(), playback.TranscodeOpts{InputPath: file.FilePath, SessionID: session.ID, InputFormat: format})
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if !delegated {
		t.Fatal("detached execution reused HTTP credentials")
	}
}
