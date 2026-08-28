package music

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Status(ctx context.Context, filter catalog.AccessFilter) (Status, error) {
	if r == nil || r.pool == nil {
		return Status{}, errors.New("music repository is not configured")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT mil.media_folder_id
		FROM music_albums ma
		JOIN media_item_libraries mil ON mil.content_id = ma.content_id
		JOIN media_folders mf ON mf.id = mil.media_folder_id
		JOIN music_tracks mt ON mt.album_id = ma.content_id
		JOIN media_files file ON file.id = mt.media_file_id
			AND file.media_folder_id = mil.media_folder_id
			AND file.missing_since IS NULL
		WHERE mf.enabled = true
		ORDER BY mil.media_folder_id`)
	if err != nil {
		return Status{}, fmt.Errorf("list music libraries: %w", err)
	}
	defer rows.Close()
	ids := make([]int, 0)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return Status{}, fmt.Errorf("scan music library: %w", err)
		}
		if libraryAllowed(id, filter) {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return Status{}, fmt.Errorf("iterate music libraries: %w", err)
	}
	return Status{Available: len(ids) > 0, LibraryIDs: ids}, nil
}

func (r *PostgresRepository) ListArtists(ctx context.Context, libraryID int, cursor string, limit int, filter catalog.AccessFilter) (ArtistPage, error) {
	if r == nil || r.pool == nil || !libraryAllowed(libraryID, filter) {
		return ArtistPage{}, ErrNotFound
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ar.id, ar.name, ar.artwork_path
		FROM music_artists ar
		WHERE ar.id > $1
		  AND EXISTS (
			SELECT 1 FROM music_albums ma
			JOIN media_item_libraries mil ON mil.content_id = ma.content_id
			JOIN music_tracks mt ON mt.album_id = ma.content_id
			JOIN media_files mf ON mf.id = mt.media_file_id
				AND mf.media_folder_id = mil.media_folder_id
				AND mf.missing_since IS NULL
			WHERE ma.artist_id = ar.id AND mil.media_folder_id = $2
		  )
		ORDER BY ar.id
		LIMIT $3`, strings.TrimSpace(cursor), libraryID, limit+1)
	if err != nil {
		return ArtistPage{}, fmt.Errorf("list music artists: %w", err)
	}
	defer rows.Close()
	items := make([]Artist, 0, limit+1)
	for rows.Next() {
		var item Artist
		if err := rows.Scan(&item.ID, &item.Name, &item.ArtworkPath); err != nil {
			return ArtistPage{}, fmt.Errorf("scan music artist: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ArtistPage{}, fmt.Errorf("iterate music artists: %w", err)
	}
	page := ArtistPage{Items: items}
	if len(items) > limit {
		page.NextCursor = items[limit-1].ID
		page.Items = items[:limit]
	}
	return page, nil
}

func (r *PostgresRepository) Artist(ctx context.Context, libraryID int, artistID string, filter catalog.AccessFilter) (ArtistDetail, error) {
	if r == nil || r.pool == nil || !libraryAllowed(libraryID, filter) || strings.TrimSpace(artistID) == "" {
		return ArtistDetail{}, ErrNotFound
	}
	var artist Artist
	err := r.pool.QueryRow(ctx, `
		SELECT ar.id, ar.name, ar.artwork_path
		FROM music_artists ar
		WHERE ar.id = $1 AND EXISTS (
			SELECT 1 FROM music_albums ma
			JOIN media_item_libraries mil ON mil.content_id = ma.content_id
			JOIN music_tracks mt ON mt.album_id = ma.content_id
			JOIN media_files mf ON mf.id = mt.media_file_id
				AND mf.media_folder_id = mil.media_folder_id
				AND mf.missing_since IS NULL
			WHERE ma.artist_id = ar.id AND mil.media_folder_id = $2
		)`, artistID, libraryID).Scan(&artist.ID, &artist.Name, &artist.ArtworkPath)
	if errors.Is(err, pgx.ErrNoRows) {
		return ArtistDetail{}, ErrNotFound
	}
	if err != nil {
		return ArtistDetail{}, fmt.Errorf("get music artist: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT mi.content_id, mi.title, ma.artist_id, ar.name, mi.poster_path, COALESCE(ma.year, 0)
		FROM music_albums ma
		JOIN media_items mi ON mi.content_id = ma.content_id
		JOIN music_artists ar ON ar.id = ma.artist_id
		JOIN media_item_libraries mil ON mil.content_id = ma.content_id
		JOIN music_tracks mt ON mt.album_id = ma.content_id
		JOIN media_files mf ON mf.id = mt.media_file_id
			AND mf.media_folder_id = mil.media_folder_id
			AND mf.missing_since IS NULL
		WHERE ma.artist_id = $1 AND mil.media_folder_id = $2
		GROUP BY mi.content_id, ma.artist_id, ar.name, ma.year
		ORDER BY COALESCE(ma.year, 0), lower(mi.title), mi.content_id`, artistID, libraryID)
	if err != nil {
		return ArtistDetail{}, fmt.Errorf("list music albums: %w", err)
	}
	defer rows.Close()
	albums := make([]Album, 0)
	for rows.Next() {
		var album Album
		if err := rows.Scan(&album.ID, &album.Title, &album.ArtistID, &album.ArtistName, &album.ArtworkPath, &album.Year); err != nil {
			return ArtistDetail{}, fmt.Errorf("scan music album: %w", err)
		}
		albums = append(albums, album)
	}
	return ArtistDetail{Artist: artist, Albums: albums}, rows.Err()
}

func (r *PostgresRepository) Album(ctx context.Context, libraryID int, albumID string, filter catalog.AccessFilter) (AlbumDetail, error) {
	if r == nil || r.pool == nil || !libraryAllowed(libraryID, filter) || strings.TrimSpace(albumID) == "" {
		return AlbumDetail{}, ErrNotFound
	}
	var album Album
	err := r.pool.QueryRow(ctx, `
		SELECT mi.content_id, mi.title, ma.artist_id, ar.name, mi.poster_path, COALESCE(ma.year, 0)
		FROM music_albums ma
		JOIN media_items mi ON mi.content_id = ma.content_id
		JOIN music_artists ar ON ar.id = ma.artist_id
		JOIN media_item_libraries mil ON mil.content_id = ma.content_id
		WHERE ma.content_id = $1 AND mil.media_folder_id = $2
		  AND EXISTS (
			SELECT 1 FROM music_tracks active_mt
			JOIN media_files active_mf ON active_mf.id = active_mt.media_file_id
			WHERE active_mt.album_id = ma.content_id
			  AND active_mf.media_folder_id = $2
			  AND active_mf.missing_since IS NULL
		  )`, albumID, libraryID).
		Scan(&album.ID, &album.Title, &album.ArtistID, &album.ArtistName, &album.ArtworkPath, &album.Year)
	if errors.Is(err, pgx.ErrNoRows) {
		return AlbumDetail{}, ErrNotFound
	}
	if err != nil {
		return AlbumDetail{}, fmt.Errorf("get music album: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT mt.id, mt.title, mt.album_id, mt.artist_id, mt.media_file_id,
		       mt.duration_ms, mt.disc_number, mt.track_number, mi.poster_path
		FROM music_tracks mt
		JOIN media_items mi ON mi.content_id = mt.album_id
		JOIN media_files mf ON mf.id = mt.media_file_id AND mf.missing_since IS NULL
		WHERE mt.album_id = $1 AND mf.media_folder_id = $2
		ORDER BY mt.disc_number, mt.track_number, mt.id`, albumID, libraryID)
	if err != nil {
		return AlbumDetail{}, fmt.Errorf("list music tracks: %w", err)
	}
	defer rows.Close()
	tracks := make([]Track, 0)
	for rows.Next() {
		var track Track
		if err := rows.Scan(&track.ID, &track.Title, &track.AlbumID, &track.ArtistID, &track.MediaFileID, &track.DurationMS, &track.DiscNumber, &track.TrackNumber, &track.ArtworkPath); err != nil {
			return AlbumDetail{}, fmt.Errorf("scan music track: %w", err)
		}
		tracks = append(tracks, track)
	}
	return AlbumDetail{Album: album, Tracks: tracks}, rows.Err()
}

func libraryAllowed(libraryID int, filter catalog.AccessFilter) bool {
	if libraryID <= 0 {
		return false
	}
	if filter.AllowedLibraryIDs != nil {
		found := false
		for _, candidate := range filter.AllowedLibraryIDs {
			if candidate == libraryID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, disabled := range filter.DisabledLibraryIDs {
		if disabled == libraryID {
			return false
		}
	}
	return true
}
