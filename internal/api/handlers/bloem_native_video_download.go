package handlers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// NativeVideoDownloads serves native-storage and `.strm` videos as original
// downloads (downloads.NativeOriginals). Open opens a file under the current
// request's download authority, which the reader rechecks around every read,
// as for direct play. Size returns the bytes a download serves when the
// catalog size is not that, as for a `.strm` item, whose catalog size is the
// text file's.
type NativeVideoDownloads struct {
	Service *NativeVideoService
	Open    func(context.Context, *models.MediaFile) (NativeVideoFile, error)
	Size    func(context.Context, *models.MediaFile) (int64, bool, error)
}

var _ downloads.NativeOriginals = (*NativeVideoDownloads)(nil)

// DownloadFile admits native video (not native audio, which keeps its own
// presentation path) and returns a copy carrying the served size.
func (d *NativeVideoDownloads) DownloadFile(ctx context.Context, file *models.MediaFile) (*models.MediaFile, bool) {
	if d == nil || d.Service == nil || d.Open == nil || !nativeVideoFileSupported(file) {
		return nil, false
	}
	view := *file
	if d.Size != nil {
		size, ok, err := d.Size(ctx, file)
		if err != nil {
			return nil, false
		}
		if ok {
			view.FileSize = size
		}
	}
	return &view, true
}

// OpenOriginal opens file through the native reader. Like direct play, it
// reads ahead in 1 MiB blocks and copies bytes out only while the latest
// fresh authority check is less than a second old.
func (d *NativeVideoDownloads) OpenOriginal(ctx context.Context, r *http.Request, file *models.MediaFile) (downloads.NativeOriginal, error) {
	if d == nil || d.Service == nil || d.Open == nil || !nativeVideoFileSupported(file) {
		return nil, catalog.ErrItemNotFound
	}
	opened, binding, err := d.Service.openWith(ctx, file, d.Open)
	if err != nil {
		return nil, nativeVideoDownloadError(err)
	}
	reader, err := nativestorage.NewReadAhead(ctx, opened, nativeVideoDirectReadAhead)
	if err != nil {
		_ = opened.Close()
		return nil, err
	}
	// This check also opens the first authorization window for the reads.
	if err := reader.Reauthorize(ctx); err != nil {
		_ = reader.Close()
		return nil, nativeVideoDownloadError(err)
	}
	if r != nil && r.Header.Get("If-Range") == "" {
		reader.SetRangeEnd(nativestorage.RangeEnd(r.Header.Get("Range"), binding.Info.Size))
	}
	return &nativeVideoDownload{ReadAhead: reader, opened: opened, binding: binding, container: file.Container}, nil
}

type nativeVideoDownload struct {
	*nativestorage.ReadAhead
	opened    NativeVideoFile
	binding   NativeVideoBinding
	container string
}

func (d *nativeVideoDownload) ModTime() time.Time  { return d.binding.Info.ModifiedAt }
func (d *nativeVideoDownload) ETag() string        { return nativeVideoEntityTag(d.binding) }
func (d *nativeVideoDownload) ContentType() string { return nativeVideoContentType(d.container) }
func (d *nativeVideoDownload) Extension() string   { return nativeVideoExtension(d.container) }
func (d *nativeVideoDownload) Err() error          { return errors.Join(d.ReadAhead.Err(), d.opened.Err()) }

// nativeVideoEntityTag is the strong validator native direct play sends, so a
// download resumed with If-Range matches the same revision.
func nativeVideoEntityTag(binding NativeVideoBinding) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", binding.Identity.FileID, binding.Info.Size, binding.Info.Revision)))
	return fmt.Sprintf("\"%x\"", digest)
}

// nativeVideoExtension is the saved file's extension for an admitted container.
func nativeVideoExtension(container string) string {
	c := strings.ToLower(strings.TrimSpace(container))
	switch c {
	case "matroska":
		return ".mkv"
	case "mpegts":
		return ".ts"
	case "mpeg":
		return ".mpg"
	}
	if nativeVideoFormat(c) == "" {
		return ""
	}
	return "." + c
}

// nativeVideoDownloadError maps an open failure onto the download errors every
// download surface already answers: not found, or not allowed (never 401, which
// signs a Jellyfin client out).
func nativeVideoDownloadError(err error) error {
	switch {
	case errors.Is(err, catalog.ErrItemNotFound), errors.Is(err, storagesource.ErrReferenceConflict):
		return fmt.Errorf("%w: %w", catalog.ErrItemNotFound, err)
	case errors.Is(err, middleware.ErrCurrentCredentialInvalid), errors.Is(err, middleware.ErrCurrentCredentialForbidden):
		return fmt.Errorf("%w: %w", downloads.ErrDownloadNotAllowed, err)
	default:
		return err
	}
}
