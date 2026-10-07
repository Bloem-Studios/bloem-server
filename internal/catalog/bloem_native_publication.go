package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanbatch"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type NativePublicationInput struct {
	Claim           storagesource.IngestionClaim
	FolderID        int
	ItemKey         string
	ExistingItemKey string
	ItemVersion     time.Time
	File            models.MediaFile
	Sidecars        []*storagev1.Entry
}

func nativePublicationUnavailable(cause error) error {
	return &NativeOnboardingError{Code: "native_storage_unavailable", Cause: cause}
}

// nativePublicationShape encodes the actual canonical repository outcomes,
// including nil/default fields. It contains no source or authorization facts.
func nativePublicationShape(ctx context.Context, input NativePublicationInput) ([]byte, error) {
	f := input.File
	e := input.Claim.Entry
	if e == nil || input.ItemKey == "" || f.ContentID != input.ItemKey || f.MediaFolderID != input.FolderID || f.EpisodeID != "" || f.ExtraID != "" ||
		f.Container != "epub" && f.Container != "pdf" || f.ProbeSource != "native" || f.FileSize != e.Size || f.FileModifiedAt == nil ||
		!f.FileModifiedAt.Equal(models.NormalizeFileModifiedAt(time.Unix(0, e.ModifiedUnixNano))) ||
		f.CanonicalRootPath != f.FilePath || f.ObservedRootPath != f.FilePath || f.BaseType != "ebook" ||
		f.GroupKeyVersion <= 0 || !strings.HasPrefix(f.ContentGroupKey, "bloem-native:"+input.Claim.Lease.BindingID.String()+":") ||
		f.SeasonNumber != 0 || f.EpisodeNumber != 0 || f.MissingSince != nil || f.ProbeUpdatedAt != nil || f.ProbeFailedAt != nil ||
		f.CodecVideo != "" || f.CodecAudio != "" || f.Resolution != "" || f.AudioChannels != 0 || f.HDR || f.Bitrate != 0 ||
		len(f.VideoTracks) != 0 || len(f.AudioTracks) != 0 || len(f.SubtitleTracks) != 0 || len(f.ExternalSubtitles) != 0 || len(f.Chapters) != 0 ||
		f.IntroStart != nil || f.IntroEnd != nil || f.CreditsStart != nil || f.CreditsEnd != nil || f.RecapStart != nil || f.RecapEnd != nil ||
		f.PreviewStart != nil || f.PreviewEnd != nil || f.MarkersSource != nil || f.MarkersConfidence != nil || f.FileHash != "" ||
		f.MultiplePPS != nil || f.MultiplePPSScanSize != nil || f.MultiplePPSScanMtime != nil ||
		f.EditionRaw != "" || f.EditionKey != "" || f.EditionConfidence != nil || f.EditionSource != "" ||
		f.PresentationKind != "" || f.PresentationGroupKey != "" || f.PresentationPartIndex != 0 || f.PresentationPartTotal != 0 ||
		f.MultiEpisodeStart != 0 || f.MultiEpisodeEnd != 0 || f.MatchAttemptedAt != nil || f.FirstSeenScanRunID != "" {
		return nil, nativePublicationUnavailable(nil)
	}
	confidence := f.IdentityConfidence
	if confidence == "" {
		confidence = "low"
	}
	identity := json.RawMessage(f.IdentityJSON)
	if len(identity) == 0 {
		identity = json.RawMessage("{}")
	}
	shape := map[string]any{
		"content_id": input.ItemKey, "episode_id": nil, "extra_id": nil, "media_folder_id": input.FolderID,
		"file_path": f.FilePath, "canonical_root_path": f.FilePath, "observed_root_path": f.FilePath,
		"content_group_key": f.ContentGroupKey, "group_key_version": f.GroupKeyVersion, "base_title": f.BaseTitle, "base_year": f.BaseYear,
		"base_type": f.BaseType, "identity_confidence": confidence, "identity_json": identity, "file_size": f.FileSize,
		"season_number": nil, "episode_number": nil, "file_hash": nil, "codec_video": nil, "codec_audio": nil, "resolution": nil, "audio_channels": nil,
		"hdr": false, "container": f.Container, "duration": nil, "bitrate": nil, "video_tracks": nil, "audio_tracks": nil,
		"subtitle_tracks": nil, "external_subtitles": nil, "chapters": nil, "intro_start": nil, "intro_end": nil, "credits_start": nil, "credits_end": nil,
		"markers_source": nil, "markers_confidence": nil, "edition_raw": "", "edition_key": "", "edition_confidence": nil, "edition_source": "",
		"presentation_kind": "", "presentation_group_key": "", "presentation_part_index": nil, "presentation_part_total": nil,
		"multi_episode_start": nil, "multi_episode_end": nil, "probe_source": "native", "probe_updated_at": nil, "missing_since": nil,
		"first_seen_scan_run_id": nil, "probe_failed_at": nil,
	}
	if runID := scanbatch.RunID(ctx); runID != "" {
		shape["first_seen_scan_run_id"] = runID
	}
	if f.Duration != 0 {
		shape["duration"] = f.Duration
	}
	return json.Marshal(shape)
}

// BeginNativePublicationPermitTx is SQL-only and runs after the repository's
// real authorizer/claim validation. Preparation alone cannot arm a permit.
func BeginNativePublicationPermitTx(ctx context.Context, tx pgx.Tx, input NativePublicationInput) error {
	c := input.Claim
	l := c.Lease
	e := c.Entry
	if tx == nil || e == nil || input.FolderID <= 0 || l.SourceKey == uuid.Nil || l.BindingID == uuid.Nil || l.RunID == uuid.Nil ||
		c.Token == uuid.Nil || l.Epoch <= 0 || l.ConfigurationRevision <= 0 || l.Owner == "" || e.Kind != storagev1.EntryKind_ENTRY_KIND_FILE ||
		e.Id == "" || e.Revision == "" || e.LogicalPath == "" || e.Size < 0 {
		return nativePublicationUnavailable(nil)
	}
	location, err := storagesource.CatalogLocation(l.BindingID, e.Id)
	if err != nil || location != input.File.FilePath {
		return nativePublicationUnavailable(err)
	}
	shape, err := nativePublicationShape(ctx, input)
	if err != nil {
		return err
	}
	var isolation string
	if err = tx.QueryRow(ctx, "SELECT current_setting('transaction_isolation')").Scan(&isolation); err != nil {
		return MapNativeOnboardingError(err)
	}
	if isolation != "read committed" {
		return nativePublicationUnavailable(nil)
	}
	limit := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < limit {
		limit = time.Until(deadline)
	}
	if limit <= 0 {
		return nativePublicationUnavailable(ctx.Err())
	}
	lockLimit := 250 * time.Millisecond
	if limit < lockLimit {
		lockLimit = limit
	}
	_, err = tx.Exec(ctx, "SELECT set_config('lock_timeout',$1,true),set_config('statement_timeout',$2,true)",
		strconv.FormatInt(max(1, lockLimit.Milliseconds()), 10)+"ms", strconv.FormatInt(max(1, limit.Milliseconds()), 10)+"ms")
	if err != nil {
		return MapNativeOnboardingError(err)
	}
	if !NativeStorageSchemaReady(ctx, tx) {
		return nativePublicationUnavailable(nil)
	}
	var installation int64
	if err = tx.QueryRow(ctx, "SELECT installation_id FROM bloem_storage_sources WHERE key=$1", l.SourceKey).Scan(&installation); err != nil {
		return MapNativeOnboardingError(err)
	}
	var sourceOwner, folderOwner uuid.UUID
	var generation, libraryRevision int64
	var protocol int
	// The authorizer already holds these locks. Reentrant NOWAIT acquisitions
	// validate the actual installed/source/folder/binding rows, never copied UUIDs.
	if err = tx.QueryRow(ctx, `SELECT i.owner_id,i.runtime_generation,n.protocol_version FROM plugin_installations i
 JOIN bloem_storage_installations n ON n.installation_id=i.id AND n.owner_id=i.owner_id
 WHERE i.id=$1 AND i.enabled AND i.kind='plugin' AND n.protocol_version=1 FOR SHARE OF i,n NOWAIT`,
		installation).Scan(&sourceOwner, &generation, &protocol); err != nil {
		return MapNativeOnboardingError(err)
	}
	if generation <= 0 {
		return nativePublicationUnavailable(nil)
	}
	var actualConfiguration int64
	if err = tx.QueryRow(ctx, `SELECT configuration_revision FROM bloem_storage_sources WHERE key=$1 AND owner_id=$2
 AND installation_id=$3 AND enabled AND discovery_run_id=$4 FOR UPDATE NOWAIT`,
		l.SourceKey, sourceOwner, installation, l.RunID).Scan(&actualConfiguration); err != nil {
		return MapNativeOnboardingError(err)
	}
	if actualConfiguration != l.ConfigurationRevision {
		return nativePublicationUnavailable(nil)
	}
	if err = tx.QueryRow(ctx, `SELECT f.owner_id,m.revision FROM media_folders f JOIN bloem_native_libraries m ON m.folder_id=f.id
 WHERE f.id=$1 AND f.owner_id=m.owner_id AND f.type='ebook' AND f.enabled AND m.initialized AND m.revision=3
 AND m.deleting_job_id IS NULL AND NOT EXISTS(SELECT 1 FROM media_folder_paths WHERE media_folder_id=f.id)
 FOR UPDATE OF f,m NOWAIT`, input.FolderID).Scan(&folderOwner, &libraryRevision); err != nil {
		return MapNativeOnboardingError(err)
	}
	var binding uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM bloem_storage_bindings WHERE id=$1 AND source_key=$2 AND folder_id=$3 FOR UPDATE NOWAIT`,
		l.BindingID, l.SourceKey, input.FolderID).Scan(&binding); err != nil {
		return MapNativeOnboardingError(err)
	}
	rows, err := tx.Query(ctx, `SELECT id FROM resource_owners WHERE id IN($1,$2) ORDER BY id FOR SHARE NOWAIT`, sourceOwner, folderOwner)
	if err != nil {
		return MapNativeOnboardingError(err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return MapNativeOnboardingError(err)
	}
	rows, err = tx.Query(ctx, `SELECT o.id FROM organizations o JOIN resource_owners r ON r.organization_id=o.id
 WHERE r.id IN($1,$2) ORDER BY o.id FOR SHARE OF o NOWAIT`, sourceOwner, folderOwner)
	if err != nil {
		return MapNativeOnboardingError(err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return MapNativeOnboardingError(err)
	}
	rows, err = tx.Query(ctx, `SELECT e.id FROM organization_entitlements e JOIN resource_owners r ON r.organization_id=e.organization_id
 WHERE r.id=$1 AND e.root_owner_id=$2 AND e.root_kind='plugin_installation' AND e.entitlement_kind='plugin_availability'
 AND e.plugin_installation_id=$3 AND e.status='active' ORDER BY e.id FOR SHARE OF e NOWAIT`, folderOwner, sourceOwner, installation)
	if err != nil {
		return MapNativeOnboardingError(err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return MapNativeOnboardingError(err)
	}
	if _, err = tx.Exec(ctx, "SELECT bloem_native_lock_item_keys($1::text[])", []string{input.ItemKey}); err != nil {
		return MapNativeOnboardingError(err)
	}
	var existing string
	var version time.Time
	err = tx.QueryRow(ctx, "SELECT content_id,updated_at FROM media_items WHERE content_id=$1 FOR UPDATE NOWAIT", input.ItemKey).Scan(&existing, &version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return MapNativeOnboardingError(err)
	}
	if err == nil {
		var class string
		if err = tx.QueryRow(ctx, "SELECT bloem_native_item_class($1)", input.ItemKey).Scan(&class); err != nil {
			return MapNativeOnboardingError(err)
		}
		if class != "native" || input.ExistingItemKey != existing || !input.ItemVersion.Equal(version) {
			return nativePublicationUnavailable(nil)
		}
	} else if input.ExistingItemKey != "" || !input.ItemVersion.IsZero() {
		return nativePublicationUnavailable(nil)
	}
	var invalidEvidence bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_files f LEFT JOIN bloem_storage_file_refs r ON r.media_file_id=f.id
 WHERE (f.content_id=$1 OR f.episode_id=$1 OR f.extra_id=$1) AND
 (f.media_folder_id<>$2 OR f.missing_since IS NOT NULL OR r.binding_id IS DISTINCT FROM $3 OR f.content_id IS DISTINCT FROM $1
 OR f.episode_id IS NOT NULL OR f.extra_id IS NOT NULL))
 OR EXISTS(SELECT 1 FROM media_item_libraries ml WHERE ml.content_id=$1 AND (ml.media_folder_id<>$2 OR NOT EXISTS(
 SELECT 1 FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE f.content_id=$1 AND f.media_folder_id=$2 AND r.binding_id=$3)))
 OR EXISTS(SELECT 1 FROM media_extras WHERE content_id=$1 OR parent_id=$1)
 OR EXISTS(SELECT 1 FROM user_dropped_series WHERE series_id=$1)
 OR EXISTS(SELECT 1 FROM watch_provider_dropped_items WHERE series_id=$1)
 OR EXISTS(SELECT 1 FROM seasons WHERE content_id=$1 OR series_id=$1)
 OR EXISTS(SELECT 1 FROM episodes WHERE content_id=$1 OR series_id=$1 OR season_id=$1)
 OR EXISTS(SELECT 1 FROM media_item_roots WHERE content_id=$1)
 OR EXISTS(SELECT 1 FROM media_item_groups WHERE content_id=$1)`, input.ItemKey, input.FolderID, l.BindingID).Scan(&invalidEvidence)
	if err != nil {
		return MapNativeOnboardingError(err)
	}
	if invalidEvidence {
		return nativePublicationUnavailable(nil)
	}
	var retained *int64
	var fileID int64
	var firstSeen *string
	err = tx.QueryRow(ctx, `SELECT f.id,f.first_seen_scan_run_id FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id
 WHERE f.file_path=$1 AND f.content_id=$2 AND f.media_folder_id=$3 AND f.episode_id IS NULL AND f.extra_id IS NULL
 AND f.missing_since IS NULL AND r.binding_id=$4 AND r.entry_id=$5 FOR UPDATE OF f,r NOWAIT`,
		location, input.ItemKey, input.FolderID, l.BindingID, e.Id).Scan(&fileID, &firstSeen)
	if err == nil {
		retained = &fileID
		// Canonical conflict outcomes retain scan provenance: the real repository
		// does not assign first_seen_scan_run_id in its conflict UPDATE.
		var canonical map[string]any
		if err = json.Unmarshal(shape, &canonical); err != nil {
			return nativePublicationUnavailable(err)
		}
		canonical["first_seen_scan_run_id"] = firstSeen
		shape, err = json.Marshal(canonical)
		if err != nil {
			return nativePublicationUnavailable(err)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return MapNativeOnboardingError(err)
	}
	var conflict bool
	if retained == nil {
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM media_files WHERE file_path=$1)", location).Scan(&conflict); err != nil {
			return MapNativeOnboardingError(err)
		}
		if conflict {
			return nativePublicationUnavailable(nil)
		}
	}
	sidecars := append([]*storagev1.Entry(nil), input.Sidecars...)
	for _, sidecar := range sidecars {
		if sidecar == nil {
			return nativePublicationUnavailable(nil)
		}
	}
	sort.Slice(sidecars, func(i, j int) bool { return sidecars[i].Id < sidecars[j].Id })
	tuples := make([]map[string]any, 0, len(sidecars))
	for i, s := range sidecars {
		if s.Id == "" || s.Revision == "" || s.Id == e.Id || i > 0 && sidecars[i-1].Id == s.Id {
			return nativePublicationUnavailable(nil)
		}
		tuples = append(tuples, map[string]any{"id": s.Id, "revision": s.Revision, "logical_path": s.LogicalPath, "kind": s.Kind, "size": s.Size, "modified_unix_nano": s.ModifiedUnixNano})
	}
	sidecarShape, err := json.Marshal(tuples)
	if err != nil {
		return nativePublicationUnavailable(err)
	}
	var previous any
	var previousVersion any
	if input.ExistingItemKey != "" {
		previous = input.ExistingItemKey
		previousVersion = input.ItemVersion
	}
	_, err = tx.Exec(ctx, `INSERT INTO bloem_native_publication_permits(xid,item_key,existing_item_key,item_version,folder_key,library_revision,
 folder_owner_id,source_key,source_owner_id,binding_id,discovery_run_id,installation_key,runtime_generation,configuration_revision,
 protocol_version,ingestion_owner,ingestion_epoch,lease_token,entry_id,entry_revision,logical_path,catalog_location,entry_kind,entry_size,
 entry_modified_unix_nano,normalized_modified_at,parsed_container,content_group_key,file_shape,sidecar_shape,retained_file_key)
 VALUES(txid_current(),$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30)`,
		input.ItemKey, previous, previousVersion, input.FolderID, libraryRevision, folderOwner, l.SourceKey, sourceOwner, l.BindingID, l.RunID,
		installation, generation, l.ConfigurationRevision, protocol, l.Owner, l.Epoch, c.Token, e.Id, e.Revision, e.LogicalPath, location, e.Kind, e.Size,
		e.ModifiedUnixNano, *input.File.FileModifiedAt, input.File.Container, input.File.ContentGroupKey, shape, sidecarShape, retained)
	return MapNativeOnboardingError(err)
}

func RecordNativePublicationFileTx(ctx context.Context, tx pgx.Tx, id int) error {
	if tx == nil || id <= 0 {
		return nativePublicationUnavailable(nil)
	}
	tag, err := tx.Exec(ctx, `UPDATE bloem_native_publication_permits SET stored_file_key=$1
 WHERE xid=txid_current() AND phase='file' AND stored_file_key IS NULL AND $1=COALESCE(retained_file_key,attempted_file_key)`, id)
	if err != nil {
		return MapNativeOnboardingError(err)
	}
	if tag.RowsAffected() != 1 {
		return nativePublicationUnavailable(nil)
	}
	return nil
}
func NativePublicationStoredFileTx(ctx context.Context, tx pgx.Tx) (int, error) {
	if tx == nil {
		return 0, nativePublicationUnavailable(nil)
	}
	var id int
	err := tx.QueryRow(ctx, `SELECT f.id FROM bloem_native_publication_permits p JOIN media_files f ON f.id=p.stored_file_key
 JOIN bloem_storage_file_refs r ON r.media_file_id=f.id
 WHERE p.xid=txid_current() AND p.phase='ref' AND p.ref_checked
 AND f.file_path=p.catalog_location AND f.content_id=p.item_key AND f.media_folder_id=p.folder_key
 AND r.binding_id=p.binding_id AND r.entry_id=p.entry_id AND r.revision=p.entry_revision
 AND r.logical_path=p.logical_path AND r.configuration_revision=p.configuration_revision`).Scan(&id)
	if err != nil {
		return 0, MapNativeOnboardingError(err)
	}
	return id, nil
}
func FinishNativePublicationPermitTx(ctx context.Context, tx pgx.Tx, id int) error {
	if tx == nil || id <= 0 {
		return nativePublicationUnavailable(nil)
	}
	tag, err := tx.Exec(ctx, `UPDATE bloem_native_publication_permits SET phase='finished',finished=true
 WHERE xid=txid_current() AND stored_file_key=$1 AND phase='ref' AND item_checked AND member_checked AND ref_checked`, id)
	if err != nil {
		return MapNativeOnboardingError(err)
	}
	if tag.RowsAffected() != 1 {
		return nativePublicationUnavailable(nil)
	}
	tag, err = tx.Exec(ctx, "DELETE FROM bloem_native_publication_permits WHERE xid=txid_current() AND finished AND phase='finished'")
	if err != nil {
		return MapNativeOnboardingError(err)
	}
	if tag.RowsAffected() != 1 {
		return nativePublicationUnavailable(nil)
	}
	return nil
}
