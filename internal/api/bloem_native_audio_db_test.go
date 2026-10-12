//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/artworkurl"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

type audioDBProvider struct {
	data   []byte
	onRead func(context.Context) error
}

func (p *audioDBProvider) Stat(_ context.Context, ref mediasource.Ref) (mediasource.Info, error) {
	name := strings.TrimPrefix(ref.EntryID, "track") + ".mp3"
	return mediasource.Info{Name: name, LogicalPath: "Book/" + name, Revision: ref.Revision, Size: int64(len(p.data))}, nil
}
func (p *audioDBProvider) ReadRange(ctx context.Context, _ mediasource.Ref, offset, length int64, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.onRead != nil {
		if err := p.onRead(ctx); err != nil {
			return err
		}
	}
	_, err := w.Write(p.data[offset : offset+length])
	return err
}

type nativeAudioAPIFixture struct {
	pool        *pgxpool.Pool
	deps        Dependencies
	ctx         context.Context
	profile     string
	login       string
	source      storagesource.SourceConfig
	location    storagesource.Location
	files       []*models.MediaFile
	data        []byte
	coordinator *nativestorage.Coordinator
	provider    *audioDBProvider
}

func newNativeAudioAPIFixture(t *testing.T) *nativeAudioAPIFixture {
	t.Helper()
	pool := coverRecipeDatabase(t)
	run := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	var uid int
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role,enabled) VALUES('audio-viewer','audio@example.test','x','user',true) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	stores := pgstore.NewPostgresProvider(pool)
	store, err := stores.ForUser(t.Context(), uid)
	if err != nil {
		t.Fatal(err)
	}
	pid := uuid.NewString()
	if err := store.CreateProfile(t.Context(), userstore.Profile{ID: pid, Name: "Audio viewer"}); err != nil {
		t.Fatal(err)
	}
	profile, err := store.GetProfile(t.Context(), pid)
	if err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if err := auth.NewSessionRepository(pool).Create(t.Context(), models.AuthSession{ID: sid, UserID: uid, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	var folder int
	if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name,enabled) VALUES('audiobooks','Audio fixture',true) RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	var install int64
	if err := pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,runtime_generation,enabled) VALUES('audio-fixture','1','/synthetic',1,true) RETURNING id`).Scan(&install); err != nil {
		t.Fatal(err)
	}
	var owner uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT owner_id FROM plugin_installations WHERE id=$1`, install).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	run(`INSERT INTO bloem_storage_installations(installation_id,owner_id) VALUES($1,$2)`, install, owner)
	run(`INSERT INTO organization_entitlements(organization_id,entitlement_kind,root_kind,root_owner_id,plugin_installation_id,status,granted_by_service) SELECT $1,'plugin_availability','plugin_installation',$2,$3,'active','audio-test' WHERE NOT EXISTS(SELECT 1 FROM organization_entitlements WHERE organization_id=$1 AND plugin_installation_id=$3)`, profile.OrganizationID, owner, install)
	repo := storagesource.NewRepository(pool)
	source, err := repo.CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: owner, InstallationID: &install, PluginID: "audio-fixture", ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	location, err := repo.AddLocationTx(t.Context(), tx, source.Key, folder)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "audio.mp3")
	if out, err := exec.CommandContext(t.Context(), "ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2.75", "-c:a", "libmp3lame", "-y", path).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	run(`INSERT INTO media_items(content_id,type,title) VALUES('native-audio-book','audiobook','Native audio book')`)
	run(`INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES('native-audio-book',$1)`, folder)
	key, err := storagesource.AudioPresentationKey(location.ID, "root")
	if err != nil {
		t.Fatal(err)
	}
	var files []*models.MediaFile
	for i := 1; i <= 2; i++ {
		entry := fmt.Sprintf("track%d", i)
		address, err := storagesource.CatalogLocation(location.ID, entry)
		if err != nil {
			t.Fatal(err)
		}
		file := models.MediaFile{ContentID: "native-audio-book", MediaFolderID: folder, FilePath: address, FileSize: int64(len(data)), BaseType: "audiobook", Container: "mp3", CodecAudio: "mp3", Duration: 2, AudioChannels: 1, ProbeSource: "storage", AudioTracks: []models.AudioTrack{{Codec: "mp3", Channels: 1, Default: true}}, PresentationKind: "multipart", PresentationGroupKey: key, PresentationPartIndex: i, PresentationPartTotal: 2}
		if _, err := scanner.NewFileRepository(pool).Upsert(t.Context(), file); err != nil {
			t.Fatal(err)
		}
		var id int
		if err := pool.QueryRow(t.Context(), `SELECT id FROM media_files WHERE file_path=$1`, address).Scan(&id); err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(t.Context())
		if err := storagesource.AttachFilesTx(t.Context(), tx, 1, []storagesource.FileRef{{MediaFileID: id, PersistedRef: storagesource.PersistedRef{LocationID: location.ID, EntryID: entry, Revision: "v1", LogicalPath: fmt.Sprintf("Book/%d.mp3", i)}, CoverEntryID: "cover/book", CoverRevision: "c1"}}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		actual, err := scanner.NewFileRepository(pool).GetByID(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, actual)
	}
	cipher, err := secret.New([]byte(strings.Repeat("task9-fixture-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "task9-fixture-jwt"
	cfg.Playback.FFmpegPath = "ffmpeg"
	redisURL := os.Getenv("SILO_TEST_REDIS_URL")
	if redisURL == "" {
		t.Fatal("SILO_TEST_REDIS_URL required")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	deps := Dependencies{DB: pool, Config: cfg, SecretCipher: cipher, UserStoreProvider: stores, FileRepo: scanner.NewFileRepository(pool), RedisClient: client}
	policy := nativeStorageActorPolicy(deps)
	jwtService := auth.NewJWTService(cfg.Auth.JWTSecret, time.Hour, time.Hour)
	token, err := jwtService.GenerateAccessToken(uid, "user", sid)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := jwtService.ValidateToken(token)
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := policy.tenants.ResolveSubjectTenant(t.Context(), uid, pid)
	if err != nil {
		t.Fatal(err)
	}
	ctx := tenancy.WithContext(middleware.SetClaims(t.Context(), claims), tenant)
	scope, err := policy.viewer.ResolveCurrent(ctx, access.ResolveInput{UserID: uid, SessionID: sid, ProfileID: pid})
	if err != nil {
		t.Fatal(err)
	}
	ctx = middleware.SetProfileID(access.SetScope(ctx, scope), pid)
	ctx = context.WithValue(ctx, nativeVideoRequestKey{}, nativeVideoRequest{authorization: "Bearer " + token, method: "POST", path: "/api/v1/playback/start"})
	registry := &coverRecipeRegistry{snapshot: &plugins.NativeStorageSnapshot{Source: source, Installation: &plugins.Installation{ID: int(install), Enabled: true}, Generation: 1, ArtifactChecksum: "approved"}}
	provider := &audioDBProvider{data: data}
	coordinator := &nativestorage.Coordinator{References: repo, Registry: registry, Runtime: coverDBRuntime{nativestorage.Lease{Context: t.Context(), Source: provider, Description: &storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: "books", RootEntryId: "root", RevisionPinnedReads: true, MediaKinds: []string{"audiobooks"}, DirectoryOnlyListing: true}}}}}}
	return &nativeAudioAPIFixture{pool: pool, deps: deps, ctx: ctx, profile: pid, login: sid, source: source, location: location, files: files, data: data, coordinator: coordinator, provider: provider}
}

func (f *nativeAudioAPIFixture) handlers(t *testing.T) (*handlers.PlaybackHandler, *handlers.StreamHandler, *playback.SessionManager) {
	t.Helper()
	sessions := playback.NewSessionManager(0, 0)
	h := handlers.NewPlaybackHandler(sessions, f.deps.FileRepo)
	h.ItemAccess = catalog.NewItemRepository(f.pool)
	h.FileVersionFetcher = f.deps.FileRepo
	h.StoreProvider = f.deps.UserStoreProvider
	h.PlaybackConfig = func() config.PlaybackConfig { return f.deps.Config.Playback }
	h.JWTSecret = f.deps.Config.Auth.JWTSecret
	service := &handlers.NativeVideoService{AudioReady: true, Presentation: nativeAudioPresentation(f.deps), Files: f.deps.FileRepo, Bindings: handlers.NewNativeVideoBindingStore(f.deps.RedisClient), Sessions: sessions, Deny: playback.NewStreamDeny(f.deps.RedisClient)}
	h.NativeVideo = service
	h.StreamDeny = service.Deny
	p := nativeStorageActorPolicy(f.deps)
	p.resolveFile = h.ResolveNativeVideoFile
	p.getSession = sessions.GetSession
	p.denied = nativeVideoSessionDenied(f.deps.RedisClient, service.Deny)
	service.Open = func(ctx context.Context, file *models.MediaFile) (handlers.NativeVideoFile, error) {
		authority, err := nativeVideoAuthority(ctx, file.ID, p)
		if err != nil {
			return nil, err
		}
		opened, err := f.coordinator.OpenAuthorizedPlayback(ctx, file, authority)
		if err != nil {
			return nil, err
		}
		return opened, nil
	}
	configureNativeVideoDelegates(f.deps, f.coordinator, service, p)
	stream := handlers.NewStreamHandler(sessions, f.deps.FileRepo)
	stream.NativeVideo = service
	stream.StreamDeny = service.Deny
	stream.JWTSecret = h.JWTSecret
	stream.PlaybackConfig = h.PlaybackConfig
	return h, stream, sessions
}

func (f *nativeAudioAPIFixture) startResponse(t *testing.T, h *handlers.PlaybackHandler, index int, position float64) *httptest.ResponseRecorder {
	t.Helper()
	request := playback.StartRequestV3{ProtocolVersion: playback.ProtocolV3, ClientFeatures: []string{playback.FeaturePlaybackPlanV3, playback.FeatureHeaderAuthenticatedMediaV3}, FileID: f.files[index].ID, ProfileID: f.profile, PlaybackAttemptID: uuid.NewString(), QualityPreference: "auto", SubtitleFidelityPreference: playback.SubtitleFidelityCompatibleV3, StartPosition: &position, Capabilities: playback.ClientCodecCapabilitiesV3{VideoEvidence: playback.EvidenceExactV3, AudioEvidence: playback.EvidenceExactV3, CodecsVideo: []string{"h264"}, CodecsAudio: []string{"mp3"}, Containers: []string{"mp3"}}, ClientPlaybackContext: playback.ClientPlaybackContextV3{ProtocolVersion: playback.ProtocolV3, FormFactor: "mobile", AppVersion: "test", Device: playback.DeviceContextV3{Platform: "android"}, Output: playback.OutputContextV3{OutputContextID: "audio-fixture"}, Deliveries: map[string]playback.DeliveryCapabilityV3{playback.DeliveryClassOriginalHTTPV3: {Enabled: true, SupportedOnDevice: true}}}}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.HandleStartPlayback(w, httptest.NewRequest("POST", "/api/v1/playback/start", bytes.NewReader(body)).WithContext(f.ctx))
	return w
}

func (f *nativeAudioAPIFixture) start(t *testing.T, h *handlers.PlaybackHandler, index int, position float64) playback.DecisionResponseV3 {
	t.Helper()
	w := f.startResponse(t, h, index, position)
	var response playback.DecisionResponseV3
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.PlaybackPlan == nil || response.PlaybackPlan.Delivery != playback.DeliveryOriginalHTTPV3 {
		t.Fatalf("start=%d %s", w.Code, w.Body.String())
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		keys := []string{playback.StreamDenyKey(response.SessionID), "bloem:native-video-session:" + response.SessionID}
		for _, file := range f.files {
			keys = append(keys, fmt.Sprintf("bloem:native-video:%s:%d", response.SessionID, file.ID))
		}
		if err := f.deps.RedisClient.Del(ctx, keys...).Err(); err != nil {
			t.Errorf("session fixture cleanup: %v", err)
		}
	})
	return response
}

func (f *nativeAudioAPIFixture) stream(t *testing.T, h *handlers.StreamHandler, started playback.DecisionResponseV3, method string, validators ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, started.PlaybackPlan.Stream.URL, nil).WithContext(f.ctx)
	route := chi.NewRouteContext()
	route.URLParams.Add("session_id", started.SessionID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, route))
	r.Header.Set("Range", "bytes=2-31")
	if len(validators) > 0 {
		r.Header.Set("If-None-Match", validators[0])
	}
	w := httptest.NewRecorder()
	func() {
		defer func() {
			if value := recover(); value != nil && value != http.ErrAbortHandler {
				panic(value)
			}
		}()
		h.HandleStream(w, r)
	}()
	return w
}

func TestNativeAudioPlaybackStartSeekNextTrackResumeDB(t *testing.T) {
	f := newNativeAudioAPIFixture(t)
	h, stream, _ := f.handlers(t)
	first := f.start(t, h, 0, 0)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		w := f.stream(t, stream, first, method)
		if w.Code != 206 || w.Header().Get("Content-Type") != "audio/mpeg" {
			t.Fatalf("stream=%d %s", w.Code, w.Body.String())
		}
		if method == http.MethodGet && !bytes.Equal(w.Body.Bytes(), f.data[2:32]) {
			t.Fatal("range differs from fixture")
		}
		if method == http.MethodHead && w.Body.Len() != 0 {
			t.Fatal("HEAD returned bytes")
		}
	}
	stop := httptest.NewRequest("POST", "/api/v1/playback/"+first.SessionID+"/stop", nil).WithContext(f.ctx)
	route := chi.NewRouteContext()
	route.URLParams.Add("session_id", first.SessionID)
	stop = stop.WithContext(context.WithValue(stop.Context(), chi.RouteCtxKey, route))
	stopped := httptest.NewRecorder()
	h.HandleStopPlayback(stopped, stop)
	if stopped.Code != http.StatusNoContent {
		t.Fatalf("stop=%d %s", stopped.Code, stopped.Body.String())
	}
	if w := f.stream(t, stream, first, http.MethodGet); w.Code < 400 {
		t.Fatalf("stopped stream=%d", w.Code)
	}
	second := f.start(t, h, 1, 1.25)
	if second.PlaybackPlan.EffectiveMediaFileID != f.files[1].ID || second.PlaybackPlan.Timeline.SourceStartSeconds != 1.25 {
		t.Fatalf("next track=%+v", second.PlaybackPlan)
	}
	progress := handlers.NewProgressHandler(f.deps.UserStoreProvider)
	progress.LibraryLookup = catalog.NewLibraryItemRepository(f.pool)
	w := httptest.NewRecorder()
	progress.HandleBloemSyncProgress(w, httptest.NewRequest("POST", "/api/bloem/v1/sync/progress", strings.NewReader(`{"items":[{"media_item_id":"native-audio-book","position":3.25,"duration":4}]}`)).WithContext(f.ctx))
	if w.Code != 200 {
		t.Fatalf("progress=%d %s", w.Code, w.Body.String())
	}
	store, err := f.deps.UserStoreProvider.ForUser(f.ctx, middleware.GetUserID(f.ctx))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetProgress(f.ctx, f.profile, "native-audio-book")
	if err != nil || saved == nil || saved.PositionSeconds != 3.25 {
		t.Fatalf("progress=%+v err=%v", saved, err)
	}
	fresh, freshStream, _ := f.handlers(t)
	resumed := f.start(t, fresh, 1, 1.25)
	if w := f.stream(t, freshStream, resumed, http.MethodGet); w.Code != 206 || !bytes.Equal(w.Body.Bytes(), f.data[2:32]) {
		t.Fatalf("restart=%d %s", w.Code, w.Body.String())
	}
}

func TestNativeAudioPlaybackRevocationDB(t *testing.T) {
	for _, mode := range []string{"not-composed", "missing"} {
		t.Run("start/"+mode, func(t *testing.T) {
			f := newNativeAudioAPIFixture(t)
			h, _, _ := f.handlers(t)
			if mode == "not-composed" {
				h.NativeVideo.AudioReady = false
			} else {
				if _, err := f.pool.Exec(t.Context(), `UPDATE media_files SET missing_since=clock_timestamp() WHERE id=$1`, f.files[0].ID); err != nil {
					t.Fatal(err)
				}
			}
			w := f.startResponse(t, h, 0, 0)
			if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"not_found"`) {
				t.Fatalf("unavailable start=%d %s", w.Code, w.Body.String())
			}
		})
	}

	for _, phase := range []string{"before", "during"} {
		for _, mutation := range []string{"root", "session", "source", "missing", "sibling-membership", "sibling-reference"} {
			t.Run(phase+"/"+mutation, func(t *testing.T) {
				f := newNativeAudioAPIFixture(t)
				h, stream, _ := f.handlers(t)
				started := f.start(t, h, 0, 0)
				etag := f.stream(t, stream, started, http.MethodHead).Header().Get("ETag")
				var sql string
				var arg any
				switch mutation {
				case "sibling-membership":
					sql = `UPDATE bloem_storage_file_roots SET enabled=false WHERE media_file_id=$1`
					arg = f.files[1].ID
				case "sibling-reference":
					sql = `DELETE FROM bloem_storage_file_refs WHERE media_file_id=$1`
					arg = f.files[1].ID
				case "root":
					sql = `UPDATE bloem_storage_selected_roots SET enabled=false WHERE location_id=$1`
					arg = f.location.ID
				case "session":
					sql = `UPDATE auth_sessions SET revoked_at=clock_timestamp() WHERE id=$1`
					arg = f.login
				case "source":
					sql = `UPDATE bloem_storage_sources SET enabled=false WHERE key=$1`
					arg = f.source.Key
				case "missing":
					sql = `UPDATE media_files SET missing_since=clock_timestamp() WHERE id=$1`
					arg = f.files[0].ID
				}
				mutate := func(ctx context.Context) error { _, err := f.pool.Exec(ctx, sql, arg); return err }
				if phase == "during" {
					f.provider.onRead = mutate
				} else if err := mutate(t.Context()); err != nil {
					t.Fatal(err)
				}
				if phase == "before" {
					for _, method := range []string{http.MethodHead, http.MethodGet} {
						if denied := f.stream(t, stream, started, method, etag); denied.Code < 400 {
							t.Errorf("revoked conditional %s=%d", method, denied.Code)
						}
					}
				}
				w := f.stream(t, stream, started, http.MethodGet)
				if (phase == "before" && w.Code < 400) || bytes.Contains(w.Body.Bytes(), f.data[2:32]) || (phase == "during" && w.Body.Len() != 0) {
					t.Fatalf("revoked=%d body=%s", w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestNativeAudioHiddenPresentationServesNoCoverDB(t *testing.T) {
	for _, mutation := range []string{"hidden", "root", "sibling-membership", "sibling-reference"} {
		t.Run(mutation, func(t *testing.T) {
			f := newNativeAudioAPIFixture(t)
			signer := artworkurl.NewSigner(f.deps.Config.Auth.JWTSecret, time.Hour)
			service := &nativeStorageArtwork{next: artworkurl.NewServerResolver(signer), signer: signer, cipher: f.deps.SecretCipher, actors: nativeStorageActorPolicy(f.deps), references: storagesource.NewRepository(f.pool), registry: f.coordinator.Registry, authorizeFile: nativeStorageCoverFileAuthority(f.deps)}
			key := artworkkey.StorageCoverKey("native-audio-book", "cover/book", "c1")
			resolved := service.ResolveURLs(f.ctx, []string{key})[key]
			if resolved.URL == "" {
				t.Fatal("visible presentation has no cover recipe")
			}
			host := &nativestorage.Host{}
			host.SetCoverRequestAuthorizer(service.authorizeRequest)
			provider := &coverDBSource{data: append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte("cover"), 16)...)}
			f.deps.NativeAudioScannerReady = true
			f.deps.NativeAudioGate = new(atomic.Bool)
			if err := nativeStorageCoverAuthority(f.deps)(t.Context(), f.location, f.source); err == nil {
				t.Fatal("cover admitted before full composition")
			}
			f.deps.NativeAudioGate.Store(true)
			host.SetCoverReader(&nativestorage.Coordinator{References: storagesource.NewRepository(f.pool), Registry: f.coordinator.Registry, Runtime: coverDBRuntime{nativestorage.Lease{Context: t.Context(), Source: provider, Description: &storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: "books", RootEntryId: "root", RevisionPinnedReads: true}}}}}, AuthorizeCover: nativeStorageCoverAuthority(f.deps)})
			host.SetCoverCache(nativestorage.NewRedisCoverCache(f.deps.RedisClient, time.Minute))
			blobs, err := blobstore.NewFilesystem(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			h := apiv2.NewBloemArtworkHandler(blobs, signer, nil, host)
			request := func(etag string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodGet, resolved.URL, nil)
				r.Header.Set("If-None-Match", etag)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			first := request("")
			if first.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), provider.data) {
				t.Fatalf("cover=%d %s", first.Code, first.Body.String())
			}
			before := provider.reads.Load()
			if hit := request(""); hit.Code != http.StatusOK || provider.reads.Load() != before {
				t.Fatalf("cache=%d reads=%d", hit.Code, provider.reads.Load())
			}
			etag := first.Header().Get("ETag")
			if hit := request(etag); hit.Code != http.StatusNotModified {
				t.Fatalf("conditional=%d", hit.Code)
			}
			var sql string
			if mutation == "sibling-membership" {
				_, err = f.pool.Exec(t.Context(), `UPDATE bloem_storage_file_roots SET enabled=false WHERE media_file_id=$1`, f.files[1].ID)
			} else if mutation == "sibling-reference" {
				_, err = f.pool.Exec(t.Context(), `DELETE FROM bloem_storage_file_refs WHERE media_file_id=$1`, f.files[1].ID)
			} else if mutation == "hidden" {
				sql = `UPDATE media_files SET missing_since=clock_timestamp() WHERE presentation_group_key=$1`
				_, err = f.pool.Exec(t.Context(), sql, f.files[0].PresentationGroupKey)
			} else {
				sql = `UPDATE bloem_storage_selected_roots SET enabled=false WHERE location_id=$1`
				_, err = f.pool.Exec(t.Context(), sql, f.location.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, validator := range []string{"", etag} {
				if denied := request(validator); denied.Code != http.StatusNotFound || bytes.Equal(denied.Body.Bytes(), provider.data) {
					t.Fatalf("revoked=%d %s", denied.Code, denied.Body.String())
				}
			}
			if provider.reads.Load() != before {
				t.Fatal("revoked recipe read provider")
			}
			if again := service.ResolveURLs(f.ctx, []string{key})[key]; again.URL != "" {
				t.Fatal("hidden presentation minted a recipe")
			}
		})
	}
}
