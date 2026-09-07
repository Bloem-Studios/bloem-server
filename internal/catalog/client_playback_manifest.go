package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
)

const clientPlaybackManifestLimit = 4096

type clientPlaybackManifestItems interface {
	GetByIDsWithAccess(context.Context, []string, AccessFilter) ([]*models.MediaItem, error)
}

type clientPlaybackManifestFiles interface {
	// Include missing files: filtering them could turn an incomplete edition into a singleton.
	loadClientPlaybackFiles(context.Context, int) ([]*models.MediaFile, error)
}

// ClientPlaybackManifestResolver reads catalog membership under the authenticated
// request's resolved access scope. It does not probe files, mutate metadata, or
// resolve an installed session again. Installation and discovered-digest checks
// belong to the discovery/start application layer.
//
// Persisted catalog group/index/total metadata defines native part order. This
// validates completeness as represented by the catalog, not publisher or disk
// completeness. It never sorts current filenames or accepts client offsets.
type ClientPlaybackManifestResolver struct {
	pool  *pgxpool.Pool
	items clientPlaybackManifestItems
	files clientPlaybackManifestFiles
}

func NewClientPlaybackManifestResolver(pool *pgxpool.Pool) *ClientPlaybackManifestResolver {
	if pool == nil {
		return &ClientPlaybackManifestResolver{}
	}
	return &ClientPlaybackManifestResolver{pool: pool, items: NewItemRepository(pool), files: clientPlaybackManifestFileReader{pool}}
}

var _ playback.ClientPlaybackTimelineResolverV3 = (*ClientPlaybackManifestResolver)(nil)

func (r *ClientPlaybackManifestResolver) ResolveClientPlaybackManifest(ctx context.Context, userID int, profileID string, anchorFileID int) (playback.ClientPlaybackManifestV3, error) {
	var empty playback.ClientPlaybackManifestV3
	scope, ok := access.GetScope(ctx)
	if !ok || userID <= 0 || profileID == "" || anchorFileID <= 0 ||
		scope.UserID != userID || scope.ProfileID != profileID || !scope.ProfileVerified {
		return empty, ErrItemNotFound
	}
	if r == nil || r.items == nil || r.files == nil {
		return empty, fmt.Errorf("%w: catalog resolver not configured", playback.ErrClientPlaybackTimelineV3)
	}
	// Direct assignment preserves nil (unrestricted) versus empty (deny all).
	filter := AccessFilter{UserID: userID, ProfileID: profileID,
		AllowedLibraryIDs: scope.AllowedLibraryIDs, DisabledLibraryIDs: scope.DisabledLibraryIDs,
		MaxContentRating: scope.MaxContentRating, MaxPlaybackQuality: scope.MaxPlaybackQuality}
	if filter.AllowedLibraryIDs != nil && len(filter.AllowedLibraryIDs) == 0 {
		return empty, ErrItemNotFound
	}
	itemsReader, filesReader := r.items, r.files
	if r.pool != nil {
		tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if err != nil {
			return empty, err
		}
		defer tx.Rollback(ctx)
		itemsReader = clientPlaybackManifestItemReader{tx}
		filesReader = clientPlaybackManifestFileReader{tx}
	}
	files, err := filesReader.loadClientPlaybackFiles(ctx, anchorFileID)
	if err != nil {
		return empty, err
	}
	var anchor *models.MediaFile
	for _, f := range files {
		if f != nil && f.ID == anchorFileID {
			anchor = f
			break
		}
	}
	if anchor == nil || anchor.ContentID == "" || anchor.EpisodeID != "" || anchor.ExtraID != "" ||
		anchor.MissingSince != nil || !FileAllowedByAccess(anchor, filter) {
		return empty, ErrItemNotFound
	}
	items, err := itemsReader.GetByIDsWithAccess(ctx, []string{anchor.ContentID}, filter)
	if err != nil {
		return empty, err
	}
	if len(items) != 1 || items[0] == nil || items[0].ContentID != anchor.ContentID {
		return empty, ErrItemNotFound
	}
	if items[0].Type != "audiobook" {
		return empty, fmt.Errorf("%w: item is not an audiobook", playback.ErrClientPlaybackTimelineV3)
	}
	return clientPlaybackManifestFromFiles(anchor, files, filter)
}

func clientPlaybackManifestFromFiles(anchor *models.MediaFile, files []*models.MediaFile, filter AccessFilter) (playback.ClientPlaybackManifestV3, error) {
	var empty playback.ClientPlaybackManifestV3
	if len(files) == 0 || len(files) > clientPlaybackManifestLimit {
		return empty, fmt.Errorf("%w: incomplete or oversized edition", playback.ErrClientPlaybackTimelineV3)
	}
	// A group-less singleton is a complete edition only when no sibling row or
	// partial group hint makes its membership ambiguous.
	if anchor.PresentationKind == "" && anchor.PresentationGroupKey == "" &&
		anchor.PresentationPartIndex == 0 && anchor.PresentationPartTotal == 0 && len(files) == 1 {
		if err := validateClientPlaybackPart(anchor, anchor, filter); err != nil {
			return empty, err
		}
		return playback.NewClientPlaybackManifestV3(anchor.ContentID, "file:"+strconv.Itoa(anchor.ID),
			[]playback.ClientPlaybackPartV3{{FileID: anchor.ID, DurationSeconds: float64(anchor.Duration)}})
	}
	if anchor.PresentationKind != "multipart" || anchor.PresentationGroupKey == "" ||
		anchor.PresentationPartTotal < 1 || anchor.PresentationPartTotal > clientPlaybackManifestLimit {
		return empty, fmt.Errorf("%w: ambiguous edition", playback.ErrClientPlaybackTimelineV3)
	}
	n := anchor.PresentationPartTotal
	parts := make([]playback.ClientPlaybackPartV3, n)
	seen := make(map[int]bool, n)
	count := 0
	for _, f := range files {
		if f == nil || f.ContentID != anchor.ContentID {
			return empty, ErrItemNotFound
		}
		// A distinct explicit catalog group is another edition. An ungrouped row
		// could be an unassigned member, so it cannot silently be left out.
		if f.PresentationGroupKey == "" {
			return empty, fmt.Errorf("%w: ungrouped edition member", playback.ErrClientPlaybackTimelineV3)
		}
		if f.PresentationGroupKey != anchor.PresentationGroupKey {
			if f.PresentationKind != "multipart" {
				return empty, fmt.Errorf("%w: mixed edition groups", playback.ErrClientPlaybackTimelineV3)
			}
			continue
		}
		if err := validateClientPlaybackPart(anchor, f, filter); err != nil {
			return empty, err
		}
		index := f.PresentationPartIndex
		if f.PresentationKind != "multipart" || f.EditionKey != anchor.EditionKey ||
			f.PresentationPartTotal != n || index < 1 || index > n || parts[index-1].FileID != 0 || seen[f.ID] {
			return empty, fmt.Errorf("%w: inconsistent edition order or membership", playback.ErrClientPlaybackTimelineV3)
		}
		seen[f.ID] = true
		parts[index-1] = playback.ClientPlaybackPartV3{FileID: f.ID, DurationSeconds: float64(f.Duration)}
		count++
	}
	if count != n {
		return empty, fmt.Errorf("%w: incomplete edition", playback.ErrClientPlaybackTimelineV3)
	}
	// Hash catalog identity rather than returning group/edition labels that may
	// contain scanner paths. Domain hashing separately binds ordered durations.
	identity, err := json.Marshal([]string{anchor.ContentID, anchor.PresentationGroupKey, anchor.EditionKey})
	if err != nil {
		return empty, err
	}
	edition := fmt.Sprintf("catalog:%x", sha256.Sum256(identity))
	return playback.NewClientPlaybackManifestV3(anchor.ContentID, edition, parts)
}

func validateClientPlaybackPart(anchor, f *models.MediaFile, filter AccessFilter) error {
	if f == nil || f.ID <= 0 || f.ContentID != anchor.ContentID || f.EpisodeID != "" || f.ExtraID != "" ||
		f.MissingSince != nil || !FileAllowedByAccess(f, filter) {
		return ErrItemNotFound
	}
	if f.ProbeSource != "local" || f.Duration <= 0 {
		return fmt.Errorf("%w: trusted positive duration unavailable", playback.ErrClientPlaybackTimelineV3)
	}
	return nil
}
