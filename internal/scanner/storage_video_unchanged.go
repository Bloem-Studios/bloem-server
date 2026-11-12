package scanner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// ErrStorageVideoChanged reports that a video listed as unchanged, and so not
// probed, was no longer current when its page was published. The page is not
// committed; the scan's retry lists it again and probes the file.
var ErrStorageVideoChanged = errors.New("unprobed storage video changed before publication")

// storageVideoPublication versions how a listed video entry becomes catalog
// rows. Bump it when that changes, so the next listing probes and republishes
// every native movie and episode.
const storageVideoPublication = "bloem.storage.video.publication.v1"

// storageVideoDigest identifies everything a native video is published from:
// the listed entry (name, logical path, size, modification time) and its
// revision, which pins the bytes the probe read.
func storageVideoDigest(e *storagev1.Entry) []byte {
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(e)
	if err != nil {
		return nil
	}
	h := sha256.New()
	h.Write([]byte(storageVideoPublication))
	h.Write([]byte{0})
	h.Write(raw)
	return h.Sum(nil)
}

type storageQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// StorageVideosUnchanged reports the listed video entries whose published file
// is current: the same entry, revision and listed digest under this source
// configuration, present, and still a member of the library through its item.
// Such a file needs neither a probe nor republication. The answer is a hint
// for the scan; publication checks it again inside the page transaction.
func (s *Scanner) StorageVideosUnchanged(ctx context.Context, folder *models.MediaFolder, location storagesource.Location, configurationRevision int64, entries []*storagev1.Entry) (map[string]bool, error) {
	if s == nil || s.fileRepo == nil || s.fileRepo.Pool() == nil || folder == nil {
		return nil, nil
	}
	return storageVideosUnchanged(ctx, s.fileRepo.Pool(), folder.ID, location, configurationRevision, entries)
}

func storageVideosUnchanged(ctx context.Context, q storageQuerier, folderID int, location storagesource.Location, configurationRevision int64, entries []*storagev1.Entry) (map[string]bool, error) {
	if len(entries) == 0 || configurationRevision <= 0 {
		return nil, nil
	}
	ids := make([]string, len(entries))
	byID := make(map[string]*storagev1.Entry, len(entries))
	for i, e := range entries {
		ids[i], byID[e.GetId()] = e.GetId(), e
	}
	rows, err := q.Query(ctx, `SELECT r.entry_id,r.revision,r.entry_sha256 FROM bloem_storage_file_refs r JOIN media_files f ON f.id=r.media_file_id
		WHERE r.location_id=$1 AND r.entry_id=ANY($2::text[]) AND f.media_folder_id=$3 AND r.configuration_revision=$4
		AND r.entry_sha256 IS NOT NULL AND f.missing_since IS NULL AND f.extra_id IS NULL AND f.content_id IS NOT NULL
		AND EXISTS(SELECT 1 FROM media_item_libraries m WHERE m.content_id=f.content_id AND m.media_folder_id=f.media_folder_id)`,
		location.ID, ids, folderID, configurationRevision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var id, revision string
		var digest []byte
		if err := rows.Scan(&id, &revision, &digest); err != nil {
			return nil, err
		}
		if e := byID[id]; e != nil && e.GetRevision() == revision && bytes.Equal(digest, storageVideoDigest(e)) {
			out[id] = true
		}
	}
	return out, rows.Err()
}

// requireStorageVideosUnchangedTx confirms, inside the publication
// transaction, that every unprobed video is still current.
func requireStorageVideosUnchangedTx(ctx context.Context, tx pgx.Tx, folderID int, location storagesource.Location, configurationRevision int64, videos []StorageMovie) error {
	var unprobed []*storagev1.Entry
	for _, v := range videos {
		if v.Probe == nil {
			unprobed = append(unprobed, v.Entry)
		}
	}
	if len(unprobed) == 0 {
		return nil
	}
	current, err := storageVideosUnchanged(ctx, tx, folderID, location, configurationRevision, unprobed)
	if err != nil {
		return err
	}
	for _, e := range unprobed {
		if !current[e.GetId()] {
			return ErrStorageVideoChanged
		}
	}
	return nil
}

// UnprobedStorageVideo is a listed video the scan found unchanged. It carries
// no probe: publication counts it and keeps its catalog rows as they are.
func UnprobedStorageVideo(entry *storagev1.Entry) StorageMovie {
	return StorageMovie{Entry: proto.CloneOf(entry)}
}
