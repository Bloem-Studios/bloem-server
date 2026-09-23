package music

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/jackc/pgx/v5/pgxpool"
)

type musicAccessFixture struct {
	pool                    *pgxpool.Pool
	enabledLib, disabledLib int
	artistPG, artistR       string
	albumPG, albumR         string
	artistOff, albumOff     string
}

// seedMusicAccessFixture seeds an enabled library with a PG album and an R
// album (distinct artists), and a disabled library with one album.
func seedMusicAccessFixture(t *testing.T) musicAccessFixture {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)

	s := time.Now().UnixNano()
	f := musicAccessFixture{
		pool:      pool,
		artistPG:  fmt.Sprintf("mx-artist-pg-%d", s),
		artistR:   fmt.Sprintf("mx-artist-r-%d", s),
		artistOff: fmt.Sprintf("mx-artist-off-%d", s),
		albumPG:   fmt.Sprintf("mx-album-pg-%d", s),
		albumR:    fmt.Sprintf("mx-album-r-%d", s),
		albumOff:  fmt.Sprintf("mx-album-off-%d", s),
	}
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	must("seed enabled library", pool.QueryRow(ctx, `INSERT INTO media_folders (type, name, enabled) VALUES ('music', $1, true) RETURNING id`, fmt.Sprintf("mx on %d", s)).Scan(&f.enabledLib))
	must("seed disabled library", pool.QueryRow(ctx, `INSERT INTO media_folders (type, name, enabled) VALUES ('music', $1, false) RETURNING id`, fmt.Sprintf("mx off %d", s)).Scan(&f.disabledLib))
	t.Cleanup(func() {
		libs := []int{f.enabledLib, f.disabledLib}
		_, _ = pool.Exec(ctx, `DELETE FROM media_files WHERE media_folder_id = ANY($1)`, libs)
		_, _ = pool.Exec(ctx, `DELETE FROM media_items WHERE content_id = ANY($1)`, []string{f.albumPG, f.albumR, f.albumOff})
		_, _ = pool.Exec(ctx, `DELETE FROM music_artists WHERE id = ANY($1)`, []string{f.artistPG, f.artistR, f.artistOff})
		_, _ = pool.Exec(ctx, `DELETE FROM media_folders WHERE id = ANY($1)`, libs)
	})

	seedAlbum := func(album, artist, rating string, lib int) {
		t.Helper()
		_, err := pool.Exec(ctx, `INSERT INTO media_items (content_id, type, title, status, genres, content_rating) VALUES ($1, 'music_album', $1, 'matched', '{}'::text[], $2)`, album, rating)
		must("seed item "+album, err)
		_, err = pool.Exec(ctx, `INSERT INTO music_artists (id, name, sort_name) VALUES ($1, $1, $1)`, artist)
		must("seed artist "+artist, err)
		_, err = pool.Exec(ctx, `INSERT INTO music_albums (content_id, artist_id, year) VALUES ($1, $2, 2026)`, album, artist)
		must("seed album "+album, err)
		_, err = pool.Exec(ctx, `INSERT INTO media_item_libraries (content_id, media_folder_id, first_seen_at) VALUES ($1, $2, NOW())`, album, lib)
		must("seed membership "+album, err)
		var fileID int
		must("seed file "+album, pool.QueryRow(ctx, `INSERT INTO media_files (content_id, media_folder_id, file_path, file_size, base_type) VALUES ($1, $2, $3, 1024, 'music') RETURNING id`, album, lib, "/"+album+"/t.flac").Scan(&fileID))
		_, err = pool.Exec(ctx, `INSERT INTO music_tracks (id, album_id, artist_id, media_file_id, title, duration_ms, disc_number, track_number) VALUES ($1, $2, $3, $4, 'T', 60000, 1, 1)`, album+"-t", album, artist, fileID)
		must("seed track "+album, err)
	}
	seedAlbum(f.albumPG, f.artistPG, "PG", f.enabledLib)
	seedAlbum(f.albumR, f.artistR, "R", f.enabledLib)
	seedAlbum(f.albumOff, f.artistOff, "PG", f.disabledLib)
	return f
}

func artistIDs(page ArtistPage) map[string]bool {
	out := map[string]bool{}
	for _, a := range page.Items {
		out[a.ID] = true
	}
	return out
}

// A library an admin disabled is invisible everywhere else in the catalog;
// the music browse must not keep serving it by id.
func TestMusicReadsHideDisabledLibraries(t *testing.T) {
	f := seedMusicAccessFixture(t)
	ctx := context.Background()
	repo := NewPostgresRepository(f.pool)
	open := catalog.AccessFilter{}

	page, err := repo.ListArtists(ctx, f.disabledLib, "", 500, open)
	if err != nil && !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListArtists(disabled): %v", err)
	}
	if artistIDs(page)[f.artistOff] {
		t.Fatalf("ListArtists(disabled library) returned %q", f.artistOff)
	}
	if _, err := repo.Artist(ctx, f.disabledLib, f.artistOff, open); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Artist(disabled library) err = %v, want ErrNotFound", err)
	}
	if _, err := repo.Album(ctx, f.disabledLib, f.albumOff, open); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Album(disabled library) err = %v, want ErrNotFound", err)
	}
	// The enabled library is unaffected.
	if _, err := repo.Album(ctx, f.enabledLib, f.albumPG, open); err != nil {
		t.Fatalf("Album(enabled library): %v", err)
	}
}

// A rating ceiling hides albums above it, and an artist whose only albums are
// above it, exactly as catalog reads do.
func TestMusicReadsApplyRatingCeiling(t *testing.T) {
	f := seedMusicAccessFixture(t)
	ctx := context.Background()
	repo := NewPostgresRepository(f.pool)
	pg := catalog.AccessFilter{AllowedLibraryIDs: []int{f.enabledLib}, MaxContentRating: "PG"}

	page, err := repo.ListArtists(ctx, f.enabledLib, "", 500, pg)
	if err != nil {
		t.Fatalf("ListArtists: %v", err)
	}
	got := artistIDs(page)
	if !got[f.artistPG] || got[f.artistR] {
		t.Fatalf("ListArtists(PG) = %v, want %q and not %q", got, f.artistPG, f.artistR)
	}
	if _, err := repo.Artist(ctx, f.enabledLib, f.artistR, pg); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Artist(R-only artist, PG ceiling) err = %v, want ErrNotFound", err)
	}
	if _, err := repo.Album(ctx, f.enabledLib, f.albumR, pg); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Album(R, PG ceiling) err = %v, want ErrNotFound", err)
	}
	detail, err := repo.Artist(ctx, f.enabledLib, f.artistPG, pg)
	if err != nil || len(detail.Albums) != 1 || detail.Albums[0].ID != f.albumPG {
		t.Fatalf("Artist(PG) = %+v, %v", detail, err)
	}
	// An unrestricted viewer still sees both.
	page, err = repo.ListArtists(ctx, f.enabledLib, "", 500, catalog.AccessFilter{})
	if err != nil || !artistIDs(page)[f.artistR] {
		t.Fatalf("ListArtists(unrestricted) = %+v, %v", page, err)
	}
}

// A content allow-list (AllowedContentIDs) narrows music reads to the listed
// albums.
func TestMusicReadsHonorAllowedContentIDs(t *testing.T) {
	f := seedMusicAccessFixture(t)
	ctx := context.Background()
	repo := NewPostgresRepository(f.pool)
	only := catalog.AccessFilter{AllowedContentIDs: []string{f.albumPG}}

	page, err := repo.ListArtists(ctx, f.enabledLib, "", 500, only)
	if err != nil {
		t.Fatalf("ListArtists: %v", err)
	}
	if got := artistIDs(page); !got[f.artistPG] || got[f.artistR] {
		t.Fatalf("ListArtists(allow-list) = %v", got)
	}
	if _, err := repo.Album(ctx, f.enabledLib, f.albumR, only); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Album outside allow-list err = %v, want ErrNotFound", err)
	}
}
