package catalog

import (
	"context"
	"errors"
	"fmt"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
)

// NativePhaseItemAccess invokes the SAME host predicate in the actual producer
// query/transaction, including lawful rows inserted earlier in that transaction.
// It neither changes isolation nor introduces a retained authorization decision.
type NativePhaseItemAccess struct{ Query NativePhaseQuery }

func (a NativePhaseItemAccess) EnsureAccessible(ctx context.Context, id string, filter AccessFilter) error {
	if filter.AllowedLibraryIDs != nil && len(filter.AllowedLibraryIDs) == 0 {
		return ErrItemNotFound
	}
	if a.Query == nil {
		return fmt.Errorf("native phase query unavailable")
	}
	sql, args := buildEnsureAccessibleSQL(id, filter)
	var found int
	if err := a.Query.QueryRow(ctx, sql, args...).Scan(&found); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrItemNotFound
		}
		return fmt.Errorf("checking item access: %w", err)
	}
	return nil
}

// Parent lookup adapters use the owning row decoder; authorization remains in
// MediaFileAuthorizer. No incoming file tuple substitutes for the stored row.
type NativePhaseEpisodeLookup struct{ Query NativePhaseQuery }

func (a NativePhaseEpisodeLookup) GetByID(ctx context.Context, id string) (*models.Episode, error) {
	if a.Query == nil {
		return nil, fmt.Errorf("native phase query unavailable")
	}
	return scanEpisode(a.Query.QueryRow(ctx, `SELECT `+episodeColumns+` FROM episodes WHERE content_id=$1`, id))
}

type NativePhaseExtraLookup struct{ Query NativePhaseQuery }

func (a NativePhaseExtraLookup) GetByID(ctx context.Context, id string) (*models.MediaExtra, error) {
	if a.Query == nil {
		return nil, fmt.Errorf("native phase query unavailable")
	}
	// Only the exact stored parent is consumed by MediaFileAuthorizer. This is
	// an admission lookup, not an alternate general ExtraRepository.
	extra := &models.MediaExtra{ContentID: id}
	err := a.Query.QueryRow(ctx, `SELECT parent_id FROM media_extras WHERE content_id=$1`, id).Scan(&extra.ParentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrExtraNotFound
	}
	if err != nil {
		return nil, err
	}
	return extra, nil
}
