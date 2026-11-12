package downloads

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// NativeOriginals serves native-storage and `.strm` videos as original-file
// downloads. Their catalog path is an opaque reference, never a local file, so
// the bytes come from the native reader under the requesting viewer's
// authority, as for playback.
type NativeOriginals interface {
	// DownloadFile reports whether file can be downloaded as an original and
	// returns the copy downloads use: its size is what the reader serves.
	DownloadFile(ctx context.Context, file *models.MediaFile) (*models.MediaFile, bool)
	// OpenOriginal opens file for the current request. The reader rechecks
	// the viewer, the library and the source around every read.
	OpenOriginal(ctx context.Context, r *http.Request, file *models.MediaFile) (NativeOriginal, error)
}

// NativeOriginal is one opened native original.
type NativeOriginal interface {
	io.ReadSeeker
	io.Closer
	ModTime() time.Time
	// ETag is the strong validator of the pinned revision.
	ETag() string
	ContentType() string
	// Extension names the container, with its dot, for the saved file name.
	Extension() string
	// Err reports a source or authority failure seen while reading.
	Err() error
}

// SetNativeOriginals lets the native-storage guard admit the native videos
// originals can serve. Without it every native row stays hidden. Only
// original downloads are served: preparing a converted copy reads the source
// with ffmpeg on the API server or a worker, which native sources do not
// support yet, so the quality resolver never offers one (see
// bloemArtifactsAvailable).
func (s *Service) SetNativeOriginals(native NativeOriginals) {
	if s == nil || native == nil {
		return
	}
	if guard, ok := s.fileRepo.(*nativeStorageFiles); ok {
		guard.native.Store(&nativeOriginalsBox{native})
	}
}

func (s *Service) nativeOriginals() NativeOriginals {
	if s == nil {
		return nil
	}
	if guard, ok := s.fileRepo.(*nativeStorageFiles); ok {
		return guard.originals()
	}
	return nil
}

// NativeOriginalsReady reports whether native originals are served.
func (s *Service) NativeOriginalsReady() bool { return s.nativeOriginals() != nil }

// bloemArtifactsAvailable keeps prepared (remuxed or converted) copies away
// from native files: a native original downloads as it is or not at all.
func bloemArtifactsAvailable(file *models.MediaFile, available bool) bool {
	return available && (file == nil || !storagesource.IsNativeLocation(file.FilePath))
}

// bloemServeNativeOriginal serves a native target through the native reader.
// It reports false for every other target, which Silo's path serves.
func (s *Service) bloemServeNativeOriginal(ctx context.Context, w http.ResponseWriter, r *http.Request, target *FileTarget, userID int) (bool, error) {
	if target == nil || target.OriginArtifactID != "" || target.ArtifactID != "" || !storagesource.IsNativeLocation(target.Path) {
		return false, nil
	}
	native := s.nativeOriginals()
	if native == nil {
		return true, catalog.ErrItemNotFound
	}
	// The guarded resolver admits only what native can serve.
	file, err := s.fileRepo.GetByID(ctx, target.MediaFileID)
	if err != nil {
		return true, translateFileLookupError(err)
	}
	if file == nil || file.ID != target.MediaFileID || file.FilePath != target.Path || file.MissingSince != nil {
		return true, catalog.ErrItemNotFound
	}
	opened, err := native.OpenOriginal(ctx, r, file)
	if err != nil {
		return true, err
	}
	defer func() { _ = opened.Close() }()

	// Never name the saved file after the opaque reference or the .strm text.
	name := s.nativeAttachmentName(ctx, target, opened.Extension())
	w.Header().Set("Content-Disposition", attachmentDisposition(name))
	w.Header().Set("Content-Type", opened.ContentType())
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if etag := opened.ETag(); etag != "" {
		w.Header().Set("ETag", etag)
	}
	var reader io.ReadSeeker = opened
	if s.bandwidth != nil {
		reader = s.bandwidth.ThrottledReader(ctx, opened, userID)
	}
	observed := &observedDownloadReader{ReadSeeker: reader}
	response := &observedDownloadResponse{ResponseWriter: w}
	http.ServeContent(response, r.WithContext(ctx), name, opened.ModTime(), observed)
	if transferErr := errors.Join(observed.readError(), response.err, opened.Err()); transferErr != nil {
		return true, fmt.Errorf("%w: serving native download: %w", ErrResponseCommitted, transferErr)
	}
	if r.Method != http.MethodHead && (response.status == http.StatusOK || response.status == http.StatusPartialContent) && response.expected >= 0 && response.written < response.expected {
		return true, fmt.Errorf("%w: serving native download: %w", ErrResponseCommitted, io.ErrUnexpectedEOF)
	}
	return true, nil
}

// nativeAttachmentName is AttachmentName for a native file: the title-based
// name with the container's extension, also for callers who may see file
// names, since the catalog path names no file.
func (s *Service) nativeAttachmentName(ctx context.Context, target *FileTarget, ext string) string {
	title := s.downloadTitle(ctx, target.MediaFileID)
	if title == "" {
		title = "Download"
	}
	return sanitizeFilename(title) + ext
}
