package api

import (
	"context"
	"errors"
	"strings"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	managedv1 "github.com/Silo-Server/silo-server/internal/managedtracking/wire"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func (s *managedCalendarSource) LookupAvailability(ctx context.Context, p managedtracking.Profile, targets []*managedv1.AvailabilityTarget) ([]*managedv1.AvailabilityResult, error) {
	scope, err := s.scope(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]*managedv1.AvailabilityResult, 0, len(targets))
	for _, target := range targets {
		result := &managedv1.AvailabilityResult{RequestId: target.RequestId}
		out = append(out, result)
		if !scope.PlaybackAllowed {
			continue
		}
		if s.pool == nil {
			return nil, managedtracking.ErrAuthority
		}
		// Namespace is a closed enum; values always remain bound parameters.
		var column string
		switch target.Namespace {
		case "tmdb":
			column = "tmdb_id"
		case "imdb":
			column = "imdb_id"
		case "tvdb":
			column = "tvdb_id"
		default:
			return nil, managedtracking.ErrSnapshot
		}
		rows, err := s.pool.Query(ctx, `SELECT content_id FROM media_items WHERE type=$1 AND `+column+`=$2 LIMIT 2`, target.Kind, target.ExternalId)
		if err != nil {
			return nil, err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(ids) != 1 {
			continue
		}
		visible, err := catalog.NewItemRepository(s.pool).GetByIDsWithAccess(ctx, ids, catalog.AccessFilter{UserID: scope.UserID, ProfileID: scope.ProfileID, AllowedLibraryIDs: scope.AllowedLibraryIDs, DisabledLibraryIDs: scope.DisabledLibraryIDs, MaturityLimits: scope.MaturityLimits, MaxPlaybackQuality: scope.MaxPlaybackQuality})
		if err != nil {
			return nil, err
		}
		if len(visible) != 1 {
			continue
		}
		// Metadata-only and missing files never establish playback availability.
		files, err := s.pool.Query(ctx, `SELECT f.id,f.media_folder_id,f.file_path FROM media_files f JOIN media_folders l ON l.id=f.media_folder_id AND l.enabled
   WHERE f.missing_since IS NULL AND (f.content_id=$1 OR f.episode_id IN(SELECT content_id FROM episodes WHERE series_id=$1))
   AND ($2::integer[] IS NULL OR f.media_folder_id=ANY($2)) AND NOT(f.media_folder_id=ANY($3::integer[])) ORDER BY f.id LIMIT 101`, ids[0], scope.AllowedLibraryIDs, nonNilLibraryIDs(scope.DisabledLibraryIDs))
		if err != nil {
			return nil, err
		}
		type fileRef struct {
			id, folder int
			path       string
		}
		refs := []fileRef{}
		for files.Next() {
			var f fileRef
			if err = files.Scan(&f.id, &f.folder, &f.path); err != nil {
				files.Close()
				return nil, err
			}
			refs = append(refs, f)
		}
		err = files.Err()
		files.Close()
		if err != nil {
			return nil, err
		}
		for _, f := range refs {
			if f.path == "" {
				continue
			}
			if strings.HasPrefix(f.path, "bloem-storage:") {
				_, _, e := storagesource.NewRepository(s.pool).FileReference(ctx, f.id, f.folder)
				if errors.Is(e, storagesource.ErrReferenceConflict) || errors.Is(e, storagesource.ErrSourceUnavailable) {
					continue
				}
				if e != nil {
					return nil, e
				}
			}
			result.Available = true
			result.ContentId = ids[0]
			break
		}
	}
	return out, nil
}
func nonNilLibraryIDs(ids []int) []int {
	if ids == nil {
		return []int{}
	}
	return ids
}
