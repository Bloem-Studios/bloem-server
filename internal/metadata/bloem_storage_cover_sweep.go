package metadata

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
)

// storageCoverPrefix is where artwork storage keeps storage covers.
const storageCoverPrefix = "storage-covers/"

// StorageCoverSweepStats summarizes one pass over the kept storage covers.
type StorageCoverSweepStats struct {
	Scanned    int `json:"scanned"`
	Referenced int `json:"referenced"`
	TooNew     int `json:"too_new"`
	Unparsable int `json:"unparsable"`
	Deleted    int `json:"deleted"`
	Pages      int `json:"pages"`
}

// StorageCoverSweeper deletes storage covers kept in artwork storage
// (artworkkey.StorageCoverKey) whose book no longer names them: the book was
// removed, or its source replaced the cover, which gives it a new key. The
// artwork storage sweep does not walk this prefix, since the catalog names
// these covers by their bloem-storage:// path rather than by their key.
type StorageCoverSweeper struct {
	pool  *pgxpool.Pool
	store ArtworkStorageLister
	now   func() time.Time
	// posters returns each given content ID's poster path; tests substitute it.
	posters func(ctx context.Context, contentIDs []string) (map[string]string, error)
}

// NewStorageCoverSweeper returns nil when the sweep cannot run.
func NewStorageCoverSweeper(pool *pgxpool.Pool, store ArtworkStorageLister) *StorageCoverSweeper {
	if pool == nil || store == nil {
		return nil
	}
	s := &StorageCoverSweeper{pool: pool, store: store, now: time.Now}
	s.posters = s.posterPaths
	return s
}

// Sweep walks the kept storage covers, at most maxPages pages, and deletes
// those no book names. As in the artwork storage sweep, an object younger than
// artworkSweepMinAge is left alone. Unlike stored artwork, a kept cover is
// only a copy of its source's, so a page of mostly unnamed covers, such as a
// removed library's, is deleted rather than held back as an anomaly.
func (s *StorageCoverSweeper) Sweep(ctx context.Context, maxPages int) (StorageCoverSweepStats, error) {
	var stats StorageCoverSweepStats
	if s == nil {
		return stats, nil
	}
	cutoff := s.now().Add(-artworkSweepMinAge)
	cursor := ""
	for stats.Pages < maxPages {
		objects, next, err := s.store.List(ctx, storageCoverPrefix, cursor, artworkSweepPageSize)
		if err != nil {
			return stats, fmt.Errorf("storage cover sweep: list: %w", err)
		}
		stats.Pages++
		stats.Scanned += len(objects)
		byContent := map[string][]string{}
		for _, object := range objects {
			contentID, _, ok := artworkkey.ParseStorageCoverKey(object.Key)
			switch {
			case !ok:
				stats.Unparsable++
			case object.ModTime.After(cutoff):
				stats.TooNew++
			default:
				byContent[contentID] = append(byContent[contentID], object.Key)
			}
		}
		ids := make([]string, 0, len(byContent))
		for id := range byContent {
			ids = append(ids, id)
		}
		posters, err := s.posters(ctx, ids)
		if err != nil {
			return stats, err
		}
		var unnamed []string
		for id, keys := range byContent {
			for _, key := range keys {
				if posters[id] == artworkkey.StorageCoverScheme+"://"+key {
					stats.Referenced++
				} else {
					unnamed = append(unnamed, key)
				}
			}
		}
		if len(unnamed) > 0 {
			deleted, err := s.store.Delete(ctx, unnamed)
			stats.Deleted += deleted
			if err != nil {
				return stats, fmt.Errorf("storage cover sweep: delete: %w", err)
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return stats, nil
}

func (s *StorageCoverSweeper) posterPaths(ctx context.Context, contentIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(contentIDs))
	if len(contentIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT content_id, COALESCE(poster_path, '') FROM media_items WHERE content_id = ANY($1)`, contentIDs)
	if err != nil {
		return nil, fmt.Errorf("storage cover sweep: posters: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, poster string
		if err := rows.Scan(&id, &poster); err != nil {
			return nil, fmt.Errorf("storage cover sweep: scan poster: %w", err)
		}
		out[id] = poster
	}
	return out, rows.Err()
}
