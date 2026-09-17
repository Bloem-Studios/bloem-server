package jellycompat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/livetv"
	"github.com/go-chi/chi/v5"
)

type sharedCompatLiveStore struct {
	*livetvTestStore
	shared map[string]livetv.CompatStream
	hashes map[string]string
}

func (s *sharedCompatLiveStore) PutCompatStream(_ context.Context, stream livetv.CompatStream, hash string) error {
	s.shared[stream.ID] = stream
	s.hashes[stream.ID] = hash
	return nil
}
func (s *sharedCompatLiveStore) GetCompatStream(ctx context.Context, id, hash string) (livetv.CompatStream, error) {
	stream, ok := s.shared[id]
	if !ok || s.hashes[id] != hash {
		return livetv.CompatStream{}, livetv.ErrNotFound
	}
	native, err := s.GetSession(ctx, stream.NativeSession)
	if err != nil || native.Status != "active" {
		return livetv.CompatStream{}, livetv.ErrNotFound
	}
	stream.ChannelID = native.ChannelID
	return stream, nil
}

func TestLiveTVCompatOpenFetchCloseAcrossReplicas(t *testing.T) {
	first, base, _ := rawStreamFixture(t)
	shared := &sharedCompatLiveStore{livetvTestStore: base, shared: map[string]livetv.CompatStream{}, hashes: map[string]string{}}
	first.service = livetv.NewServiceWithStore(shared)
	second := newTestLiveTVHandler(base)
	second.service = livetv.NewServiceWithStore(shared)
	caller := &Session{Token: "same-login-token", StreamAppUserID: 7, ProfileID: "p"}
	source, err := first.openChannelStream(context.Background(), caller, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.streams) != 0 || len(second.streams) != 0 {
		t.Fatal("shared stream leaked into local cache")
	}
	// Replica B resolves credentials/source without opening another tuner.
	stream, err := second.loadOpenLiveStream(context.Background(), source.LiveStreamID, caller)
	if err != nil || stream.SourceURL != "http://192.168.1.2/auto/v1" {
		t.Fatalf("remote lookup %+v %v", stream, err)
	}
	foreign := *caller
	foreign.Token = "another-login"
	if _, err := second.loadOpenLiveStream(context.Background(), source.LiveStreamID, &foreign); !errors.Is(err, livetv.ErrNotFound) {
		t.Fatalf("foreign opener: %v", err)
	}
	// HEAD exercises the real fetch handler without an indefinite tuner stream.
	request := httptest.NewRequest("HEAD", "/LiveTv/LiveStreamFiles/"+source.LiveStreamID+"/stream.ts", nil)
	route := chi.NewRouteContext()
	route.URLParams.Add("id", source.LiveStreamID)
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, route)
	ctx = context.WithValue(ctx, compatSessionKey, caller)
	rec := httptest.NewRecorder()
	second.HandleLiveStreamFile(rec, request.WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("remote fetch %d %s", rec.Code, rec.Body.String())
	}
	request = httptest.NewRequest("POST", "/LiveStreams/Close?LiveStreamId="+source.LiveStreamID, nil)
	ctx = context.WithValue(request.Context(), compatSessionKey, caller)
	rec = httptest.NewRecorder()
	second.HandleCloseLiveStream(rec, request.WithContext(ctx))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remote close %d %s", rec.Code, rec.Body.String())
	}
	if _, err := first.loadOpenLiveStream(context.Background(), source.LiveStreamID, caller); !errors.Is(err, livetv.ErrNotFound) {
		t.Fatalf("released stream survived on A: %v", err)
	}
}
