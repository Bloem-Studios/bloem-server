package planstore

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nodeconfig"
	"github.com/Silo-Server/silo-server/internal/noderecipe"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/proxy"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/subtitles"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type auxiliaryFixture struct {
	f           *initialActivationFixture
	api, egress *ExecutorRuntime
	card        playback.RecipeCard
	watcher     *nodeconfig.Watcher
}

func newAuxiliaryFixture(t *testing.T) *auxiliaryFixture {
	t.Helper()
	f := newInitialActivationFixture(t)
	nodeURL := "http://auxiliary-" + uuid.NewString() + ".invalid"
	if err := f.pool.QueryRow(t.Context(), `INSERT INTO stream_nodes(name,type,url) VALUES($1,'proxy',$2) RETURNING id`, "auxiliary-fixture", nodeURL).Scan(&f.route.EgressNodeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM stream_nodes WHERE id=$1`, f.route.EgressNodeID)
	})
	f.route.ExecutionNodeID = 0
	f.acknowledge(t)
	f.stage(t)
	if _, err := f.store.PublishInitialActivation(t.Context(), f.binding, f.record); err != nil {
		t.Fatal(err)
	}
	raw := os.Getenv("SILO_TEST_REDIS_URL")
	if raw == "" {
		t.Skip("owned Redis required")
	}
	opts, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	recipes := noderecipe.NewStore(client, time.Minute)
	card := playback.NewDirectRecipeCard(f.record.SessionID, f.userID, f.record.ProfileID, f.record.EffectiveMediaFileID)
	card.Executor = &f.route.Executor
	card.TranscodeTransportID = f.route.TransportID
	card.InputPath = "/owned/immutable-media"
	card.RoutingWorkload = string(noderouting.WorkloadDirectPlay)
	card.RoutingExecution = string(noderouting.ExecutionNone)
	card.RoutingEgress = string(noderouting.EgressProxy)
	card.RoutingEgressNodeID = f.route.EgressNodeID
	locator, err := recipes.PutImmutable(t.Context(), card)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recipes.DeleteImmutable(context.Background(), locator) })
	if err := f.store.PublishAttemptRecipeLocator(t.Context(), f.authority, nil, locator); err != nil {
		t.Fatal(err)
	}
	clock, err := playback.NewRuntimeGrantClockV3()
	if err != nil {
		t.Fatal(err)
	}
	policy := playback.RuntimeGrantPolicyV3{MaxDuration: time.Second, SafetyMargin: 100 * time.Millisecond, RenewBefore: 200 * time.Millisecond, PollInterval: 5 * time.Millisecond}
	api, err := NewExecutorRuntime(f.store, recipes, 0, clock, policy)
	if err != nil {
		t.Fatal(err)
	}
	egress, err := NewExecutorRuntime(f.store, recipes, f.route.EgressNodeID, clock, policy)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secret.New([]byte(strings.Repeat("x", 32)))
	if err != nil {
		t.Fatal(err)
	}
	watcher := nodeconfig.NewWatcher(f.pool, cipher, nil, nodeconfig.BootstrapOverrides{NodeURL: nodeURL, Mode: "proxy", DatabaseURL: os.Getenv("SILO_TEST_DATABASE_URL"), RedisURL: raw})
	if err := watcher.ForceReload(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfg := watcher.Config()
	cfg.Auth.JWTSecret = "auxiliary-fixture-secret"
	watcher.SetConfigForTest(cfg)
	if id, known := watcher.NodeRowID(); !known || id != f.route.EgressNodeID {
		t.Fatal("configured proxy node unresolved")
	}
	return &auxiliaryFixture{f, api, egress, card, watcher}
}

func TestAuxiliaryTransferExactAuthority(t *testing.T) {
	x := newAuxiliaryFixture(t)
	f := x.f
	ctx := t.Context()
	if _, _, err := x.api.OpenAuxiliaryTransfer(ctx, f.route.TransportID, f.route.Executor); err == nil {
		t.Fatal("API impersonated proxy")
	}
	if _, err := x.api.Acquire(ctx, f.route.TransportID, f.route.Executor, playback.AttemptGrantServeV3); err == nil {
		t.Fatal("API served proxy recipe")
	}
	permit, release, err := x.egress.OpenAuxiliaryTransfer(ctx, f.route.TransportID, f.route.Executor)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, id := range []string{"", "invalid", uuid.NewString()} {
		if _, err := x.api.AcquireAuxiliaryTransfer(ctx, f.route.TransportID, f.route.Executor, id); err == nil {
			t.Fatal("foreign/malformed permit")
		}
	}
	if _, err := x.egress.AcquireAuxiliaryTransfer(ctx, f.route.TransportID, f.route.Executor, permit); err == nil {
		t.Fatal("proxy became API producer")
	}
	other := newAuxiliaryFixture(t)
	if _, err := other.api.AcquireAuxiliaryTransfer(ctx, other.f.route.TransportID, other.f.route.Executor, permit); err == nil {
		t.Fatal("permit crossed real session")
	}
	output, closeOutput, err := x.egress.OpenOutputTransfer(ctx, f.route.TransportID, f.route.Executor)
	if err != nil {
		t.Fatal(err)
	}
	defer closeOutput()
	if _, err := x.api.AcquireAuxiliaryTransfer(ctx, f.route.TransportID, f.route.Executor, output); err == nil {
		t.Fatal("output permit became auxiliary")
	}
	if _, err := x.api.AcquireOutputTransfer(ctx, f.route.TransportID, f.route.Executor, permit); err == nil {
		t.Fatal("auxiliary permit became output")
	}
	request := f.request
	request.NodeID = 0
	request.Purpose = playback.AttemptGrantAuxiliaryV3
	request.AuxiliaryTransferID = permit
	request.EgressNodeID = f.route.EgressNodeID
	for _, mutate := range []func(*playback.AttemptGrantRequestV3){func(r *playback.AttemptGrantRequestV3) { r.SessionID = uuid.NewString() }, func(r *playback.AttemptGrantRequestV3) { r.PlanID = uuid.NewString() }, func(r *playback.AttemptGrantRequestV3) { r.Executor.ExecutorID = uuid.NewString() }, func(r *playback.AttemptGrantRequestV3) { r.NodeID = 9 }, func(r *playback.AttemptGrantRequestV3) { r.EgressNodeID += 100 }} {
		bad := request
		mutate(&bad)
		if _, err := f.store.IssueAttemptGrant(ctx, f.authority, bad); err == nil {
			t.Fatal("foreign authority issued")
		}
	}
	granted, err := f.store.IssueAttemptGrant(ctx, f.authority, request)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := x.api.AcquireAuxiliaryTransfer(ctx, f.route.TransportID, f.route.Executor, permit); err == nil {
		t.Fatal("released permit reacquired")
	}
	stop, err := f.store.BeginBoundStop(ctx, f.binding, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if stop.DrainNotBefore.Before(granted.NotAfter) {
		t.Fatal("lost producer reply excluded from stop")
	}
	if _, _, err := x.egress.OpenAuxiliaryTransfer(ctx, f.route.TransportID, f.route.Executor); err == nil {
		t.Fatal("stop opened permit")
	}
}

type auxiliaryFiles map[int]*models.MediaFile

func (f auxiliaryFiles) GetByID(_ context.Context, id int) (*models.MediaFile, error) {
	return f[id], nil
}

type auxiliarySubtitles struct {
	subtitles.Repository
	entry subtitles.DownloadedSubtitle
}

func (s auxiliarySubtitles) GetDownloadedSubtitle(_ context.Context, id int) (*subtitles.DownloadedSubtitle, error) {
	if id == s.entry.ID {
		return &s.entry, nil
	}
	return nil, nil
}
func (s auxiliarySubtitles) ListDownloadedSubtitles(_ context.Context, _ int) ([]subtitles.DownloadedSubtitle, error) {
	return []subtitles.DownloadedSubtitle{s.entry}, nil
}

type auxiliaryS3 struct {
	subtitles.S3Client
	get func(context.Context) ([]byte, error)
}

func (s auxiliaryS3) GetObject(ctx context.Context, _, _ string) ([]byte, error) { return s.get(ctx) }

func TestAuxiliaryTransferActualProxyAPI(t *testing.T) {
	x := newAuxiliaryFixture(t)
	f := x.f
	ctx := t.Context()
	dir := t.TempDir()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("ffmpeg required", err)
	}
	ass := "[Script Info]\nScriptType: v4.00+\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,owned styled cue\n"
	sidecar := filepath.Join(dir, "track.ass")
	if err := os.WriteFile(sidecar, []byte(ass), 0600); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(dir, "source.mkv")
	font, err := filepath.Abs("../../../web/public/vendor/pdfjs/standard_fonts/LiberationSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=size=64x36:rate=1", "-i", sidecar, "-attach", font, "-metadata:s:t:0", "mimetype=font/ttf", "-metadata:s:t:0", "filename=Fixture.ttf", "-t", "1", "-map", "0:v", "-map", "1:s", "-c:v", "libx264", "-c:s", "ass", "-y", media).CombinedOutput(); err != nil {
		t.Fatalf("media: %v %s", err, out)
	}
	sup := filepath.Join(dir, "synthetic.sup")
	if err := os.WriteFile(sup, auxiliaryPGSFixture(), 0600); err != nil {
		t.Fatal(err)
	}
	muxed := filepath.Join(dir, "with-pgs.mkv")
	if out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-i", media, "-i", sup, "-map", "0", "-map", "1", "-c", "copy", "-y", muxed).CombinedOutput(); err != nil {
		t.Fatalf("PGS mux: %v %s", err, out)
	}
	media = muxed
	file := &models.MediaFile{ID: x.card.MediaFileID, FilePath: media, ExternalSubtitles: []models.ExternalSubtitle{{Path: sidecar, Format: "ass"}}, SubtitleTracks: []models.SubtitleTrack{{Index: 1, Codec: "ass"}, {Index: 3, Codec: "hdmv_pgs_subtitle"}}}
	h := handlers.NewStreamHandler(playback.NewSessionManager(0, 0), auxiliaryFiles{file.ID: file})
	h.JWTSecret = "auxiliary-fixture-secret"
	h.PlaybackConfig = func() config.PlaybackConfig { return config.PlaybackConfig{FFmpegPath: ffmpeg} }
	h.SubtitleCache = playback.NewSubtitleCache(func() string { return dir })
	h.SubtitleRepo = auxiliarySubtitles{entry: subtitles.DownloadedSubtitle{ID: 123, MediaFileID: file.ID, Format: subtitles.FormatVTT, S3Key: "owned"}}
	h.S3Client = auxiliaryS3{get: func(context.Context) ([]byte, error) {
		return []byte("WEBVTT\n\n00:00.000 --> 00:01.000\nowned downloaded cue\n"), nil
	}}
	var blockFont atomic.Bool
	fontEntered := make(chan struct{})
	fontCanceled := make(chan struct{})
	adapter := h.AuxiliaryProducer(x.api.ResolveAuxiliary, x.api.AcquireAuxiliaryTransfer)
	producer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if blockFont.Load() && strings.HasSuffix(r.URL.Path, "/fonts") {
			w = &auxiliaryBlockedWriter{ResponseWriter: w, ctx: r.Context(), entered: fontEntered, canceled: fontCanceled, revoked: make(chan struct{})}
		}
		adapter.ServeHTTP(w, r)
	}))
	defer producer.Close()
	p := proxy.NewServer(x.watcher, nodesessions.NewTracker(nil, "http://owned-proxy.invalid", "proxy", "proxy")).WithExecutorRuntime(x.egress.Acquire, x.egress.Resolve, x.egress.OpenOutputTransfer)
	opened := make(chan func(), 1)
	open := func(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3) (string, func(), error) {
		id, closePermit, err := x.egress.OpenAuxiliaryTransfer(ctx, transport, executor)
		if blockFont.Load() && err == nil {
			opened <- closePermit
		}
		return id, closePermit, err
	}
	if _, err := p.WithAuxiliaryProducer(producer.URL, open); err != nil {
		t.Fatal(err)
	}
	edge := httptest.NewServer(p.Handler())
	defer edge.Close()
	edge.Client().Transport = &http.Transport{DisableKeepAlives: true}
	token, err := streamtoken.Sign(x.card.ToClaims(), h.JWTSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// No viewer token or session UUID can replace the internal permit.
	internalPath := producer.URL + "/internal/playback/auxiliary/" + x.card.SessionID + "/subtitles/0.ass"
	for _, id := range []string{"", "malformed", uuid.NewString()} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, internalPath, nil)
		req.Header.Set("X-Silo-Stream-Token", token)
		req.Header.Set(playback.AuxiliaryTransferHeaderV3, id)
		resp, err := producer.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 503 {
			t.Fatal("internal permit refusal", resp.StatusCode)
		}
	}
	for _, tc := range []struct {
		path, contains string
		status         int
	}{
		{"0.ass", "owned styled cue", 200}, {"0.sup?embedded_stream_index=3", "PG", 200}, {"0.ass?embedded_stream_index=1", "owned styled cue", 200}, {"0/fonts?embedded_stream_index=1", "Fixture.ttf", 200},
		{"99.vtt?downloaded_subtitle_id=123", "owned downloaded cue", 200}, {"0.ass?file_id=" + strconv.Itoa(file.ID+999), "", 400}, {"0.ass?embedded_stream_index=999", "", 404},
		{"0.ass?embedded_stream_index=1&embedded_stream_index=1", "", 400}, {"0/fonts", "", 404}, {"0.sup?embedded_stream_index=1", "", 415},
	} {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := edge.Client().Get(edge.URL + "/stream/subtitles/" + token + "/" + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || resp.StatusCode != tc.status || !strings.Contains(string(body), tc.contains) {
				t.Fatalf("response %d %q %v", resp.StatusCode, body, err)
			}
			if resp.Header.Get(playback.AuxiliaryTransferHeaderV3) != "" || resp.Header.Get("X-Silo-Stream-Token") != "" {
				t.Fatal("internal authority leaked")
			}
		})
	}
	// The complete SUP is cached after the first GET. HEAD, ranges and
	// conditional requests preserve the API producer's representation semantics.
	supURL := edge.URL + "/stream/subtitles/" + token + "/0.sup?embedded_stream_index=3"
	for _, tc := range []struct {
		method, rangeValue string
		status             int
	}{{http.MethodHead, "", 200}, {http.MethodGet, "bytes=0-12", 206}, {http.MethodGet, "bytes=999999-", 416}} {
		req, _ := http.NewRequestWithContext(ctx, tc.method, supURL, nil)
		if tc.rangeValue != "" {
			req.Header.Set("Range", tc.rangeValue)
		}
		resp, err := edge.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != tc.status || (tc.method == http.MethodHead && len(body) != 0) {
			t.Fatalf("SUP %s %s: %d %v", tc.method, tc.rangeValue, resp.StatusCode, err)
		}
		if tc.method == http.MethodHead {
			req, _ = http.NewRequestWithContext(ctx, http.MethodGet, supURL, nil)
			req.Header.Set("If-Modified-Since", resp.Header.Get("Last-Modified"))
			cached, err := edge.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = cached.Body.Close()
			if cached.StatusCode != 304 {
				t.Fatal("SUP condition", cached.StatusCode)
			}
		}
	}
	var permits int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM playback_auxiliary_transfer_permits WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID).Scan(&permits); err != nil || permits != 0 {
		t.Fatal("permit leak", permits, err)
	}
	// Hold the actual font-body Write. Its permit must remain open while the
	// typed producer has returned items but has not completed the response.
	blockFont.Store(true)
	fontResult := make(chan error, 1)
	go func() {
		resp, err := edge.Client().Get(edge.URL + "/stream/subtitles/" + token + "/0/fonts?embedded_stream_index=1")
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			err = readErr
			if strings.Contains(string(body), "Fixture.ttf") {
				err = fmt.Errorf("expired font body escaped")
			}
		}
		fontResult <- err
	}()
	fontTimeout, fontCancel := context.WithTimeout(ctx, 4*time.Second)
	defer fontCancel()
	select {
	case <-fontEntered:
	case <-fontTimeout.Done():
		t.Fatal("font body was not reached")
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM playback_auxiliary_transfer_permits WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID).Scan(&permits); err != nil || permits != 1 {
		t.Fatal("font permit released before body", permits, err)
	}
	select {
	case closePermit := <-opened:
		closePermit()
	case <-fontTimeout.Done():
		t.Fatal("missing font permit")
	}
	select {
	case <-fontCanceled:
	case <-fontTimeout.Done():
		t.Fatal("font body did not cancel on permit closure")
	}
	select {
	case err := <-fontResult:
		if err != nil && strings.Contains(err.Error(), "escaped") {
			t.Fatal(err)
		}
	case <-fontTimeout.Done():
		t.Fatal("font response did not close")
	}
	blockFont.Store(false)
	// Block real API production until source withdrawal makes its grant fail.
	entered := make(chan struct{})
	canceled := make(chan struct{})
	h.S3Client = auxiliaryS3{get: func(ctx context.Context) ([]byte, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	}}
	result := make(chan error, 1)
	go func() {
		resp, err := edge.Client().Get(edge.URL + "/stream/subtitles/" + token + "/99.vtt?downloaded_subtitle_id=123")
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		result <- err
	}()
	timeout, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	select {
	case <-entered:
	case <-timeout.Done():
		t.Fatal("producer not entered")
	}
	if _, err := f.pool.Exec(ctx, `UPDATE playback_source_registrations SET admission_state='retiring' WHERE user_id=$1`, f.userID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-timeout.Done():
		t.Fatal("withdrawal did not cancel producer")
	}
	select {
	case <-result:
	case <-timeout.Done():
		t.Fatal("proxy response did not end")
	}
	if _, _, err := x.egress.OpenAuxiliaryTransfer(ctx, f.route.TransportID, f.route.Executor); err == nil {
		t.Fatal("withdrawn source opened permit")
	}
}

// Unwrap preserves the real socket deadline while the application writer waits.
type auxiliaryBlockedWriter struct {
	http.ResponseWriter
	ctx               context.Context
	entered, canceled chan struct{}
	once              sync.Once
	revoked           chan struct{}
	revokedOnce       sync.Once
}

func (w *auxiliaryBlockedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *auxiliaryBlockedWriter) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() && !deadline.After(time.Now()) {
		w.revokedOnce.Do(func() { close(w.revoked) })
	}
	return http.NewResponseController(w.ResponseWriter).SetWriteDeadline(deadline)
}
func (w *auxiliaryBlockedWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	select {
	case <-w.revoked:
	case <-w.ctx.Done():
	}
	close(w.canceled)
	return 0, io.ErrClosedPipe
}

// One owned 2x2 PGS image and a clear event, no external media dependency.
func auxiliaryPGSFixture() []byte {
	var data []byte
	packet := func(pts uint32, kind byte, payload []byte) {
		data = append(data, 'P', 'G')
		data = binary.BigEndian.AppendUint32(data, pts)
		data = binary.BigEndian.AppendUint32(data, 0)
		data = append(data, kind)
		data = binary.BigEndian.AppendUint16(data, uint16(len(payload)))
		data = append(data, payload...)
	}
	packet(0, 0x16, []byte{0, 64, 0, 36, 0x10, 0, 0, 0x80, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0})
	packet(0, 0x17, []byte{1, 0, 0, 0, 0, 0, 0, 2, 0, 2})
	packet(0, 0x14, []byte{0, 0, 0, 0, 128, 128, 0, 1, 235, 128, 128, 255})
	packet(0, 0x15, []byte{0, 0, 0, 0xc0, 0, 0, 12, 0, 2, 0, 2, 1, 1, 0, 0, 1, 1, 0, 0})
	packet(0, 0x80, nil)
	packet(90000, 0x16, []byte{0, 64, 0, 36, 0x10, 0, 1, 0, 0, 0, 0})
	packet(90000, 0x80, nil)
	return data
}

func TestAuxiliaryTransferRevocation(t *testing.T) {
	for _, action := range []string{"withdraw", "owner", "expiry", "drain"} {
		t.Run(action, func(t *testing.T) {
			x := newAuxiliaryFixture(t)
			f := x.f
			ctx := t.Context()
			permit, release, err := x.egress.OpenAuxiliaryTransfer(ctx, f.route.TransportID, f.route.Executor)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			g, err := x.api.AcquireAuxiliaryTransfer(ctx, f.route.TransportID, f.route.Executor, permit)
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			switch action {
			case "withdraw":
				_, err = f.pool.Exec(ctx, `UPDATE playback_source_registrations SET admission_state='retiring' WHERE user_id=$1`, f.userID)
			case "owner":
				_, err = f.pool.Exec(ctx, `UPDATE playback_v3_attempts SET control_owner=$2::uuid WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID, uuid.NewString())
			case "expiry":
				_, err = f.pool.Exec(ctx, `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1`, f.authority.PlaybackAttemptID)
			case "drain":
				_, err = f.store.BeginBoundStop(ctx, f.binding, uuid.NewString())
			}
			if err != nil {
				t.Fatal(err)
			}
			timeout, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			select {
			case <-g.Context().Done():
			case <-timeout.Done():
				t.Fatal("auxiliary grant did not close")
			}
			if _, err := x.api.AcquireAuxiliaryTransfer(ctx, f.route.TransportID, f.route.Executor, permit); err == nil {
				t.Fatal("revoked producer reacquired")
			}
		})
	}
}
