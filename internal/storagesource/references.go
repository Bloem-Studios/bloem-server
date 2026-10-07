package storagesource

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FileRef associates a published catalog file with the storage entry it reads
// and, when the provider offers one, the cover entry for its book.
type FileRef struct {
	MediaFileID int
	PersistedRef
	CoverEntryID, CoverRevision string
}

// AttachFilesTx records the storage entries behind published catalog files in
// the caller's publication transaction. Each file must belong to its
// location's library and carry that entry's catalog location as its path. A
// file keeps its location and entry; only its revision and display path change.
func AttachFilesTx(ctx context.Context, tx pgx.Tx, configurationRevision int64, refs []FileRef) error {
	if len(refs) == 0 {
		return nil
	}
	fileIDs := make([]int, len(refs))
	locationIDs := make([]string, len(refs))
	catalogPaths := make([]string, len(refs))
	entries := make([]string, len(refs))
	revisions := make([]string, len(refs))
	paths := make([]string, len(refs))
	coverEntries := make([]*string, len(refs))
	coverRevisions := make([]*string, len(refs))
	for i, ref := range refs {
		catalogPath, err := CatalogLocation(ref.LocationID, ref.EntryID)
		if err != nil || ref.MediaFileID <= 0 || !validText(ref.Revision, 4096, true) || !validText(ref.LogicalPath, 65536, false) ||
			(ref.CoverEntryID == "") != (ref.CoverRevision == "") || !validText(ref.CoverEntryID, 1024, false) || !validText(ref.CoverRevision, 4096, false) {
			return ErrReferenceConflict
		}
		fileIDs[i], locationIDs[i], catalogPaths[i] = ref.MediaFileID, ref.LocationID.String(), catalogPath
		entries[i], revisions[i], paths[i] = ref.EntryID, ref.Revision, ref.LogicalPath
		if ref.CoverEntryID != "" {
			coverEntries[i], coverRevisions[i] = &refs[i].CoverEntryID, &refs[i].CoverRevision
		}
	}
	tag, err := tx.Exec(ctx, `INSERT INTO bloem_storage_file_refs(media_file_id,location_id,entry_id,revision,logical_path,configuration_revision,cover_entry_id,cover_revision)
 SELECT t.f, l.id, t.e, t.r, t.p, $7, t.ce, t.cr
 FROM unnest($1::bigint[],$2::uuid[],$3::text[],$4::text[],$5::text[],$6::text[],$8::text[],$9::text[]) AS t(f,l,c,e,r,p,ce,cr)
 JOIN media_files f ON f.id = t.f AND f.file_path = t.c
 JOIN library_storage_locations l ON l.id = t.l AND l.folder_id = f.media_folder_id
 ON CONFLICT(media_file_id) DO UPDATE SET revision=EXCLUDED.revision,logical_path=EXCLUDED.logical_path,configuration_revision=EXCLUDED.configuration_revision,
  cover_entry_id=EXCLUDED.cover_entry_id,cover_revision=EXCLUDED.cover_revision
 WHERE bloem_storage_file_refs.location_id=EXCLUDED.location_id AND bloem_storage_file_refs.entry_id=EXCLUDED.entry_id`,
		fileIDs, locationIDs, catalogPaths, entries, revisions, paths, configurationRevision, coverEntries, coverRevisions)
	if err != nil {
		return err
	}
	if int(tag.RowsAffected()) != len(refs) {
		return ErrReferenceConflict
	}
	return nil
}

// FileReference resolves a catalog file in an already authorized library to
// its storage source and entry. An unavailable source still returns the typed
// reference with ErrSourceUnavailable; callers must never open the catalog
// key as a local path.
func (r *Repository) FileReference(ctx context.Context, fileID, authorizedFolderID int) (SourceConfig, PersistedRef, error) {
	var source SourceConfig
	var ref PersistedRef
	var available bool
	var location string
	if fileID <= 0 || authorizedFolderID <= 0 {
		return source, ref, ErrReferenceConflict
	}
	err := r.pool.QueryRow(ctx, `SELECT s.key,s.owner_id,s.installation_id,s.plugin_id,s.provider_source_id,s.root_entry_id,s.configuration_revision,s.enabled,
 x.location_id,x.entry_id,x.revision,x.logical_path,f.file_path,
 COALESCE(s.enabled AND i.enabled AND i.plugin_id=s.plugin_id AND i.owner_id=s.owner_id,false)
 FROM media_files f
 JOIN bloem_storage_file_refs x ON x.media_file_id=f.id
 JOIN library_storage_locations l ON l.id=x.location_id AND l.folder_id=f.media_folder_id
 JOIN bloem_storage_sources s ON s.key=l.source_key
 LEFT JOIN plugin_installations i ON i.id=s.installation_id
 WHERE f.id=$1 AND f.media_folder_id=$2`, fileID, authorizedFolderID).Scan(&source.Key, &source.OwnerID, &source.InstallationID, &source.PluginID, &source.ProviderSourceID, &source.RootEntryID, &source.ConfigurationRevision, &source.Enabled, &ref.LocationID, &ref.EntryID, &ref.Revision, &ref.LogicalPath, &location, &available)
	if errors.Is(err, pgx.ErrNoRows) {
		return source, ref, ErrReferenceConflict
	}
	if err != nil {
		return source, ref, err
	}
	expected, err := CatalogLocation(ref.LocationID, ref.EntryID)
	if err != nil || location != expected {
		return SourceConfig{}, PersistedRef{}, ErrReferenceConflict
	}
	if !available {
		return source, ref, ErrSourceUnavailable
	}
	return source, ref, nil
}

// CoverClaim is a published file whose book's cover has not been fetched at
// its current revision.
type CoverClaim struct {
	MediaFileID, FolderID       int
	ContentID                   string
	LocationID                  uuid.UUID
	CoverEntryID, CoverRevision string
}

// ClaimCovers claims up to limit files with a cover to fetch, for lease. A
// claim that is not marked fetched expires and is retried after the lease,
// which also backs off a cover that keeps failing.
func (r *Repository) ClaimCovers(ctx context.Context, limit int, lease time.Duration) ([]CoverClaim, error) {
	rows, err := r.pool.Query(ctx, `WITH pending AS (
  SELECT media_file_id FROM bloem_storage_file_refs
  WHERE cover_entry_id IS NOT NULL AND cover_revision IS DISTINCT FROM cover_fetched_revision
    AND (cover_claimed_until IS NULL OR cover_claimed_until < now())
  ORDER BY cover_claimed_until NULLS FIRST
  LIMIT $1 FOR UPDATE SKIP LOCKED)
 UPDATE bloem_storage_file_refs r SET cover_claimed_until = now() + ($2::bigint * interval '1 millisecond')
 FROM pending, media_files f
 WHERE r.media_file_id = pending.media_file_id AND f.id = r.media_file_id
 RETURNING r.media_file_id, f.media_folder_id, f.content_id, r.location_id, r.cover_entry_id, r.cover_revision`, limit, lease.Milliseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var claims []CoverClaim
	for rows.Next() {
		var c CoverClaim
		if err := rows.Scan(&c.MediaFileID, &c.FolderID, &c.ContentID, &c.LocationID, &c.CoverEntryID, &c.CoverRevision); err != nil {
			return nil, err
		}
		claims = append(claims, c)
	}
	return claims, rows.Err()
}

// MarkCoverFetched records that the claimed cover revision is stored. A
// revision that changed since the claim stays pending.
func (r *Repository) MarkCoverFetched(ctx context.Context, claim CoverClaim) error {
	_, err := r.pool.Exec(ctx, `UPDATE bloem_storage_file_refs SET cover_fetched_revision = $2, cover_claimed_until = NULL
 WHERE media_file_id = $1 AND cover_revision = $2`, claim.MediaFileID, claim.CoverRevision)
	return err
}

// LocationSource returns the storage source behind a library location.
func (r *Repository) LocationSource(ctx context.Context, locationID uuid.UUID) (Location, SourceConfig, error) {
	var location Location
	err := r.pool.QueryRow(ctx, `SELECT id,source_key,folder_id FROM library_storage_locations WHERE id=$1`, locationID).Scan(&location.ID, &location.SourceKey, &location.FolderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return location, SourceConfig{}, ErrSourceUnavailable
	}
	if err != nil {
		return location, SourceConfig{}, err
	}
	source, err := r.Source(ctx, location.SourceKey)
	return location, source, err
}
