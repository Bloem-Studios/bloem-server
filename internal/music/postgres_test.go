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

func TestLibraryAllowedFailsClosedForScopedAndDisabledLibraries(t *testing.T) {
	cases := []struct {
		name   string
		id     int
		filter catalog.AccessFilter
		want   bool
	}{
		{name: "unrestricted", id: 7, want: true},
		{name: "invalid", id: 0, want: false},
		{name: "allowed unsorted", id: 7, filter: catalog.AccessFilter{AllowedLibraryIDs: []int{9, 7, 3}}, want: true},
		{name: "not allowed", id: 7, filter: catalog.AccessFilter{AllowedLibraryIDs: []int{9, 3}}, want: false},
		{name: "empty allow list", id: 7, filter: catalog.AccessFilter{AllowedLibraryIDs: []int{}}, want: false},
		{name: "disabled overrides allowed", id: 7, filter: catalog.AccessFilter{AllowedLibraryIDs: []int{7}, DisabledLibraryIDs: []int{7}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := libraryAllowed(tc.id, tc.filter); got != tc.want {
				t.Fatalf("libraryAllowed(%d, %+v) = %v, want %v", tc.id, tc.filter, got, tc.want)
			}
		})
	}
}

func TestAlbumScopesExistenceAndTracksToRequestedLibrary(t *testing.T) {
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

	suffix := time.Now().UnixNano()
	albumID := fmt.Sprintf("music-library-album-%d", suffix)
	artistID := fmt.Sprintf("music-library-artist-%d", suffix)
	trackAID := fmt.Sprintf("music-library-track-a-%d", suffix)
	trackBID := fmt.Sprintf("music-library-track-b-%d", suffix)
	var libraryA, libraryB int
	if err := pool.QueryRow(ctx, `
		INSERT INTO media_folders (type, name, enabled)
		VALUES ('music', $1, true) RETURNING id
	`, fmt.Sprintf("Music library A %d", suffix)).Scan(&libraryA); err != nil {
		t.Fatalf("seed music library A: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO media_folders (type, name, enabled)
		VALUES ('music', $1, true) RETURNING id
	`, fmt.Sprintf("Music library B %d", suffix)).Scan(&libraryB); err != nil {
		t.Fatalf("seed music library B: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM media_files WHERE media_folder_id = ANY($1)`, []int{libraryA, libraryB})
		_, _ = pool.Exec(ctx, `DELETE FROM media_items WHERE content_id = $1`, albumID)
		_, _ = pool.Exec(ctx, `DELETE FROM music_artists WHERE id = $1`, artistID)
		_, _ = pool.Exec(ctx, `DELETE FROM media_folders WHERE id = ANY($1)`, []int{libraryA, libraryB})
	})

	if _, err := pool.Exec(ctx, `
		INSERT INTO media_items (content_id, type, title, status, genres)
		VALUES ($1, 'music_album', 'Shared Album', 'matched', '{}'::text[])
	`, albumID); err != nil {
		t.Fatalf("seed shared music item: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO music_artists (id, name, sort_name)
		VALUES ($1, 'Shared Artist', 'Shared Artist')
	`, artistID); err != nil {
		t.Fatalf("seed shared music artist: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO music_albums (content_id, artist_id, year)
		VALUES ($2, $1, 2026)
	`, artistID, albumID); err != nil {
		t.Fatalf("seed shared music album: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_item_libraries (content_id, media_folder_id, first_seen_at)
		VALUES ($1, $2, NOW()), ($1, $3, NOW())
	`, albumID, libraryA, libraryB); err != nil {
		t.Fatalf("seed shared music memberships: %v", err)
	}
	var fileA, fileB int
	if err := pool.QueryRow(ctx, `
		INSERT INTO media_files (content_id, media_folder_id, file_path, file_size, base_type)
		VALUES ($1, $2, $3, 1024, 'music') RETURNING id
	`, albumID, libraryA, fmt.Sprintf("/music-a-%d/track.flac", suffix)).Scan(&fileA); err != nil {
		t.Fatalf("seed music file A: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO media_files (content_id, media_folder_id, file_path, file_size, base_type)
		VALUES ($1, $2, $3, 1024, 'music') RETURNING id
	`, albumID, libraryB, fmt.Sprintf("/music-b-%d/track.flac", suffix)).Scan(&fileB); err != nil {
		t.Fatalf("seed music file B: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO music_tracks (
			id, album_id, artist_id, media_file_id, title,
			duration_ms, disc_number, track_number
		) VALUES
			($1, $3, $4, $5, 'Library A Track', 60000, 1, 1),
			($2, $3, $4, $6, 'Library B Track', 60000, 1, 2)
	`, trackAID, trackBID, albumID, artistID, fileA, fileB); err != nil {
		t.Fatalf("seed library-scoped music tracks: %v", err)
	}

	repo := NewPostgresRepository(pool)
	detail, err := repo.Album(ctx, libraryA, albumID, catalog.AccessFilter{AllowedLibraryIDs: []int{libraryA}})
	if err != nil {
		t.Fatalf("Album(library A): %v", err)
	}
	if len(detail.Tracks) != 1 || detail.Tracks[0].ID != trackAID {
		t.Fatalf("Album(library A) tracks = %+v, want only %q", detail.Tracks, trackAID)
	}

	if _, err := pool.Exec(ctx, `UPDATE media_files SET missing_since = NOW() WHERE id = $1`, fileA); err != nil {
		t.Fatalf("mark library A track missing: %v", err)
	}
	_, err = repo.Album(ctx, libraryA, albumID, catalog.AccessFilter{AllowedLibraryIDs: []int{libraryA}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Album(library A) with only foreign active tracks error = %v, want ErrNotFound", err)
	}

	filterA := catalog.AccessFilter{AllowedLibraryIDs: []int{libraryA}}
	status, err := repo.Status(ctx, filterA)
	if err != nil {
		t.Fatalf("Status(library A): %v", err)
	}
	if status.Available || len(status.LibraryIDs) != 0 {
		t.Fatalf("Status(library A) = %+v, want unavailable without library-owned active tracks", status)
	}
	artists, err := repo.ListArtists(ctx, libraryA, "", 100, filterA)
	if err != nil {
		t.Fatalf("ListArtists(library A): %v", err)
	}
	if len(artists.Items) != 0 {
		t.Fatalf("ListArtists(library A) = %+v, want no artists without library-owned active tracks", artists.Items)
	}
	_, err = repo.Artist(ctx, libraryA, artistID, filterA)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Artist(library A) with only foreign active tracks error = %v, want ErrNotFound", err)
	}
}
