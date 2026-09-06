package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/diagnostics"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/playback/testfixture"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func TestInitialPlaybackRootRouterV2Synthetic(t *testing.T) {
	for _, transcode := range []bool{false, true} {
		name := "direct"
		if transcode {
			name = "hls"
		}
		t.Run(name, func(t *testing.T) { testInitialPlaybackRootRouterV2Synthetic(t, transcode) })
	}
}

func testInitialPlaybackRootRouterV2Synthetic(t *testing.T, transcode bool) {
	dsn, redisURL := os.Getenv("SILO_PLAYBACK_ROUTER_TEST_DATABASE_URL"), os.Getenv("SILO_TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("SILO_PLAYBACK_ROUTER_TEST_DATABASE_URL and SILO_TEST_REDIS_URL required")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	redisClient := redis.NewClient(redisOpts)
	defer func() { _ = redisClient.Close() }()
	synthetic, err := testfixture.ProvisionPostgres(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, synthetic.AccountID) }()
	provider := pgstore.NewPostgresProvider(pool)
	var folderID, fileID int
	itemID := "synthetic-router-" + uuid.NewString()
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('movies',$1) RETURNING id`, itemID).Scan(&folderID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_folders WHERE id=$1`, folderID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_items WHERE content_id=$1`, itemID)
	}()
	if _, err := pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title) VALUES($1,'movie','Synthetic router media')`, itemID); err != nil {
		t.Fatal(err)
	}
	mediaPath := filepath.Join(t.TempDir(), "synthetic.mp4")
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	videoCodec, videoProfile, videoLevel := "h264", "high", 41
	codecArgs := []string{"-c:v", "libx264", "-profile:v", "high", "-level:v", "4.1"}
	if transcode {
		videoCodec, videoProfile, videoLevel = "mpeg4", "simple", 0
		codecArgs = []string{"-c:v", "mpeg4"}
	}
	mediaArgs := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "2"}
	mediaArgs = append(mediaArgs, codecArgs...)
	mediaArgs = append(mediaArgs, "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2", "-movflags", "+faststart", mediaPath)
	output, err := exec.CommandContext(ctx, ffmpeg, mediaArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("fixture media: %v %s", err, output)
	}
	media, err := os.ReadFile(mediaPath)
	if err != nil {
		t.Fatal(err)
	}
	video, _ := json.Marshal([]models.VideoTrack{{Codec: videoCodec, Profile: videoProfile, Level: videoLevel, Width: 320, Height: 180, FrameRate: "24/1", BitDepth: 8, VideoRange: "SDR", VideoRangeType: "SDR"}})
	audio, _ := json.Marshal([]models.AudioTrack{{Codec: "aac", Channels: 2, Layout: "stereo", Default: true}})
	if err := pool.QueryRow(ctx, `INSERT INTO media_files(content_id,media_folder_id,file_path,file_size,container,codec_video,codec_audio,resolution,bitrate,audio_channels,duration,video_tracks,audio_tracks,probe_source,probe_updated_at) VALUES($1,$2,$3,$4,'mp4',$7,'aac','1080p',8000,2,2,$5,$6,'ffprobe',now()) RETURNING id`, itemID, folderID, mediaPath, len(media), video, audio, videoCodec).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFromDB(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Auth.JWTSecret = "synthetic-router-test-secret"
	cfg.Playback.TranscodeEnabled = transcode
	cfg.Playback.HWAccel = "none"
	cfg.Playback.FFmpegPath = ffmpeg
	cfg.Playback.TranscodeDir = t.TempDir()
	cipher, err := secret.New(bytes.Repeat([]byte{1}, secret.MinMasterKeyLen))
	if err != nil {
		t.Fatal(err)
	}
	installation, err := diagnostics.ServerInstanceID(ctx, catalog.NewServerSettingsRepo(pool))
	if err != nil {
		t.Fatal(err)
	}
	ownerPolicy := playback.RuntimeGrantPolicyV3{MaxDuration: 30 * time.Second, SafetyMargin: time.Second, RenewBefore: 10 * time.Second, PollInterval: 10 * time.Millisecond}
	grantPolicy := playback.RuntimeGrantPolicyV3{MaxDuration: time.Second, SafetyMargin: 100 * time.Millisecond, RenewBefore: 200 * time.Millisecond, PollInterval: 10 * time.Millisecond}
	flow, err := NewInitialPlaybackRuntime(ctx, pool, redisClient, provider, installation, ownerPolicy, grantPolicy)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewInitialPlaybackRuntime(ctx, pool, redisClient, provider, installation, ownerPolicy, grantPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.InstallationID != flow.InstallationID || restarted.OwnerID == flow.OwnerID || restarted.OwnerID == "" {
		t.Fatal("runtime recreation must retain persisted installation identity and allocate a fresh boot owner")
	}
	if mismatched, err := NewInitialPlaybackRuntime(ctx, pool, redisClient, provider, uuid.NewString(), ownerPolicy, grantPolicy); err == nil || mismatched != nil {
		t.Fatal("mismatched installation identity did not fail closed")
	}
	manager := playback.NewSessionManager(0, 0)
	server := httptest.NewServer(NewRouter(Dependencies{Config: cfg, AppContext: ctx, DB: pool, SecretCipher: cipher, UserStoreProvider: provider, RedisClient: redisClient, SessionMgr: manager, FileRepo: scanner.NewFileRepository(pool), FolderRepo: catalog.NewFolderRepository(pool), ClientIPResolver: clientip.NewResolver(nil), NodeID: "synthetic-router", InitialPlayback: flow}))
	defer server.Close()
	jwtService := auth.NewJWTService(cfg.Auth.JWTSecret, time.Hour, time.Hour)
	loginID := uuid.NewString()
	if err := auth.NewSessionRepository(pool).Create(ctx, models.AuthSession{ID: loginID, UserID: synthetic.AccountID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	token, err := jwtService.GenerateAccessToken(synthetic.AccountID, "user", loginID)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any, accessToken, profile string) (int, []byte) {
		t.Helper()
		var encoded []byte
		if body != nil {
			var err error
			encoded, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("X-Profile-Id", profile)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := res.Body.Close(); err != nil {
				t.Error(err)
			}
		}()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, data
	}
	if _, err := access.NewResolver(auth.NewUserRepository(pool), provider, access.NewProfileTokenService(cfg.Auth.JWTSecret, 0), access.NewGroupStore(pool)).Resolve(ctx, access.ResolveInput{UserID: synthetic.AccountID, SessionID: loginID, ProfileID: synthetic.ProfileID}); err != nil {
		t.Fatalf("real viewer access: %v", err)
	}
	status, data := call(http.MethodGet, "/api/v2/playback/capabilities", nil, token, synthetic.ProfileID)
	if status != 200 {
		t.Fatalf("capabilities: %d %s", status, data)
	}
	var capability struct {
		InstallationID string `json:"installation_id"`
		Allowed        bool   `json:"allowed"`
	}
	if err := json.Unmarshal(data, &capability); err != nil || !capability.Allowed || capability.InstallationID != installation {
		t.Fatalf("capability: %s %v", data, err)
	}
	request := map[string]any{"installation_id": installation, "protocol_version": 3, "client_features": []string{playback.FeaturePlaybackPlanV3}, "file_id": strconv.Itoa(fileID), "profile_id": synthetic.ProfileID, "playback_attempt_id": uuid.NewString(), "quality_preference": "original", "metered": false, "subtitle_fidelity_preference": "compatible", "client_capabilities": playback.ClientCodecCapabilitiesV3{VideoEvidence: playback.EvidenceExactV3, AudioEvidence: playback.EvidenceExactV3, CodecsVideo: []string{"h264"}, CodecsVideoHardware: []string{"h264"}, CodecsAudio: []string{"aac"}, Containers: []string{"mp4"}, MaxResolution: "1080p", VideoDecode: []playback.VideoDecodeCapabilityV3{{Codec: "h264", Profiles: []string{"high"}, Levels: []int{41}, BitDepths: []int{8}, MaxWidth: 1920, MaxHeight: 1080, MaxFrameRate: 60, MaxBitrateKbps: 20000, Hardware: true}}}, "client_playback_context": playback.ClientPlaybackContextV3{ProtocolVersion: 3, FormFactor: "tv", AppVersion: "test", Device: playback.DeviceContextV3{Platform: "android"}, Output: playback.OutputContextV3{OutputContextID: "synthetic-output"}, Deliveries: map[string]playback.DeliveryCapabilityV3{playback.DeliveryClassOriginalHTTPV3: {Enabled: true, SupportedOnDevice: true, Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioDecodeCodecs: []string{"aac"}, AudioPassthroughCodecs: []string{}, Features: []string{}, ValidatedClaims: []string{}, Transformations: []playback.TransformationV3{}, Subtitles: playback.DeliverySubtitleCapabilitiesV3{EmbeddedText: true, SidecarText: true}}}}}
	if transcode {
		clientContext := request["client_playback_context"].(playback.ClientPlaybackContextV3)
		hls := clientContext.Deliveries[playback.DeliveryClassOriginalHTTPV3]
		hls.Containers = []string{"hls"}
		clientContext.Deliveries[playback.DeliveryClassHLSV3] = hls
		request["client_playback_context"] = clientContext
	}
	status, data = call(http.MethodPost, "/api/v2/playback/start", request, token, synthetic.ProfileID)
	if status != 201 {
		t.Fatalf("start: %d %s", status, data)
	}
	var decision struct {
		SessionID string `json:"session_id"`
		Plan      struct {
			Stream    playback.StreamV3 `json:"stream"`
			Requested string            `json:"requested_media_file_id"`
		} `json:"playback_plan"`
	}
	if err := json.Unmarshal(data, &decision); err != nil || decision.SessionID == "" || decision.Plan.Requested != strconv.Itoa(fileID) {
		t.Fatalf("decision %s: %v", data, err)
	}
	expectedPrefix := "/api/v2/stream/"
	if transcode {
		expectedPrefix = "/api/v2/playback/transcode/"
	}
	if !strings.HasPrefix(decision.Plan.Stream.URL, expectedPrefix) {
		t.Fatal("v2 start returned the wrong delivery namespace")
	}
	status, data = call(http.MethodGet, decision.Plan.Stream.URL, nil, token, synthetic.ProfileID)
	if transcode {
		if status != 200 || !bytes.HasPrefix(data, []byte("#EXTM3U")) {
			t.Fatalf("manifest: %d %s", status, data)
		}
		base, err := url.Parse(decision.Plan.Stream.URL)
		if err != nil {
			t.Fatal(err)
		}
		segmentURL := ""
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				ref, err := url.Parse(line)
				if err != nil {
					t.Fatal(err)
				}
				segmentURL = base.ResolveReference(ref).String()
				break
			}
		}
		if !strings.HasPrefix(segmentURL, "/api/v2/playback/transcode/") {
			t.Fatal("HLS segment escaped v2 delivery")
		}
		status, data = call(http.MethodGet, segmentURL, nil, token, synthetic.ProfileID)
		if status != 200 || len(data) == 0 {
			t.Fatalf("segment: %d bytes=%d", status, len(data))
		}
	} else if status != 200 || !bytes.Equal(data, media) {
		t.Fatalf("media: %d bytes=%d want=%d", status, len(data), len(media))
	}
	status, data = call(http.MethodGet, strings.Split(decision.Plan.Stream.URL, "?")[0], nil, token, synthetic.ProfileID)
	if status != 503 || !bytes.Contains(data, []byte("dependency_unavailable")) {
		t.Fatalf("missing signed authority: %d %s", status, data)
	}
	if !transcode {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			r, err := http.NewRequestWithContext(ctx, method, server.URL+decision.Plan.Stream.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("X-Profile-Id", synthetic.ProfileID)
			r.Header.Set("Accept-Encoding", "gzip")
			if method == http.MethodGet {
				r.Header.Set("Range", "bytes=0-31")
			}
			res, err := server.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			payload, readErr := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if readErr != nil || res.Header.Get("Content-Encoding") != "" {
				t.Fatalf("media encoding/read: %v %v", readErr, res.Header)
			}
			if method == http.MethodGet && (res.StatusCode != 206 || !bytes.Equal(payload, media[:32])) {
				t.Fatalf("range bytes: %d %d", res.StatusCode, len(payload))
			}
			if method == http.MethodHead && (res.StatusCode != 200 || len(payload) != 0 || res.ContentLength != int64(len(media))) {
				t.Fatalf("HEAD bytes: %d body=%d length=%d", res.StatusCode, len(payload), res.ContentLength)
			}
		}
	}
	path := "/api/v2/playback/" + decision.SessionID
	status, data = call(http.MethodPost, path+"/progress", map[string]any{"installation_id": installation, "sequence": 1, "position": 1.0, "is_paused": false}, token, synthetic.ProfileID)
	if status != 200 {
		t.Fatalf("progress: %d %s", status, data)
	}
	var mutation struct {
		Accepted struct {
			Sequence int64   `json:"sequence"`
			Position float64 `json:"position"`
		} `json:"accepted"`
		StopID string `json:"stop_id"`
	}
	if err := json.Unmarshal(data, &mutation); err != nil || mutation.Accepted.Sequence != 1 || mutation.Accepted.Position != 1 {
		t.Fatalf("progress receipt %s: %v", data, err)
	}
	stop := map[string]any{"installation_id": installation, "stop_id": uuid.NewString(), "sequence": 2, "position": 1.5, "is_paused": false}
	deadline := time.Now().Add(5 * time.Second)
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	for {
		status, data = call(http.MethodDelete, path, stop, token, synthetic.ProfileID)
		if status != 202 || time.Now().After(deadline) {
			break
		}
		select {
		case <-poll.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if status != 200 {
		t.Fatalf("stop: %d %s", status, data)
	}
	if err := json.Unmarshal(data, &mutation); err != nil || mutation.StopID != stop["stop_id"] || mutation.Accepted.Sequence != 2 || mutation.Accepted.Position != 1.5 {
		t.Fatalf("stop receipt %s: %v", data, err)
	}
	status, data = call(http.MethodGet, decision.Plan.Stream.URL, nil, token, synthetic.ProfileID)
	if status != 503 || !bytes.Contains(data, []byte("dependency_unavailable")) {
		t.Fatalf("stopped delivery served bytes: %d %s", status, data)
	}
	// A separate existing account is never enrolled by capabilities or start.
	var otherID int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role,access_group_id) VALUES($1::text,$1::text||'@example.invalid','synthetic-disabled','user',(SELECT id FROM access_groups WHERE is_default)) RETURNING id`, "unregistered-synthetic-"+uuid.NewString()).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, otherID) }()
	otherProfile := uuid.NewString()
	otherStore, _ := provider.ForUser(ctx, otherID)
	if err := otherStore.CreateProfile(ctx, userstore.Profile{ID: otherProfile, Name: "Unregistered"}); err != nil {
		t.Fatal(err)
	}
	otherLogin := uuid.NewString()
	if err := auth.NewSessionRepository(pool).Create(ctx, models.AuthSession{ID: otherLogin, UserID: otherID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	otherToken, _ := jwtService.GenerateAccessToken(otherID, "user", otherLogin)
	status, data = call(http.MethodGet, "/api/v2/playback/capabilities", nil, otherToken, otherProfile)
	if status != 200 {
		t.Fatalf("unregistered capability: %d %s", status, data)
	}
	if err := json.Unmarshal(data, &capability); err != nil || capability.Allowed {
		t.Fatalf("unregistered capability admission: %s %v", data, err)
	}
	request["profile_id"] = otherProfile
	request["playback_attempt_id"] = uuid.NewString()
	status, data = call(http.MethodPost, "/api/v2/playback/start", request, otherToken, otherProfile)
	if status < 400 {
		t.Fatalf("unregistered start accepted: %d %s", status, data)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM playback_source_markers WHERE user_id=$1)+(SELECT count(*) FROM playback_source_registrations WHERE user_id=$1)+(SELECT count(*) FROM playback_v3_attempts WHERE user_id=$1)`, otherID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("enrollment side effects=%d: %v", count, err)
	}
}
