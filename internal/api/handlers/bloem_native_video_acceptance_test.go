package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/mediaprobe"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tonemap"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Catalog, provider and deny-marker persistence I/O are synthetic. The coordinator,
// immutable reader, video service, planner, handlers and ffmpeg run together.
// The production deny-marker store runs unchanged; this hook replaces Redis I/O.
type nativeAcceptanceDenyIO struct {
	mu   sync.Mutex
	keys map[string]bool
}

func (*nativeAcceptanceDenyIO) DialHook(next redis.DialHook) redis.DialHook { return next }
func (*nativeAcceptanceDenyIO) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (d *nativeAcceptanceDenyIO) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		d.mu.Lock()
		defer d.mu.Unlock()
		key := fmt.Sprint(cmd.Args()[1])
		switch cmd.Name() {
		case "set":
			d.keys[key] = true
			cmd.(*redis.StatusCmd).SetVal("OK")
			return nil
		case "get":
			if d.keys[key] {
				cmd.(*redis.StringCmd).SetVal("denied")
				return nil
			}
			cmd.SetErr(redis.Nil)
			return redis.Nil
		default:
			err := fmt.Errorf("unexpected deny-marker command %s", cmd.Name())
			cmd.SetErr(err)
			return err
		}
	}
}

type nativeAcceptanceReadBarrier struct {
	entered chan struct{}
	once    sync.Once
}

type nativeAcceptanceProvider struct {
	mu       sync.RWMutex
	source   storagesource.SourceConfig
	ref      storagesource.PersistedRef
	data     map[string][]byte
	snapshot plugins.NativeStorageSnapshot
	hold     atomic.Pointer[nativeAcceptanceReadBarrier]
	reads    atomic.Int64
	revoked  atomic.Bool
	opens    atomic.Int64
	active   atomic.Int64
}

func (p *nativeAcceptanceProvider) FileReference(_ context.Context, id, folder int) (storagesource.SourceConfig, storagesource.PersistedRef, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if id != 42 || folder != 7 {
		return p.source, p.ref, storagesource.ErrReferenceConflict
	}
	return p.source, p.ref, nil
}
func (*nativeAcceptanceProvider) CoverReference(context.Context, string, string) (storagesource.CoverReference, error) {
	return storagesource.CoverReference{}, storagesource.ErrReferenceConflict
}
func (p *nativeAcceptanceProvider) Snapshot(context.Context, uuid.UUID, uuid.UUID) (*plugins.NativeStorageSnapshot, error) {
	return &p.snapshot, nil
}
func (p *nativeAcceptanceProvider) Borrow(context.Context, storageplugin.Snapshot) (nativestorage.Lease, error) {
	return nativestorage.Lease{Context: context.Background(), Source: p, Description: &storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: p.source.ProviderSourceID, RootEntryId: p.source.RootEntryID, RevisionPinnedReads: true}}}}, nil
}
func (*nativeAcceptanceProvider) Disable(int) {}
func (p *nativeAcceptanceProvider) Stat(ctx context.Context, ref mediasource.Ref) (mediasource.Info, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	data, ok := p.data[ref.Revision]
	if !ok {
		return mediasource.Info{}, storagesource.ErrReferenceConflict
	}
	return mediasource.Info{Name: p.ref.LogicalPath, LogicalPath: p.ref.LogicalPath, Revision: ref.Revision, Size: int64(len(data))}, ctx.Err()
}
func (p *nativeAcceptanceProvider) ReadRange(ctx context.Context, ref mediasource.Ref, offset, length int64, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if barrier := p.hold.Load(); barrier != nil {
		barrier.once.Do(func() { close(barrier.entered) })
		<-ctx.Done()
		return ctx.Err()
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	data, ok := p.data[ref.Revision]
	if !ok || offset < 0 || length < 0 || offset > int64(len(data))-length {
		return storagesource.ErrReferenceConflict
	}
	p.reads.Add(1)
	_, err := w.Write(data[offset : offset+length])
	return err
}
func (p *nativeAcceptanceProvider) replace() {
	p.mu.Lock()
	defer p.mu.Unlock()
	original := p.data[p.ref.Revision]
	changed := bytes.Clone(original)
	changed[len(changed)-1] ^= 1
	p.data["version-2"] = changed
	p.ref.Revision = "version-2"
}

type nativeAcceptance struct {
	p        *nativeAcceptanceProvider
	service  *NativeVideoService
	h        *PlaybackHandler
	stream   *StreamHandler
	sessions *playback.SessionManager
	file     *models.MediaFile
	data     []byte
}

func newNativeAcceptance(t *testing.T, container string, hdr ...bool) *nativeAcceptance {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	path := filepath.Join(t.TempDir(), "synthetic."+container)
	args := []string{"-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=1280x720:r=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "8", "-c:v", "libx264", "-profile:v", "high", "-level:v", "4.1", "-threads", "1", "-g", "24", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2"}
	if len(hdr) > 0 && hdr[0] {
		// FFmpeg 8 tags the encoded stream from the filtered frames' color
		// properties, not from -color_trc and friends, so setparams marks
		// the frames as PQ BT.2020. Older builds accept either.
		args = []string{"-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=1280x720:r=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "8", "-vf", "setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc", "-c:v", "libx265", "-preset", "ultrafast", "-threads", "1", "-g", "24", "-pix_fmt", "yuv420p10le", "-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc", "-x265-params", "pools=1:frame-threads=1:wpp=0:log-level=error:hdr10=1:repeat-headers=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50):max-cll=1000,400", "-c:a", "aac", "-ac", "2"}
	}
	if container == "mp4" {
		args = append(args, "-movflags", "+faststart")
	}
	args = append(args, path)
	if output, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		t.Fatalf("synthetic media: %v %s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	installation := int64(3)
	p := &nativeAcceptanceProvider{source: storagesource.SourceConfig{Key: uuid.New(), OwnerID: uuid.New(), InstallationID: &installation, PluginID: "synthetic", ProviderSourceID: "immutable-source", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true}, ref: storagesource.PersistedRef{LocationID: uuid.New(), EntryID: "movie", Revision: "version-1", LogicalPath: "movie." + container}, data: map[string][]byte{"version-1": data}}
	p.snapshot = plugins.NativeStorageSnapshot{Source: p.source, Installation: &plugins.Installation{ID: 3, InstallPath: "/synthetic/plugin", Enabled: true}, Generation: 1, ArtifactChecksum: "verified-synthetic"}
	location, err := storagesource.CatalogLocation(p.ref.LocationID, p.ref.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	file := &models.MediaFile{ID: 42, MediaFolderID: 7, ContentID: "movie", FilePath: location, Container: container, CodecVideo: "h264", CodecAudio: "aac", Resolution: "720p", Bitrate: 1000, Duration: 8, AudioChannels: 2, ProbeSource: "storage", VideoTracks: []models.VideoTrack{{Codec: "h264", Profile: "high", Level: 41, Width: 1280, Height: 720, FrameRate: "24/1", BitDepth: 8, VideoRange: "SDR", VideoRangeType: "SDR"}}, AudioTracks: []models.AudioTrack{{Codec: "aac", Channels: 2, Layout: "stereo", Default: true}}}
	file.FileSize = int64(len(data))
	digest := sha256.Sum256(data)
	file.FileHash = fmt.Sprintf("%x", digest)
	if len(hdr) > 0 && hdr[0] {
		track, err := mediaprobe.ProbePrimaryVideoTrack(t.Context(), mediaprobe.FFprobePathFromFFmpeg(bin), path)
		if err != nil {
			t.Fatal(err)
		}
		file.VideoTracks = []models.VideoTrack{track}
		file.CodecVideo = track.Codec
		probedAt := time.Now()
		file.ProbeUpdatedAt = &probedAt
	}
	c := &nativestorage.Coordinator{References: p, Registry: p, Runtime: p, AcquireOpen: func() (func(), error) { p.opens.Add(1); p.active.Add(1); return func() { p.active.Add(-1) }, nil }}
	authority := nativestorage.PlaybackAuthority{AuthorizeFile: func(ctx context.Context, _ *models.MediaFile) (*models.MediaFile, error) {
		if p.revoked.Load() {
			return nil, catalog.ErrItemNotFound
		}
		return file, ctx.Err()
	}, AuthorizeSource: func(ctx context.Context, _ *models.MediaFile, _ storagesource.SourceConfig) error {
		if p.revoked.Load() {
			return catalog.ErrItemNotFound
		}
		return ctx.Err()
	}}
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	client.AddHook(&nativeAcceptanceDenyIO{keys: make(map[string]bool)})
	t.Cleanup(func() { _ = client.Close() })
	deny := playback.NewStreamDeny(client)
	sessions := playback.NewSessionManager(0, 0)
	service := &NativeVideoService{Deny: deny, Files: testPlaybackFileResolver{file: file}, Sessions: sessions, Bindings: NewNativeVideoBindingStore(nil), Open: func(ctx context.Context, f *models.MediaFile) (NativeVideoFile, error) {
		opened, err := c.OpenAuthorizedPlayback(ctx, f, authority)
		if err != nil {
			return nil, err
		}
		return opened, nil
	}}
	h := NewPlaybackHandler(sessions, service.Files)
	h.NativeVideo = service
	h.StreamDeny = deny
	h.ItemAccess = allowAllPlaybackItemAccess{}
	h.ProbeEnsurer = &blockingProbeEnsurer{t: t}
	h.PlaybackConfig = playbackTestConfig(bin, t.TempDir())
	h.JWTSecret = "synthetic-acceptance-key"
	h.SettingsRepo = &mutablePlaybackSettingsV3{values: map[string]string{"transcode_enabled": "true"}}
	h.InstallNativeVideoRecovery()
	stream := NewStreamHandler(sessions, service.Files)
	stream.NativeVideo = service
	stream.StreamDeny = deny
	stream.PlaybackConfig = h.PlaybackConfig
	t.Cleanup(func() {
		if n := p.active.Load(); n != 0 {
			t.Errorf("source admission still held: %d", n)
		}
	})
	return &nativeAcceptance{p: p, service: service, h: h, stream: stream, sessions: sessions, file: file, data: data}
}
func (a *nativeAcceptance) start(t *testing.T, delivery string, encode bool) playback.DecisionResponseV3 {
	t.Helper()
	start := v3HandlerStartRequest()
	start.PlaybackAttemptID = "native-acceptance-" + delivery
	if delivery != "hls" {
		start.ClientFeatures = append(start.ClientFeatures, playback.FeatureHeaderAuthenticatedMediaV3)
	}
	if delivery != "direct" {
		start.ClientPlaybackContext.Deliveries = map[string]playback.DeliveryCapabilityV3{}
		key := playback.DeliveryClassProgressiveV3
		containers := []string{"mp4"}
		if delivery == "hls" {
			key = playback.DeliveryClassHLSV3
			containers = []string{"hls"}
		}
		start.ClientPlaybackContext.Deliveries[key] = playback.DeliveryCapabilityV3{Enabled: true, SupportedOnDevice: true, Containers: containers, VideoCodecs: []string{"h264"}, AudioDecodeCodecs: []string{"aac"}}
	}
	if delivery != "direct" && !encode {
		position := 2.5
		start.StartPosition = &position
	}
	if encode {
		start.QualityPreference = "480p"
	}
	w := httptest.NewRecorder()
	a.h.HandleStartPlayback(w, httptest.NewRequest(http.MethodPost, "/api/v1/playback/start", strings.NewReader(marshalV3StartRequest(t, start))).WithContext(newAuthorizedPlaybackContext()))
	var result playback.DecisionResponseV3
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Outcome != playback.OutcomePlayableV3 || result.PlaybackPlan == nil {
		t.Fatalf("start %d %s", w.Code, w.Body.String())
	}
	t.Cleanup(func() {
		_ = a.h.stopPlaybackSessionByID(newAuthorizedPlaybackContext(), result.SessionID, false)
		a.h.tm.CloseTranscodeSession(result.SessionID, "")
	})
	return result
}
func (a *nativeAcceptance) getStream(id, method string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := playbackTestRequest(method, "/api/v1/stream/"+id, nil, map[string]string{"session_id": id})
	r.Header.Set("Range", "bytes=2-31")
	a.stream.HandleStream(w, r)
	return w
}
func waitNativeAcceptance(t *testing.T, ts *playback.TranscodeSession) {
	t.Helper()
	if ts == nil {
		t.Fatal("missing actual transcode")
	}
	select {
	case <-ts.Done():
	case <-time.After(20 * time.Second):
		t.Fatal("ffmpeg/input did not join")
	}
	if err := ts.WaitError(); err != nil {
		t.Fatalf("ffmpeg: %v", err)
	}
}
func (a *nativeAcceptance) manifest(id string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.h.HandleGetTranscodeManifest(w, playbackTestRequest(http.MethodGet, "/transcode/"+id+"/master.m3u8", nil, map[string]string{"session_id": id}))
	return w
}
func (a *nativeAcceptance) segment(id, name string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.h.HandleGetTranscodeSegment(w, playbackTestRequest(http.MethodGet, "/transcode/"+id+"/"+name, nil, map[string]string{"session_id": id, "name": name}))
	return w
}
func TestNativeVideoAcceptanceDirectAndAuthority(t *testing.T) {
	for _, terminal := range []string{"revision", "viewer", "stop"} {
		t.Run(terminal, func(t *testing.T) {
			a := newNativeAcceptance(t, "mp4")
			result := a.start(t, "direct", false)
			if result.PlaybackPlan.Delivery != playback.DeliveryOriginalHTTPV3 {
				t.Fatal(result.PlaybackPlan.Delivery)
			}
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				w := a.getStream(result.SessionID, method)
				want := a.data[2:32]
				if method == http.MethodHead {
					want = nil
				}
				if w.Code != 206 || !bytes.Equal(w.Body.Bytes(), want) || w.Header().Get("Content-Range") != fmt.Sprintf("bytes 2-31/%d", len(a.data)) {
					t.Fatalf("range %d %s", w.Code, w.Body.String())
				}
			}
			before := a.p.reads.Load()
			switch terminal {
			case "revision":
				a.p.replace()
			case "viewer":
				a.p.revoked.Store(true)
			case "stop":
				if err := a.h.stopPlaybackSessionByID(newAuthorizedPlaybackContext(), result.SessionID, true); err != nil {
					t.Fatal(err)
				}
			}
			w := a.getStream(result.SessionID, http.MethodGet)
			if w.Code < 400 || a.p.reads.Load() != before {
				t.Fatalf("invalid authority served bytes: %d reads %d->%d", w.Code, before, a.p.reads.Load())
			}
		})
	}
}
func TestNativeVideoAcceptanceProgressive(t *testing.T) {
	a := newNativeAcceptance(t, "mkv")
	result := a.start(t, "progressive", false)
	if result.PlaybackPlan.Delivery != playback.DeliveryRemuxProgressiveV3 {
		t.Fatal(result.PlaybackPlan.Delivery)
	}
	w := httptest.NewRecorder()
	a.stream.HandleStream(w, playbackTestRequest(http.MethodGet, "/api/v1"+result.PlaybackPlan.Stream.URL, nil, map[string]string{"session_id": result.SessionID}))
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("ftyp")) {
		t.Fatalf("remux %d %s", w.Code, w.Body.String())
	}
	assertNativeAcceptanceDecode(t, a.h.playbackConfig().FFmpegPath, "mov", w.Body.Bytes())
	if a.p.reads.Load() == 0 || a.p.active.Load() != 0 {
		t.Fatal("remux bypassed provider or leaked admission")
	}
	for _, terminal := range []string{"viewer", "revision", "stop"} {
		before := a.p.reads.Load()
		switch terminal {
		case "viewer":
			a.p.revoked.Store(true)
		case "revision":
			a.p.replace()
		case "stop":
			if err := a.h.stopPlaybackSessionByID(newAuthorizedPlaybackContext(), result.SessionID, true); err != nil {
				t.Fatal(err)
			}
		}
		refused := a.getStream(result.SessionID, http.MethodGet)
		if refused.Code < 400 || a.p.reads.Load() != before {
			t.Fatalf("%s served progressive bytes: %d", terminal, refused.Code)
		}
		a.p.revoked.Store(false)
		a.p.mu.Lock()
		a.p.ref.Revision = "version-1"
		a.p.mu.Unlock()
	}
}
func TestNativeVideoAcceptanceHLS(t *testing.T) {
	for _, encode := range []bool{false, true} {
		t.Run(fmt.Sprint("encode=", encode), func(t *testing.T) {
			a := newNativeAcceptance(t, "mkv")
			result := a.start(t, "hls", encode)
			want := playback.DeliveryRemuxHLSV3
			if encode {
				want = playback.DeliveryTranscodeHLSV3
			}
			if result.PlaybackPlan.Delivery != want {
				t.Fatal(result.PlaybackPlan.Delivery)
			}
			ts := a.h.tm.GetTranscodeSession(result.SessionID)
			waitNativeAcceptance(t, ts)
			w := a.manifest(result.SessionID)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "#EXTM3U") {
				t.Fatalf("manifest %d %s", w.Code, w.Body.String())
			}
			extension := ".ts"
			if !encode {
				extension = ".m4s"
			}
			segmentName := fmt.Sprintf("seg_%05d%s", ts.Opts().StartSegmentNumber, extension)
			segment := a.segment(result.SessionID, segmentName)
			if segment.Code != 200 || segment.Body.Len() < 188 {
				t.Fatalf("segment %d %s", segment.Code, segment.Body.String())
			}

			a.p.revoked.Store(true)
			if refused := a.manifest(result.SessionID); refused.Code < 400 {
				t.Fatal("revoked viewer served cached manifest")
			}
			if refused := a.segment(result.SessionID, segmentName); refused.Code < 400 {
				t.Fatal("revoked viewer served cached segment")
			}
			a.p.revoked.Store(false)
			if encode {
				assertNativeAcceptanceDecode(t, a.h.playbackConfig().FFmpegPath, "mpegts", segment.Body.Bytes())
			} else {
				initialization := a.segment(result.SessionID, "init.mp4")
				if initialization.Code != 200 {
					t.Fatalf("initialization: %d", initialization.Code)
				}
				assertNativeAcceptanceDecode(t, a.h.playbackConfig().FFmpegPath, "mov", append(bytes.Clone(initialization.Body.Bytes()), segment.Body.Bytes()...))
			}
			opts := ts.Opts()
			if !encode && (opts.SeekSeconds != 2.5 || opts.StreamOriginSeconds < 1.99 || opts.StreamOriginSeconds > 2.05 || !opts.CopySeekAnchorResolved) {
				t.Fatalf("copy seek did not use pinned-source anchor: seek=%g origin=%g resolved=%t", opts.SeekSeconds, opts.StreamOriginSeconds, opts.CopySeekAnchorResolved)
			}

			streamURL, err := url.Parse(result.PlaybackPlan.Stream.URL)
			if err != nil {
				t.Fatal(err)
			}
			card, _ := verifiedStreamCardFromToken(streamURL.Query().Get(streamTokenParam), result.SessionID, a.h.JWTSecret)
			if card == nil {
				t.Fatal("start did not commit a reconstructable recipe")
			}
			serialized, err := json.Marshal(card)
			if err != nil {
				t.Fatal(err)
			}
			if opts.InputPath != a.file.FilePath || strings.Contains(string(serialized), "127.0.0.1") || strings.Contains(string(serialized), "http://") {
				t.Fatal("private input URL persisted")
			}
			before := a.p.opens.Load()
			if err := ts.Restart(newAuthorizedPlaybackContext(), 2, 1); err != nil {
				t.Fatal(err)
			}
			waitNativeAcceptance(t, ts)
			if a.p.opens.Load() <= before {
				t.Fatal("restart reused an open input")
			}
			a.h.tm.CloseTranscodeSession(result.SessionID, "")
			recoveryRequest := playbackTestRequest(http.MethodGet, "/api/v1"+result.PlaybackPlan.Stream.URL, nil, map[string]string{"session_id": result.SessionID})
			recoveryResponse := httptest.NewRecorder()
			a.h.HandleGetTranscodeManifest(recoveryResponse, recoveryRequest)
			if recoveryResponse.Code != 200 || !strings.Contains(recoveryResponse.Body.String(), "#EXTM3U") {
				t.Fatalf("handler recovery %d %s", recoveryResponse.Code, recoveryResponse.Body.String())
			}
			recovered := a.h.tm.GetTranscodeSession(result.SessionID)
			waitNativeAcceptance(t, recovered)
			if a.p.reads.Load() == 0 {
				t.Fatal("HLS bypassed provider")
			}
			a.p.replace()
			w = a.manifest(result.SessionID)
			if w.Code < 400 || strings.Contains(w.Body.String(), "#EXTM3U") {
				t.Fatalf("replacement served manifest %d", w.Code)
			}
			segment = a.segment(result.SessionID, segmentName)
			if segment.Code < 400 {
				t.Fatal("replacement served existing segment")
			}
			a.h.tm.CloseTranscodeSession(result.SessionID, "")
			recoveryResponse = httptest.NewRecorder()
			a.h.HandleGetTranscodeManifest(recoveryResponse, recoveryRequest)
			if recoveryResponse.Code < 400 || strings.Contains(recoveryResponse.Body.String(), "#EXTM3U") {
				t.Fatal("recovered replacement")
			}
			a.p.mu.Lock()
			a.p.ref.Revision = "version-1"
			a.p.mu.Unlock()
			if err := a.h.stopPlaybackSessionByID(newAuthorizedPlaybackContext(), result.SessionID, true); err != nil {
				t.Fatal(err)
			}
			if refused := a.manifest(result.SessionID); refused.Code < 400 {
				t.Fatal("stopped playback served manifest")
			}
			if refused := a.segment(result.SessionID, segmentName); refused.Code < 400 {
				t.Fatal("stopped playback served segment")
			}
			recoveryResponse = httptest.NewRecorder()
			a.h.HandleGetTranscodeManifest(recoveryResponse, recoveryRequest)
			if recoveryResponse.Code < 400 || a.h.tm.GetTranscodeSession(result.SessionID) != nil {
				t.Fatal("stopped signed recipe recovered native output")
			}

		})
	}
}

func assertNativeAcceptanceDecode(t *testing.T, binary, format string, data []byte) {
	t.Helper()
	cmd := exec.Command(binary, "-v", "error", "-threads", "1", "-f", format, "-i", "pipe:0", "-f", "null", "-")
	cmd.Stdin = bytes.NewReader(data)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("output was not decodable media: %v %s", err, output)
	}
}

func TestNativeVideoAcceptanceStopJoinsInflightRange(t *testing.T) {
	a := newNativeAcceptance(t, "mp4")
	result := a.start(t, "direct", false)
	barrier := &nativeAcceptanceReadBarrier{entered: make(chan struct{})}
	a.p.hold.Store(barrier)
	type outcome struct {
		response *httptest.ResponseRecorder
		aborted  any
	}
	finished := make(chan outcome, 1)
	go func() {
		w := httptest.NewRecorder()
		defer func() { finished <- outcome{response: w, aborted: recover()} }()
		r := playbackTestRequest(http.MethodGet, "/stream/"+result.SessionID, nil, map[string]string{"session_id": result.SessionID})
		r.Header.Set("Range", "bytes=2-31")
		a.stream.HandleStream(w, r)
	}()
	select {
	case <-barrier.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("actual stream never reached provider range")
	}
	if err := a.h.stopPlaybackSessionByID(newAuthorizedPlaybackContext(), result.SessionID, true); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-finished:
		aborted, isError := got.aborted.(error)
		if got.aborted != nil && (!isError || !errors.Is(aborted, http.ErrAbortHandler)) {
			t.Fatalf("unexpected abort: %v", got.aborted)
		}
		if got.response.Body.Len() != 0 || a.p.active.Load() != 0 {
			t.Fatalf("stop exposed bytes or retained input: bytes=%d active=%d", got.response.Body.Len(), a.p.active.Load())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stopped provider read did not join")
	}
}

func TestNativeVideoAcceptanceHDRSoftwareToneMap(t *testing.T) {
	a := newNativeAcceptance(t, "mkv", true)
	if a.file.VideoTracks[0].BitDepth != 10 || a.file.VideoTracks[0].ColorTransfer != "smpte2084" {
		t.Fatalf("fixture is not HEVC HDR: %+v", a.file.VideoTracks[0])
	}
	a.h.SettingsRepo = &mutablePlaybackSettingsV3{values: map[string]string{"transcode_enabled": "true", config.PlaybackTranscodeHardwareToneMapSettingKey: "false", config.PlaybackTranscodeSoftwareToneMapSettingKey: "true"}}
	previous := a.h.PlaybackConfig
	a.h.PlaybackConfig = func() config.PlaybackConfig { cfg := previous(); cfg.HWAccel = playback.HWAccelNone; return cfg }
	result := a.start(t, "hls", true)
	if result.PlaybackPlan.Delivery != playback.DeliveryTranscodeHLSV3 {
		t.Fatal("HDR source did not produce a transcode plan")
	}
	ts := a.h.tm.GetTranscodeSession(result.SessionID)
	waitNativeAcceptance(t, ts)
	opts := ts.Opts()
	if opts.ToneMapMode != tonemap.ModeSoftware || opts.ToneMapSourceKind != tonemap.SourcePQ || opts.ToneMapSourceRevision != tonemap.RevisionForFile(a.file) {
		t.Fatalf("HDR revision/mode not frozen: mode=%q source=%q", opts.ToneMapMode, opts.ToneMapSourceKind)
	}
	name := fmt.Sprintf("seg_%05d.ts", opts.StartSegmentNumber)
	segment := a.segment(result.SessionID, name)
	if segment.Code != 200 {
		t.Fatalf("HDR segment %d %s", segment.Code, segment.Body.String())
	}
	assertNativeAcceptanceDecode(t, a.h.playbackConfig().FFmpegPath, "mpegts", segment.Body.Bytes())
	probePath := filepath.Join(t.TempDir(), "output.ts")
	if err := os.WriteFile(probePath, segment.Body.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := mediaprobe.ProbePrimaryVideoTrack(t.Context(), mediaprobe.FFprobePathFromFFmpeg(a.h.playbackConfig().FFmpegPath), probePath)
	if err != nil {
		t.Fatal(err)
	}
	if output.Codec != "h264" || output.BitDepth != 8 || output.ColorTransfer != "bt709" || output.ColorPrimaries != "bt709" || output.ColorSpace != "bt709" {
		t.Fatalf("tone-map output is not SDR BT709: %+v", output)
	}
	before := a.p.opens.Load()
	if err := ts.Restart(newAuthorizedPlaybackContext(), 2, 1); err != nil {
		t.Fatal(err)
	}
	waitNativeAcceptance(t, ts)
	if a.p.opens.Load() <= before {
		t.Fatal("HDR restart did not freshly validate/open source")
	}
	a.p.replace()
	if err := ts.Restart(newAuthorizedPlaybackContext(), 2, 1); err == nil {
		t.Fatal("HDR restart accepted a same-size replacement")
	}
	if refused := a.manifest(result.SessionID); refused.Code < 400 {
		t.Fatal("HDR replacement served cached output")
	}
}
