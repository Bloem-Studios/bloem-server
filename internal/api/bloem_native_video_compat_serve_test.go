package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

type compatFilesFixture struct{ file *models.MediaFile }

func (f compatFilesFixture) GetByID(_ context.Context, id int) (*models.MediaFile, error) {
	if f.file == nil || id != f.file.ID {
		return nil, storagesource.ErrReferenceConflict
	}
	return f.file, nil
}

type compatServeFixture struct {
	delegation *nativeVideoDelegationFixture
	sessions   *compatSessionsFixture
	host       *NativeVideoCompat
	store      *nativeVideoBoundBindingsFixture
	opens      int
}

// newCompatServeFixture composes the Jellyfin native media host over native
// video delivery, with a synthetic ten-byte source.
func newCompatServeFixture(t *testing.T) *compatServeFixture {
	t.Helper()
	d := newNativeVideoDelegationFixture(t)
	f := d.base
	sessions := compatPolicy(f)
	host := f.policy.compat
	store := &nativeVideoBoundBindingsFixture{}
	f.policy.loadBinding = store.Get
	identity := nativestorage.PlaybackIdentity{FileID: 11, MediaFolderID: 9, ContentID: "movie", FilePath: f.file.FilePath}
	identity.Reference.EntryID, identity.Reference.Revision = "movie", "v1"
	info := mediasource.Info{Name: "movie.mp4", Size: 10, Revision: "v1"}
	fixture := &compatServeFixture{delegation: d, sessions: sessions, host: host, store: store}
	service := &handlers.NativeVideoService{Files: compatFilesFixture{file: f.file}, Bindings: store}
	configureNativeVideoBoundAuthority(service, f.policy)
	service.Delegation = func(ctx context.Context) (handlers.NativeVideoAuthority, error) {
		return nativeVideoDelegation(ctx, f.policy)
	}
	service.Open = func(ctx context.Context, file *models.MediaFile) (handlers.NativeVideoFile, error) {
		fixture.opens++
		authority, err := nativeVideoAuthority(ctx, file.ID, f.policy)
		if err != nil {
			return nil, err
		}
		return &nativeVideoBoundReaderFixture{Reader: bytes.NewReader([]byte("0123456789")), ctx: ctx, binding: handlers.NativeVideoBinding{Identity: identity, Info: info}, authority: authority, file: file, source: f.source}, nil
	}
	host.install(service, f.policy.getSession, compatFilesFixture{file: f.file})
	return fixture
}

func (f *compatServeFixture) request(method, rangeHeader string) *http.Request {
	ctx := playback.WithContextMediaHost(context.Background(), f.host)
	playback.NoteContextStream(ctx, compatTestStream())
	r := httptest.NewRequest(method, "/Videos/item/stream?static=true", nil).WithContext(ctx)
	if rangeHeader != "" {
		r.Header.Set("Range", rangeHeader)
	}
	return r
}

func (f *compatServeFixture) serve(t *testing.T, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	// The Jellyfin listener reaches the host through playback's direct play.
	_ = playback.ServeDirectPlay(w, r, f.delegation.base.file.FilePath)
	return w
}

func TestNativeVideoCompatDirectPlayServesRangesAndHead(t *testing.T) {
	f := newCompatServeFixture(t)
	w := f.serve(t, f.request(http.MethodGet, "bytes=2-5"))
	body, _ := io.ReadAll(w.Body)
	if w.Code != http.StatusPartialContent || string(body) != "2345" || w.Header().Get("Content-Range") != "bytes 2-5/10" || w.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("range: %d %q %v", w.Code, body, w.Header())
	}
	if w.Header().Get("Content-Type") != "video/mp4" || w.Header().Get("ETag") == "" {
		t.Fatalf("headers: %v", w.Header())
	}
	etag := w.Header().Get("ETag")
	w = f.serve(t, f.request(http.MethodHead, ""))
	if w.Code != http.StatusOK || w.Header().Get("Content-Length") != "10" || w.Body.Len() != 0 || w.Header().Get("ETag") != etag {
		t.Fatalf("head: %d %v", w.Code, w.Header())
	}
	w = f.serve(t, f.request(http.MethodGet, "bytes=20-30"))
	if w.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("unsatisfiable range: %d", w.Code)
	}
	w = f.serve(t, f.request(http.MethodGet, ""))
	if body, _ := io.ReadAll(w.Body); w.Code != http.StatusOK || string(body) != "0123456789" {
		t.Fatalf("whole file: %d %q", w.Code, body)
	}
	if f.store.binding.Authority.TokenType != nativeVideoCompatTokenType || f.store.binding.SessionID != "play-session" {
		t.Fatalf("binding = %+v", f.store.binding)
	}
}

func TestNativeVideoCompatDirectPlayRefusals(t *testing.T) {
	for _, kind := range []string{"signed-out", "profile-removed", "playback-session-other-file", "stopped", "no-stream", "not-ready"} {
		t.Run(kind, func(t *testing.T) {
			f := newCompatServeFixture(t)
			if w := f.serve(t, f.request(http.MethodGet, "bytes=0-1")); w.Code != http.StatusPartialContent {
				t.Fatalf("first request: %d", w.Code)
			}
			r := f.request(http.MethodGet, "bytes=0-1")
			want := http.StatusForbidden
			switch kind {
			case "signed-out":
				f.sessions.err = errors.New("signed out")
				want = 0
			case "profile-removed":
				f.sessions.liveErr = errors.New("compat rows deleted")
			case "playback-session-other-file":
				f.delegation.base.session.MediaFileID = 12
			case "stopped":
				// FinishSession removes the session, then runs the hook.
				stopped := *f.delegation.base.session
				f.delegation.base.session = nil
				f.host.revokeStopped(t.Context(), &stopped)
				if f.store.binding.SessionID != "" {
					t.Fatal("stop left the native binding")
				}
				want = 0
			case "no-stream":
				r = httptest.NewRequest(http.MethodGet, "/Videos/item/stream", nil).WithContext(playback.WithContextMediaHost(context.Background(), f.host))
			case "not-ready":
				r = httptest.NewRequest(http.MethodGet, "/Videos/item/stream", nil).WithContext(playback.WithContextMediaHost(context.Background(), NewNativeVideoCompat()))
				want = http.StatusServiceUnavailable
			}
			w := f.serve(t, r)
			if w.Code < 400 || want != 0 && w.Code != want || w.Code == http.StatusUnauthorized {
				t.Fatalf("%s answered %d, want %d (never 401)", kind, w.Code, want)
			}
			if bytes.Contains(w.Body.Bytes(), []byte("01")) {
				t.Fatal("refusal delivered media bytes")
			}
		})
	}
}
