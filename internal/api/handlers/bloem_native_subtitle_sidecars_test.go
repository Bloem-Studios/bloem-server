package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// nativeSidecarStore stands in for the bloem_storage_episode_sidecars rows of
// the series scan: what the listing reads.
type nativeSidecarStore struct {
	mu   sync.Mutex
	rows []storagesource.Sidecar
}

func (s *nativeSidecarStore) FileSidecars(_ context.Context, fileID int) ([]storagesource.Sidecar, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []storagesource.Sidecar
	for _, row := range s.rows {
		if row.MediaFileID == fileID {
			out = append(out, row)
		}
	}
	return out, nil
}
func (s *nativeSidecarStore) setRevision(entry, revision string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rows {
		if s.rows[i].EntryID == entry {
			s.rows[i].Revision = revision
		}
	}
}

// nativeSidecarProvider stands in for the storage plugin behind
// OpenSidecar: bytes by entry and revision, and every open it admitted.
type nativeSidecarProvider struct {
	mu       sync.Mutex
	identity nativestorage.PlaybackIdentity
	objects  map[string]string
	opens    []storagesource.PersistedRef
	bindings []NativeVideoBinding
	err      error
	wrap     func(NativeVideoFile) NativeVideoFile
}

func (p *nativeSidecarProvider) open(_ context.Context, file *models.MediaFile, binding NativeVideoBinding, ref storagesource.PersistedRef) (NativeVideoFile, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.opens = append(p.opens, ref)
	p.bindings = append(p.bindings, binding)
	if p.err != nil {
		return nil, p.err
	}
	data, ok := p.objects[ref.EntryID+"@"+ref.Revision]
	if !ok {
		return nil, storagesource.ErrReferenceConflict
	}
	identity := p.identity
	identity.Reference = ref
	var opened NativeVideoFile = &videoFileFixture{Reader: bytes.NewReader([]byte(data)), id: identity, info: mediasource.Info{Name: ref.EntryID, LogicalPath: ref.LogicalPath, Revision: ref.Revision, Size: int64(len(data))}, closed: new(int)}
	if p.wrap != nil {
		opened = p.wrap(opened)
	}
	return opened, nil
}
func (p *nativeSidecarProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.opens)
}

const (
	nativeSidecarSDH    = "1\n00:00:01,000 --> 00:00:02,500\n[door creaks] Hearing-impaired line\n"
	nativeSidecarPlain  = "1\n00:00:01,000 --> 00:00:02,500\nPlain English line\n"
	nativeSidecarForced = "[Script Info]\nScriptType: v4.00+\n\n[Events]\nFormat: Layer, Start, End, Style, Text\nDialogue: 0,0:00:01.00,0:00:02.00,Default,Forced Dutch sign\n"
)

func nativeSidecarFixture(t *testing.T) (*StreamHandler, *models.MediaFile, *playback.Session, *nativeSidecarStore, *nativeSidecarProvider) {
	t.Helper()
	service, file, session, identity, closed := nativeVideoFixture(t)
	location := uuid.New()
	identity.Reference.LocationID, identity.Reference.EntryID, identity.Reference.LogicalPath = location, "Show/Season 01/Show - S01E01.mkv", "Show/Season 01/Show - S01E01.mkv"
	file.FilePath, _ = storagesource.CatalogLocation(location, identity.Reference.EntryID)
	identity.FilePath = file.FilePath
	service.Open = func(context.Context, *models.MediaFile) (NativeVideoFile, error) {
		return &videoFileFixture{Reader: bytes.NewReader([]byte("0123456789")), id: *identity, info: mediasource.Info{Name: "Show - S01E01.mkv", LogicalPath: identity.Reference.LogicalPath, Size: 10, Revision: identity.Reference.Revision}, closed: closed}, nil
	}
	if err := service.Bind(t.Context(), session, file); err != nil {
		t.Fatal(err)
	}
	sidecar := func(name, format, language string, forced, sdh bool) storagesource.Sidecar {
		entry := "Show/Season 01/" + name
		return storagesource.Sidecar{MediaFileID: file.ID, PersistedRef: storagesource.PersistedRef{LocationID: location, EntryID: entry, Revision: "rev-1", LogicalPath: entry},
			Name: name, Format: format, Language: language, Forced: forced, HearingImpaired: sdh}
	}
	store := &nativeSidecarStore{rows: []storagesource.Sidecar{
		sidecar("Show - S01E01.en.sdh.srt", "srt", "en", false, true),
		sidecar("Show - S01E01.en.srt", "srt", "en", false, false),
		sidecar("Show - S01E01.nl.forced.ass", "ass", "nl", true, false),
		// Another episode's sidecar never joins this file.
		{MediaFileID: file.ID + 1, PersistedRef: storagesource.PersistedRef{LocationID: location, EntryID: "other.srt", Revision: "rev-1"}, Name: "other.srt", Format: "srt"},
	}}
	provider := &nativeSidecarProvider{identity: *identity, objects: map[string]string{
		"Show/Season 01/Show - S01E01.en.sdh.srt@rev-1":    nativeSidecarSDH,
		"Show/Season 01/Show - S01E01.en.srt@rev-1":        nativeSidecarPlain,
		"Show/Season 01/Show - S01E01.nl.forced.ass@rev-1": nativeSidecarForced,
	}}
	service.Sidecars = store
	service.OpenSidecar = provider.open
	handler := NewStreamHandler(service.Sessions, service.Files)
	handler.NativeVideo = service
	return handler, file, session, store, provider
}

func nativeSidecarGet(t *testing.T, handler *StreamHandler, session *playback.Session, method, track, query string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/api/v2/stream/" + session.ID + "/subtitles/" + track
	if query != "" {
		target += "?" + query
	}
	response := httptest.NewRecorder()
	handler.HandleSubtitle(response, playbackTestRequest(method, target, nil, map[string]string{"session_id": session.ID, "track": track}))
	return response
}

// The playback plan and the subtitle route read a native episode's sidecars
// from the series scan's rows, in the place and shape Silo gives a local
// external subtitle; catalog JSON never names a local path for them.
func TestNativeSidecarsListAsExternalSubtitles(t *testing.T) {
	handler, file, session, _, _ := nativeSidecarFixture(t)
	file.ExternalSubtitles = []models.ExternalSubtitle{{Path: "/private/leak.srt", Format: "srt", Language: "en"}}
	playbackHandler := NewPlaybackHandler(handler.NativeVideo.Sessions, handler.NativeVideo.Files)
	playbackHandler.NativeVideo = handler.NativeVideo
	for name, files := range map[string]FilePathResolver{"plan": playbackHandler.playbackMetadataFiles(), "route": handler.subtitleMetadataFiles()} {
		listed, err := files.GetByID(t.Context(), file.ID)
		if err != nil {
			t.Fatal(name, err)
		}
		want := []struct {
			name, format, language string
			forced, sdh            bool
		}{{"Show - S01E01.en.sdh.srt", "srt", "en", false, true}, {"Show - S01E01.en.srt", "srt", "en", false, false}, {"Show - S01E01.nl.forced.ass", "ass", "nl", true, false}}
		if len(listed.ExternalSubtitles) != len(want) {
			t.Fatalf("%s listed %+v", name, listed.ExternalSubtitles)
		}
		local := &models.MediaFile{ID: file.ID}
		for i, w := range want {
			got := listed.ExternalSubtitles[i]
			if got.FileName() != w.name || got.Format != w.format || got.Language != w.language || got.Forced != w.forced || got.HearingImpaired != w.sdh || got.Default || !got.TitleIsFileName() {
				t.Fatalf("%s sidecar %d: %+v", name, i, got)
			}
			if strings.HasPrefix(got.Path, "/") || strings.Contains(got.Path, "leak") || strings.Contains(got.Path, "Season 01") {
				t.Fatalf("%s sidecar exposes a path: %q", name, got.Path)
			}
			local.ExternalSubtitles = append(local.ExternalSubtitles, models.ExternalSubtitle{Path: "/media/Show/Season 01/" + w.name, Format: w.format, Language: w.language, Forced: w.forced, HearingImpaired: w.sdh})
		}
		// The same inventory rows Silo publishes for local sidecars.
		native, silo := playback.SubtitleInventoryV3(session.ID, listed, nil), playback.SubtitleInventoryV3(session.ID, local, nil)
		playback.HideSidecarFileNamesV3(native, listed)
		playback.HideSidecarFileNamesV3(silo, local)
		for i := range silo {
			n, s := native[i], silo[i]
			if n.Source != playback.SubtitleSourceExternalV3 || n.Source != s.Source || n.Codec != s.Codec || n.Language != s.Language || n.Label != s.Label || n.Forced != s.Forced ||
				n.HearingImpaired != s.HearingImpaired || n.Delivery != s.Delivery || n.Delivery != playback.SubtitleDeliverySidecarV3 || n.CombinedIndex != i || !strings.Contains(n.URL, playback.ExternalSubtitleKeyParamV3+"=") {
				t.Fatalf("%s inventory %d native %+v silo %+v", name, i, n, s)
			}
			if strings.Contains(n.Label, "S01E01") {
				t.Fatalf("%s hidden label shows the file name: %q", name, n.Label)
			}
		}
	}
	// Without a sidecar store nothing is listed in their place.
	handler.NativeVideo.Sidecars = nil
	if listed, err := handler.subtitleMetadataFiles().GetByID(t.Context(), file.ID); err != nil || len(listed.ExternalSubtitles) != 1 {
		t.Fatalf("store-less listing changed: %v %+v", err, listed)
	}
}

// A sidecar is served from the storage plugin: SRT as WebVTT by default and as
// the original bytes on request, ASS as itself, each at the URL the plan mints.
func TestNativeSidecarsServeConvertedAndOriginal(t *testing.T) {
	handler, file, session, _, provider := nativeSidecarFixture(t)
	vtt := nativeSidecarGet(t, handler, session, http.MethodGet, "0.vtt", "")
	if vtt.Code != http.StatusOK || !strings.HasPrefix(vtt.Body.String(), "WEBVTT") || !strings.Contains(vtt.Body.String(), "00:00:01.000 --> 00:00:02.500") || !strings.Contains(vtt.Body.String(), "Hearing-impaired line") {
		t.Fatalf("WebVTT: %d %q", vtt.Code, vtt.Body.String())
	}
	if !strings.HasPrefix(vtt.Header().Get("Content-Type"), "text/vtt") || vtt.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("WebVTT headers %v", vtt.Header())
	}
	original := nativeSidecarGet(t, handler, session, http.MethodGet, "1.srt", playback.SubtitleOriginalParamV3+"=1")
	if original.Code != http.StatusOK || original.Body.String() != nativeSidecarPlain {
		t.Fatalf("original SRT: %d %q", original.Code, original.Body.String())
	}
	ass := nativeSidecarGet(t, handler, session, http.MethodGet, "2.ass", "")
	if ass.Code != http.StatusOK || !strings.Contains(ass.Body.String(), "Forced Dutch sign") || !strings.HasPrefix(ass.Header().Get("Content-Type"), "text/x-ssa") {
		t.Fatalf("ASS: %d %q %v", ass.Code, ass.Body.String(), ass.Header())
	}
	if mismatch := nativeSidecarGet(t, handler, session, http.MethodGet, "2.srt", ""); mismatch.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("ASS as SRT: %d", mismatch.Code)
	}
	// The plan's URL pins its sidecar by key.
	listed, err := handler.subtitleMetadataFiles().GetByID(t.Context(), file.ID)
	if err != nil {
		t.Fatal(err)
	}
	minted, err := url.Parse(playback.SubtitleInventoryV3(session.ID, listed, nil)[1].URL)
	if err != nil {
		t.Fatal(err)
	}
	track := minted.Path[strings.LastIndex(minted.Path, "/")+1:]
	pinned := nativeSidecarGet(t, handler, session, http.MethodGet, track, minted.RawQuery)
	if pinned.Code != http.StatusOK || !strings.Contains(pinned.Body.String(), "Plain English line") {
		t.Fatalf("plan URL: %s %d %q", minted, pinned.Code, pinned.Body.String())
	}
	before := provider.count()
	if head := nativeSidecarGet(t, handler, session, http.MethodHead, "0.vtt", ""); head.Code != http.StatusOK || head.Body.Len() != 0 || provider.count() != before {
		t.Fatalf("HEAD: %d body %d opens %d->%d", head.Code, head.Body.Len(), before, provider.count())
	}
	for _, binding := range provider.bindings {
		if binding.SessionID != session.ID || binding.UserID != session.UserID || binding.ProfileID != session.ProfileID || binding.Identity.FileID != file.ID {
			t.Fatalf("sidecar opened outside the session binding: %+v", binding)
		}
	}
}

type nativeSidecarRevokedFile struct{ NativeVideoFile }

func (nativeSidecarRevokedFile) Reauthorize(context.Context) error {
	return storagesource.ErrReferenceConflict
}

// A sidecar is read only within the session's native authority: its owner and
// profile, a live binding at the bound source revision, no deny marker, and a
// sidecar the provider still holds. A refusal exposes no subtitle text.
func TestNativeSidecarsRequireSessionAuthority(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, *StreamHandler, *playback.Session, *nativeSidecarStore, *nativeSidecarProvider) *http.Request
		opens  bool
	}{
		{name: "other profile", mutate: func(_ *testing.T, _ *StreamHandler, s *playback.Session, _ *nativeSidecarStore, _ *nativeSidecarProvider) *http.Request {
			r := playbackTestRequest(http.MethodGet, "/api/v2/stream/"+s.ID+"/subtitles/0.vtt", nil, map[string]string{"session_id": s.ID, "track": "0.vtt"})
			return r.WithContext(apimw.SetProfileID(r.Context(), "profile-2"))
		}},
		{name: "deny marker", mutate: func(t *testing.T, h *StreamHandler, s *playback.Session, _ *nativeSidecarStore, _ *nativeSidecarProvider) *http.Request {
			client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
			client.AddHook(&nativeAcceptanceDenyIO{keys: make(map[string]bool)})
			t.Cleanup(func() { _ = client.Close() })
			h.NativeVideo.Deny = playback.NewStreamDeny(client)
			h.NativeVideo.Deny.Deny(t.Context(), s.ID)
			return nil
		}},
		{name: "unbound session", mutate: func(_ *testing.T, h *StreamHandler, _ *playback.Session, _ *nativeSidecarStore, _ *nativeSidecarProvider) *http.Request {
			h.NativeVideo.Bindings = videoBindingFixture{}
			return nil
		}},
		{name: "video revision changed", mutate: func(_ *testing.T, h *StreamHandler, _ *playback.Session, _ *nativeSidecarStore, _ *nativeSidecarProvider) *http.Request {
			open := h.NativeVideo.Open
			h.NativeVideo.Open = func(ctx context.Context, f *models.MediaFile) (NativeVideoFile, error) {
				opened, err := open(ctx, f)
				if err != nil {
					return nil, err
				}
				fixture := opened.(*videoFileFixture)
				fixture.id.Reference.Revision, fixture.info.Revision = "version-2", "version-2"
				return fixture, nil
			}
			return nil
		}},
		{name: "sidecar no longer listed", mutate: func(t *testing.T, h *StreamHandler, s *playback.Session, store *nativeSidecarStore, _ *nativeSidecarProvider) *http.Request {
			listed, err := h.subtitleMetadataFiles().GetByID(t.Context(), s.MediaFileID)
			if err != nil {
				t.Fatal(err)
			}
			key := playback.ExternalSubtitlePathKeyV3(listed.ExternalSubtitles[0].Path)
			store.mu.Lock()
			store.rows = store.rows[1:]
			store.mu.Unlock()
			return playbackTestRequest(http.MethodGet, "/api/v2/stream/"+s.ID+"/subtitles/0.vtt?"+playback.ExternalSubtitleKeyParamV3+"="+key, nil, map[string]string{"session_id": s.ID, "track": "0.vtt"})
		}},
		{name: "provider refuses", mutate: func(_ *testing.T, _ *StreamHandler, _ *playback.Session, _ *nativeSidecarStore, p *nativeSidecarProvider) *http.Request {
			p.err = storagesource.ErrReferenceConflict
			return nil
		}, opens: true},
		{name: "provider holds another revision", mutate: func(_ *testing.T, _ *StreamHandler, _ *playback.Session, store *nativeSidecarStore, _ *nativeSidecarProvider) *http.Request {
			store.setRevision("Show/Season 01/Show - S01E01.en.sdh.srt", "rev-unlisted")
			return nil
		}, opens: true},
		{name: "opened another source", mutate: func(_ *testing.T, _ *StreamHandler, _ *playback.Session, _ *nativeSidecarStore, p *nativeSidecarProvider) *http.Request {
			p.identity.ProviderSourceID = "other-source"
			return nil
		}, opens: true},
		{name: "revoked during read", mutate: func(_ *testing.T, _ *StreamHandler, _ *playback.Session, _ *nativeSidecarStore, p *nativeSidecarProvider) *http.Request {
			p.wrap = func(f NativeVideoFile) NativeVideoFile { return nativeSidecarRevokedFile{f} }
			return nil
		}, opens: true},
		{name: "no sidecar reader", mutate: func(_ *testing.T, h *StreamHandler, _ *playback.Session, _ *nativeSidecarStore, _ *nativeSidecarProvider) *http.Request {
			h.NativeVideo.OpenSidecar = nil
			return nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, _, session, store, provider := nativeSidecarFixture(t)
			request := tc.mutate(t, handler, session, store, provider)
			if request == nil {
				request = playbackTestRequest(http.MethodGet, "/api/v2/stream/"+session.ID+"/subtitles/0.vtt", nil, map[string]string{"session_id": session.ID, "track": "0.vtt"})
			}
			response := httptest.NewRecorder()
			handler.HandleSubtitle(response, request)
			if response.Code == http.StatusOK || strings.Contains(response.Body.String(), "WEBVTT") || strings.Contains(response.Body.String(), "line") {
				t.Fatalf("refused sidecar served: %d %q", response.Code, response.Body.String())
			}
			if opened := provider.count() > 0; opened != tc.opens {
				t.Fatalf("sidecar opens = %d", provider.count())
			}
		})
	}
}

// Sidecar bytes are cached by their source revision: a repeat read opens
// nothing, a new revision is read fresh and never answered with the old text,
// and the old revision's bytes stay addressable only by that revision.
func TestNativeSidecarsCacheBySourceRevision(t *testing.T) {
	handler, _, session, store, provider := nativeSidecarFixture(t)
	get := func() string {
		t.Helper()
		response := nativeSidecarGet(t, handler, session, http.MethodGet, "1.vtt", "")
		if response.Code != http.StatusOK {
			t.Fatalf("read: %d %s", response.Code, response.Body.String())
		}
		return response.Body.String()
	}
	if body := get(); !strings.Contains(body, "Plain English line") || provider.count() != 1 {
		t.Fatalf("first read %q opens %d", body, provider.count())
	}
	if body := get(); !strings.Contains(body, "Plain English line") || provider.count() != 1 {
		t.Fatalf("cached read %q opens %d", body, provider.count())
	}
	entry := "Show/Season 01/Show - S01E01.en.srt"
	provider.mu.Lock()
	provider.objects[entry+"@rev-2"] = "1\n00:00:01,000 --> 00:00:02,500\nReplaced English line\n"
	provider.mu.Unlock()
	store.setRevision(entry, "rev-2")
	if body := get(); strings.Contains(body, "Plain English line") || !strings.Contains(body, "Replaced English line") || provider.count() != 2 {
		t.Fatalf("changed sidecar %q opens %d", body, provider.count())
	}
	if last := provider.opens[len(provider.opens)-1]; last.EntryID != entry || last.Revision != "rev-2" {
		t.Fatalf("opened %+v", last)
	}
	store.setRevision(entry, "rev-1")
	if body := get(); !strings.Contains(body, "Plain English line") || provider.count() != 2 {
		t.Fatalf("revision cache %q opens %d", body, provider.count())
	}
	// A cached read still answers to the session's authority.
	handler.NativeVideo.Bindings = videoBindingFixture{}
	if response := nativeSidecarGet(t, handler, session, http.MethodGet, "1.vtt", ""); response.Code == http.StatusOK || strings.Contains(response.Body.String(), "line") {
		t.Fatalf("cached sidecar served without a binding: %d %q", response.Code, response.Body.String())
	}
}

func TestNativeSidecarCacheBounds(t *testing.T) {
	var cache nativeSidecarCache
	big := bytes.Repeat([]byte{'x'}, nativeSidecarMaxBytes)
	keys := nativeSidecarCacheBytes/nativeSidecarMaxBytes + 1
	for i := range keys {
		cache.put(strconv.Itoa(i), big)
	}
	if _, ok := cache.get("0"); ok || cache.bytes > nativeSidecarCacheBytes {
		t.Fatalf("cache kept %d bytes, over its budget", cache.bytes)
	}
	if data, ok := cache.get(strconv.Itoa(keys - 1)); !ok || len(data) != len(big) {
		t.Fatal("newest entry evicted")
	}
	data, _ := cache.get(strconv.Itoa(keys - 1))
	data[0] = 'y'
	if again, _ := cache.get(strconv.Itoa(keys - 1)); again[0] != 'x' {
		t.Fatal("a caller changed cached bytes")
	}
	cache.put("huge", make([]byte, nativeSidecarMaxBytes+1))
	if _, ok := cache.get("huge"); ok {
		t.Fatal("oversized sidecar cached")
	}
}
