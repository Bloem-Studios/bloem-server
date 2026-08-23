package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/music"
	"github.com/go-chi/chi/v5"
)

type musicRepoStub struct {
	status music.Status
	page   music.ArtistPage
	artist music.ArtistDetail
	album  music.AlbumDetail
}

func (s musicRepoStub) Status(context.Context, catalog.AccessFilter) (music.Status, error) {
	return s.status, nil
}

func (s musicRepoStub) ListArtists(context.Context, int, string, int, catalog.AccessFilter) (music.ArtistPage, error) {
	return s.page, nil
}

func (s musicRepoStub) Artist(context.Context, int, string, catalog.AccessFilter) (music.ArtistDetail, error) {
	return s.artist, nil
}

func (s musicRepoStub) Album(context.Context, int, string, catalog.AccessFilter) (music.AlbumDetail, error) {
	return s.album, nil
}

func TestMusicHandlerPublishesCapabilityAndClientContract(t *testing.T) {
	artist := music.Artist{ID: "artist-1", Name: "Nocturne"}
	album := music.Album{ID: "album-1", Title: "Veil", ArtistID: artist.ID, ArtistName: artist.Name, Year: 2026}
	track := music.Track{
		ID: "track-1", Title: "Glass", AlbumID: album.ID, ArtistID: artist.ID,
		MediaFileID: 42, DurationMS: 181000, DiscNumber: 1, TrackNumber: 1,
	}
	h := NewMusicHandler(musicRepoStub{
		status: music.Status{Available: true, LibraryIDs: []int{7}},
		page:   music.ArtistPage{Items: []music.Artist{artist}, NextCursor: "artist-2"},
		artist: music.ArtistDetail{Artist: artist, Albums: []music.Album{album}},
		album:  music.AlbumDetail{Album: album, Tracks: []music.Track{track}},
	}, &ItemsHandler{})

	router := chi.NewRouter()
	router.Get("/api/v1/music/status", h.HandleStatus)
	router.Get("/api/v1/music/artists", h.HandleArtists)
	router.Get("/api/v1/music/artists/{id}", h.HandleArtist)
	router.Get("/api/v1/music/albums/{id}", h.HandleAlbum)

	for _, tc := range []struct {
		path string
		want any
	}{
		{"/api/v1/music/status", music.Status{Available: true, LibraryIDs: []int{7}}},
		{"/api/v1/music/artists?library_id=7", music.ArtistPage{Items: []music.Artist{artist}, NextCursor: "artist-2"}},
		{"/api/v1/music/artists/artist-1?library_id=7", music.ArtistDetail{Artist: artist, Albums: []music.Album{album}}},
		{"/api/v1/music/albums/album-1?library_id=7", music.AlbumDetail{Album: album, Tracks: []music.Track{track}}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			encoded, err := json.Marshal(tc.want)
			if err != nil {
				t.Fatal(err)
			}
			if got := rec.Body.String(); got != string(encoded)+"\n" {
				t.Fatalf("body = %s, want %s", got, encoded)
			}
		})
	}
}

func TestMusicHandlerRejectsMissingOrInvalidLibraryID(t *testing.T) {
	h := NewMusicHandler(musicRepoStub{}, &ItemsHandler{})
	for _, path := range []string{"/api/v1/music/artists", "/api/v1/music/artists?library_id=0", "/api/v1/music/artists?library_id=nope"} {
		rec := httptest.NewRecorder()
		h.HandleArtists(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", path, rec.Code)
		}
	}
}
