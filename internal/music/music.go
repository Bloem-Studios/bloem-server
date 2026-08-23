// Package music owns Vondel's additive first-party music catalogue contract.
package music

import (
	"context"
	"errors"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

var ErrNotFound = errors.New("music resource not found")

type Artist struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ArtworkURL  string `json:"artwork_url,omitempty"`
	ArtworkPath string `json:"-"`
}

type Album struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	ArtistID    string `json:"artist_id"`
	ArtistName  string `json:"artist_name"`
	ArtworkURL  string `json:"artwork_url,omitempty"`
	ArtworkPath string `json:"-"`
	Year        int    `json:"year,omitempty"`
}

type Track struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	AlbumID     string `json:"album_id"`
	ArtistID    string `json:"artist_id"`
	MediaFileID int    `json:"media_file_id"`
	DurationMS  int64  `json:"duration_ms"`
	DiscNumber  int    `json:"disc_number"`
	TrackNumber int    `json:"track_number"`
	ArtworkURL  string `json:"artwork_url,omitempty"`
	ArtworkPath string `json:"-"`
}

type ArtistPage struct {
	Items      []Artist `json:"items"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

type ArtistDetail struct {
	Artist Artist  `json:"artist"`
	Albums []Album `json:"albums"`
}

type AlbumDetail struct {
	Album  Album   `json:"album"`
	Tracks []Track `json:"tracks"`
}

type Status struct {
	Available  bool  `json:"available"`
	LibraryIDs []int `json:"library_ids"`
}

type Repository interface {
	Status(context.Context, catalog.AccessFilter) (Status, error)
	ListArtists(context.Context, int, string, int, catalog.AccessFilter) (ArtistPage, error)
	Artist(context.Context, int, string, catalog.AccessFilter) (ArtistDetail, error)
	Album(context.Context, int, string, catalog.AccessFilter) (AlbumDetail, error)
}
