package catalog

import (
	"context"
	"fmt"
	"strconv"

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
// Current catalog multipart indexes originate in filename sorting and have no
// authoritative order provenance. Such editions are refused, not promoted to
// timeline authority. A sole complete, locally probed file needs no part-order
// inference. Adding multipart support requires an authoritative catalog source.
type ClientPlaybackManifestResolver struct {
	items clientPlaybackManifestItems
	files clientPlaybackManifestFiles
}

func NewClientPlaybackManifestResolver(pool *pgxpool.Pool) *ClientPlaybackManifestResolver {
	if pool == nil {
		return &ClientPlaybackManifestResolver{}
	}
	return &ClientPlaybackManifestResolver{items: NewItemRepository(pool), files: clientPlaybackManifestFileReader{pool}}
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
	files, err := r.files.loadClientPlaybackFiles(ctx, anchorFileID)
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
	items, err := r.items.GetByIDsWithAccess(ctx, []string{anchor.ContentID}, filter)
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
	for _, f := range files {
		if f == nil || f.ContentID != anchor.ContentID || f.EpisodeID != "" || f.ExtraID != "" ||
			f.MissingSince != nil || !FileAllowedByAccess(f, filter) {
			return empty, ErrItemNotFound
		}
	}
	// Neither filename order nor scanner-derived presentation indexes establish
	// multipart authority. Do not choose a subset by edition labels or durations.
	if len(files) != 1 || anchor.PresentationKind != "" || anchor.PresentationGroupKey != "" ||
		anchor.PresentationPartIndex != 0 || anchor.PresentationPartTotal != 0 {
		return empty, fmt.Errorf("%w: authoritative edition membership and part order unavailable", playback.ErrClientPlaybackTimelineV3)
	}
	if anchor.ProbeSource != "local" || anchor.Duration <= 0 {
		return empty, fmt.Errorf("%w: trusted positive duration unavailable", playback.ErrClientPlaybackTimelineV3)
	}
	// File identity distinguishes this sole-file edition without trusting a
	// parsed edition label. The constructor binds item, edition, file and duration.
	return playback.NewClientPlaybackManifestV3(anchor.ContentID, "file:"+strconv.Itoa(anchor.ID),
		[]playback.ClientPlaybackPartV3{{FileID: anchor.ID, DurationSeconds: float64(anchor.Duration)}})
}
