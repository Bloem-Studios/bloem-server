package jellycompat

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
)

func TestHandleDownload_BrowseOnlyPolicyDeniesDirectPlayTransport(t *testing.T) {
	handler := &PlaybackHandler{
		accessFilter: func(context.Context, int, string) catalog.AccessFilter {
			return catalog.AccessFilter{PlaybackDenied: true}
		},
	}
	req := httptest.NewRequest("GET", "/Items/ignored/Download", nil)
	ctx := context.WithValue(req.Context(), compatSessionKey, &Session{StreamAppUserID: 1, ProfileID: "browse-only"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.HandleBloemDownload(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestBloemDownloadRouteDeniesBeforeResolution(t *testing.T) {
	for _, policy := range []struct {
		name   string
		filter catalog.AccessFilter
	}{
		{"browse only", catalog.AccessFilter{PlaybackDenied: true}},
		{"download denied", catalog.AccessFilter{DownloadDenied: true}},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(policy.name+"/"+method, func(t *testing.T) {
				cfg, err := config.LoadFromDB(map[string]string{})
				if err != nil {
					t.Fatal(err)
				}
				store := NewSessionStore(time.Hour, nil)
				if err := store.Put(Session{Token: compatSocketToken, StreamAppUserID: 1, ProfileID: "denied-profile"}); err != nil {
					t.Fatal(err)
				}
				codec := NewResourceIDCodec()
				owner := &bloemDownloadOwnerSpy{}
				codec.SetMediaSourceOwnerLookup(owner)
				content := &bloemDownloadContentSpy{}
				serverDownloads := &bloemDownloadServerSpy{}
				server := NewServerWithDependencies(Dependencies{
					Config: cfg, SessionStore: store, IDCodec: codec, ContentService: content, Downloads: serverDownloads,
					AccessFilterFn: func(_ context.Context, user int, profile string) catalog.AccessFilter {
						if user != 1 || profile != "denied-profile" {
							t.Errorf("policy subject = %d/%q", user, profile)
						}
						return policy.filter
					},
				})
				for _, id := range []string{"malformed", codec.EncodeStringID(EncodedIDItem, "movie-1"), codec.EncodeIntID(EncodedIDMediaSource, 42)} {
					rr := httptest.NewRecorder()
					server.Handler().ServeHTTP(rr, httptest.NewRequest(method, "/Items/"+id+"/Download?api_key="+compatSocketToken, nil))
					if rr.Code != http.StatusForbidden {
						t.Errorf("%s status = %d, want 403; body=%s", id, rr.Code, rr.Body.String())
					}
					if rr.Header().Get("Content-Disposition") != "" {
						t.Error("denied request advertised an attachment")
					}
				}
				if owner.calls != 0 || content.calls != 0 || serverDownloads.calls != 0 {
					t.Fatalf("denied request performed IO: owner=%d content=%d download=%d", owner.calls, content.calls, serverDownloads.calls)
				}
			})
		}
	}
}

func TestBloemDownloadRouteRequiresAuthenticationAndService(t *testing.T) {
	cfg, err := config.LoadFromDB(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	store := NewSessionStore(time.Hour, nil)
	if err := store.Put(Session{Token: compatSocketToken, StreamAppUserID: 1, ProfileID: "profile-1"}); err != nil {
		t.Fatal(err)
	}
	codec := NewResourceIDCodec()
	server := NewServerWithDependencies(Dependencies{
		Config: cfg, SessionStore: store, IDCodec: codec,
		ContentService: &stubContentService{detail: &upstreamItemDetail{ContentID: "movie-1", Type: "movie", Versions: []catalog.FileVersion{{FileID: 42}}}},
	})
	path := "/Items/" + codec.EncodeStringID(EncodedIDItem, "movie-1") + "/Download"
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, tc := range []struct {
			suffix string
			status int
		}{{"", http.StatusUnauthorized}, {"?api_key=" + compatSocketToken, http.StatusServiceUnavailable}} {
			rr := httptest.NewRecorder()
			server.Handler().ServeHTTP(rr, httptest.NewRequest(method, path+tc.suffix, nil))
			if rr.Code != tc.status {
				t.Errorf("%s status=%d, want %d; body=%s", method, rr.Code, tc.status, rr.Body.String())
			}
		}
	}
}

type bloemDownloadOwnerSpy struct{ calls int }

func (s *bloemDownloadOwnerSpy) PlayableContentID(context.Context, int) (string, error) {
	s.calls++
	return "movie-1", nil
}

type bloemDownloadContentSpy struct {
	stubContentService
	calls int
}

func (s *bloemDownloadContentSpy) GetItemDetail(context.Context, *Session, string, *int) (*upstreamItemDetail, error) {
	s.calls++
	return nil, nil
}

type bloemDownloadServerSpy struct{ calls int }

func (s *bloemDownloadServerSpy) ServeDirect(context.Context, http.ResponseWriter, *http.Request, int, int, string, catalog.AccessFilter) error {
	s.calls++
	return nil
}

func TestHandleDownload_DownloadPolicyDeniesOriginalFileTransport(t *testing.T) {
	handler := &PlaybackHandler{accessFilter: func(context.Context, int, string) catalog.AccessFilter {
		return catalog.AccessFilter{DownloadDenied: true}
	}}
	req := httptest.NewRequest("GET", "/Items/ignored/Download", nil)
	req = req.WithContext(context.WithValue(req.Context(), compatSessionKey, &Session{StreamAppUserID: 1, ProfileID: "no-download"}))
	rec := httptest.NewRecorder()
	handler.HandleBloemDownload(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestItemDetail_BrowseOnlyPolicyDoesNotAdvertiseCanDownload(t *testing.T) {
	h := &ItemsHandler{
		mapper: newMapper(NewResourceIDCodec(), nil),
		accessFilter: func(context.Context, int, string) catalog.AccessFilter {
			return catalog.AccessFilter{PlaybackDenied: true}
		},
	}
	detail := upstreamItemDetail{
		ContentID: "movie-1",
		Type:      "movie",
		Versions:  []catalog.FileVersion{{FileID: 42}},
	}
	dto := h.itemFromDetailForSession(context.Background(), &Session{StreamAppUserID: 1, ProfileID: "browse-only"}, detail, false, nil, nil)
	if dto.CanDownload {
		t.Fatal("CanDownload = true, want browse-only policy not to advertise direct-play transport")
	}
}

func TestItemDetail_PlaybackAllowedDownloadDeniedDoesNotAdvertiseCanDownload(t *testing.T) {
	h := &ItemsHandler{mapper: newMapper(NewResourceIDCodec(), nil), accessFilter: func(context.Context, int, string) catalog.AccessFilter {
		return catalog.AccessFilter{DownloadDenied: true}
	}}
	detail := upstreamItemDetail{ContentID: "movie-1", Type: "movie", Versions: []catalog.FileVersion{{FileID: 42}}}
	dto := h.itemFromDetailForSession(context.Background(), &Session{StreamAppUserID: 1, ProfileID: "no-download"}, detail, false, nil, nil)
	if dto.CanDownload {
		t.Fatal("CanDownload = true, want download-disabled policy not to advertise Jelly download/direct-play transport")
	}
}
