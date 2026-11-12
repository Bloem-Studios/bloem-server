package nativestorage

import (
	"context"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// SidecarReferences resolves a published file's subtitle sidecar as the
// series scan currently records it (storagesource.Repository.FileSidecar).
type SidecarReferences interface {
	FileSidecar(context.Context, int, uuid.UUID, string) (storagesource.Sidecar, error)
}

// sidecarPin is the sidecar entry and revision a read of a file's sidecar
// must find.
type sidecarPin struct {
	refs SidecarReferences
	ref  storagesource.PersistedRef
}

// check confirms the sidecar is still attached to the file, in the file's
// own location, at exactly the pinned entry, revision and path.
func (p *sidecarPin) check(ctx context.Context, fileID int, file storagesource.PersistedRef) error {
	if p.ref.LocationID != file.LocationID {
		return storagesource.ErrReferenceConflict
	}
	current, err := p.refs.FileSidecar(ctx, fileID, file.LocationID, p.ref.EntryID)
	if err != nil {
		return err
	}
	if current.MediaFileID != fileID || current.PersistedRef != p.ref {
		return storagesource.ErrReferenceConflict
	}
	return ctx.Err()
}

// OpenAuthorizedSidecar opens a subtitle sidecar of file, pinned to ref, with
// the file's own playback authority: the same viewer, file, publication and
// source checks as OpenAuthorizedPlayback, before admission and around every
// provider operation, plus the sidecar's current attachment at ref. The
// returned identity is the file's, with Reference naming the sidecar.
func (c *Coordinator) OpenAuthorizedSidecar(ctx context.Context, file *models.MediaFile, authority PlaybackAuthority, ref storagesource.PersistedRef) (*PlaybackFile, error) {
	if c == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	refs, ok := c.References.(SidecarReferences)
	if !ok || ref.LocationID == uuid.Nil || ref.EntryID == "" || ref.Revision == "" {
		return nil, storagesource.ErrSourceUnavailable
	}
	return c.openAuthorized(ctx, file, authority, &sidecarPin{refs: refs, ref: ref})
}
