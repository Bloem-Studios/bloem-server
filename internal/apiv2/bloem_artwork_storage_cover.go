package apiv2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/artworkurl"
	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StorageCoverService reads a book cover its storage source serves on demand
// (artworkkey.StorageCoverKey). The first read keeps the cover in artwork
// storage under its revisioned key, and later requests are served from there.
type StorageCoverService interface {
	ReadCover(ctx context.Context, contentID, revision string) ([]byte, error)
}

// AuthorizedStorageCovers checks current viewer and selected-root authority.
// Revisioned bytes may be cached; authority must be checked for each request.
type AuthorizedStorageCovers interface {
	StorageCoverService
	AuthorizeCoverRequest(context.Context, *http.Request, string, string) error
}

// NewBloemArtworkHandler is NewArtworkHandler for listeners that also serve
// storage covers.
func NewBloemArtworkHandler(store blobstore.Store, signer *artworkurl.Signer, repair ArtworkRepairService, covers StorageCoverService) http.Handler {
	deps := Dependencies{ArtworkStore: store, ArtworkSigner: signer, ArtworkRepair: repair}
	deps.StorageCovers = covers
	reg := &Registry{deps: deps}
	return http.HandlerFunc(reg.serveArtwork)
}

// serveBloemStorageCover serves key when it names a storage cover, after the
// artwork route has verified its signature. It reports whether it answered.
func (reg *Registry) serveBloemStorageCover(w http.ResponseWriter, r *http.Request, key string, exp int64) bool {
	contentID, revision, ok := artworkkey.ParseStorageCoverKey(key)
	if !ok {
		if strings.HasPrefix(key, "storage-covers/") {
			writeProblem(w, r, NewProblem(TypeNotFound, "Artwork not found."))
			return true
		}
		return false
	}
	if !reg.serveStoredStorageCover(w, r, key, revision, exp) {
		reg.serveStorageCover(w, r, key, contentID, revision)
	}
	return true
}

// serveStoredStorageCover serves a storage cover kept in artwork storage, as
// other revisioned artwork is: its signed URL is the authority, and neither
// the source nor the viewer's policy is consulted again. It reports whether
// it answered; a cover not yet kept, or a store that cannot read it, is read
// through its source instead.
func (reg *Registry) serveStoredStorageCover(w http.ResponseWriter, r *http.Request, key, revision string, exp int64) bool {
	if reg.deps.ArtworkStore == nil {
		return false
	}
	reader, _, err := reg.deps.ArtworkStore.Get(r.Context(), key)
	if err != nil {
		return false
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, nativestorage.MaxCoverBytes+1))
	if err != nil || len(data) > nativestorage.MaxCoverBytes {
		return false
	}
	contentType := http.DetectContentType(data)
	if !strings.HasPrefix(contentType, "image/") {
		return false
	}
	etag := `"` + revision + `"`
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age="+strconv.FormatInt(max(exp-time.Now().Unix(), 0), 10)+", immutable")
	if r.Header.Get(ifNoneMatchField) == etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	http.ServeContent(w, r, path.Base(r.URL.Path), time.Time{}, bytes.NewReader(data))
	return true
}

// serveStorageCover reads a cover not yet kept in artwork storage through its
// source. It checks access before conditional responses and the read, then
// again after the read, and keeps the cover once both allow it.
func (reg *Registry) serveStorageCover(w http.ResponseWriter, r *http.Request, key, contentID, revision string) {
	notFound := func() {
		w.Header().Del("ETag")
		w.Header().Del("Cache-Control")
		writeProblem(w, r, NewProblem(TypeNotFound, "Artwork not found."))
	}
	covers, ok := reg.deps.StorageCovers.(AuthorizedStorageCovers)
	if !ok || covers.AuthorizeCoverRequest(r.Context(), r, contentID, revision) != nil {
		notFound()
		return
	}
	etag := `"` + revision + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get(ifNoneMatchField) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	data, err := covers.ReadCover(r.Context(), contentID, revision)
	if err != nil {
		// A changed, removed or oversized cover, or a source that can no
		// longer serve the library, is simply absent: clients fall back to
		// the placeholder.
		if code := status.Code(err); code == codes.NotFound || code == codes.FailedPrecondition || code == codes.PermissionDenied || code == codes.Unauthenticated ||
			errors.Is(err, storagesource.ErrReferenceConflict) || errors.Is(err, storagesource.ErrSourceUnavailable) ||
			errors.Is(err, resourcetenancy.ErrResourceHidden) || errors.Is(err, nativestorage.ErrCoverTooLarge) {
			notFound()
			return
		}
		w.Header().Del("ETag")
		w.Header().Del("Cache-Control")
		writeProblem(w, r, unavailable("storage cover"))
		return
	}
	if err := covers.AuthorizeCoverRequest(r.Context(), r, contentID, revision); err != nil {
		notFound()
		return
	}
	contentType := http.DetectContentType(data)
	if !strings.HasPrefix(contentType, "image/") {
		notFound()
		return
	}
	if reg.deps.ArtworkStore != nil {
		// A cover not kept only costs the next request another source read.
		if err := reg.deps.ArtworkStore.Put(r.Context(), key, data); err != nil {
			slog.WarnContext(r.Context(), "storage cover not kept in artwork storage", "component", "apiv2", "content_id", contentID, "error", err)
		}
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, path.Base(r.URL.Path), time.Time{}, bytes.NewReader(data))
}
