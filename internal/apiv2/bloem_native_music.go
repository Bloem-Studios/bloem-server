package apiv2

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/music"
)

// Music and person detail: the last of the native surface's client-facing
// operations.
//
// Music is Bloem-only. Silo has no artist, album or track model at all -- the
// music_catalog migration is Bloem's -- so /api/v2 will never describe these.
//
// Every body here reuses the types in internal/music, which are already
// exported and already feed the client DTO registry. Nothing is restated, so
// nothing can drift.

// BloemMusicStatusOutput reports whether this build has a music library and
// what it contains.
type BloemMusicStatusOutput struct {
	Body music.Status
}

// BloemMusicArtistsInput pages the artist list.
type BloemMusicArtistsInput struct {
	Limit  int `query:"limit" doc:"Maximum artists to return." required:"false"`
	Offset int `query:"offset" doc:"Artists to skip." required:"false"`
}

// BloemMusicArtistsOutput is one page of artists.
type BloemMusicArtistsOutput struct {
	Body music.ArtistPage
}

// BloemMusicArtistInput names the artist to fetch.
type BloemMusicArtistInput struct {
	ID string `path:"id" doc:"Artist identity."`
}

// BloemMusicArtistOutput is one artist with its albums.
type BloemMusicArtistOutput struct {
	Body music.ArtistDetail
}

// BloemMusicAlbumInput names the album to fetch.
type BloemMusicAlbumInput struct {
	ID string `path:"id" doc:"Album identity."`
}

// BloemMusicAlbumOutput is one album with its tracks.
type BloemMusicAlbumOutput struct {
	Body music.AlbumDetail
}

// BloemPersonInput names the person to fetch.
type BloemPersonInput struct {
	PersonID string `path:"person_id" doc:"Person identity."`
}

func registerBloemMusic(reg *Registry) {
	Register(reg, Operation{
		Operation: bloemOp("GET", "/music/status", "getBloemMusicStatus", "music",
			"Whether this build has a music library, and what it holds."),
		Class: ClassProfileScoped,
	}, func(context.Context, *struct{}) (*BloemMusicStatusOutput, error) {
		return &BloemMusicStatusOutput{}, nil
	})

	Register(reg, Operation{
		Operation: bloemOp("GET", "/music/artists", "listBloemMusicArtists", "music",
			"Artists this viewer may see, paged."),
		Class: ClassProfileScoped,
	}, func(context.Context, *BloemMusicArtistsInput) (*BloemMusicArtistsOutput, error) {
		return &BloemMusicArtistsOutput{}, nil
	})

	Register(reg, Operation{
		Operation: bloemOp("GET", "/music/artists/{id}", "getBloemMusicArtist", "music",
			"One artist and its albums."),
		Class: ClassProfileScoped,
	}, func(context.Context, *BloemMusicArtistInput) (*BloemMusicArtistOutput, error) {
		return &BloemMusicArtistOutput{}, nil
	})

	Register(reg, Operation{
		Operation: bloemOp("GET", "/music/albums/{id}", "getBloemMusicAlbum", "music",
			"One album and its tracks."),
		Class: ClassProfileScoped,
	}, func(context.Context, *BloemMusicAlbumInput) (*BloemMusicAlbumOutput, error) {
		return &BloemMusicAlbumOutput{}, nil
	})
}

// BloemPersonFilmographyEntry is one item credited to a person. Kind is the
// item's own kind ("movie" or "series"), not the credit's: a client renders the
// destination, and the credit itself is Role.
type BloemPersonFilmographyEntry struct {
	ContentID string `json:"content_id" doc:"Item this credit points at."`
	Title     string `json:"title"`
	Kind      string `json:"kind" doc:"The item's kind, not the credit's." example:"movie"`
	Year      int    `json:"year,omitempty"`
	Role      string `json:"role,omitempty" doc:"How this person was credited."`
	PosterURL string `json:"poster_url,omitempty"`
}

// BloemPersonDetail is one person and everything they are credited in that this
// viewer may see.
type BloemPersonDetail struct {
	ID          string                        `json:"id"`
	Name        string                        `json:"name"`
	Bio         string                        `json:"bio,omitempty"`
	BirthDate   *string                       `json:"birth_date,omitempty" doc:"RFC 3339 date, when known."`
	DeathDate   *string                       `json:"death_date,omitempty" doc:"RFC 3339 date, when known."`
	Birthplace  string                        `json:"birthplace,omitempty"`
	PhotoURL    string                        `json:"photo_url,omitempty"`
	Filmography []BloemPersonFilmographyEntry `json:"filmography" doc:"Credits filtered to what this viewer may see."`
}

// BloemPersonOutput is the person detail envelope.
type BloemPersonOutput struct {
	Body BloemPersonDetail
}

func registerBloemPersons(reg *Registry) {
	Register(reg, Operation{
		Operation: bloemOp("GET", "/persons/{person_id}", "getBloemPersonDetail", "catalog",
			"One person and their credits, filtered to what this viewer may see."),
		Class: ClassProfileScoped,
	}, func(context.Context, *BloemPersonInput) (*BloemPersonOutput, error) {
		return &BloemPersonOutput{}, nil
	})
}
