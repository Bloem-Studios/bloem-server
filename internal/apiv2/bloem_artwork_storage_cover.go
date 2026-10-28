package apiv2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
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
// (artworkkey.StorageCoverKey). The first authorized read keeps the cover in
// artwork storage under its revisioned key, and later authorized requests are
// served from there instead of the source.
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
	reg.serveStorageCover(w, r, key, contentID, revision)
	return true
}

// keptStorageCover returns a storage cover kept in artwork storage, or false
// when it is not kept yet or the store cannot read it.
func (reg *Registry) keptStorageCover(ctx context.Context, key string) ([]byte, bool) {
	if reg.deps.ArtworkStore == nil {
		return nil, false
	}
	reader, _, err := reg.deps.ArtworkStore.Get(ctx, key)
	if err != nil {
		return nil, false
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, nativestorage.MaxCoverBytes+1))
	if err != nil || len(data) > nativestorage.MaxCoverBytes || !strings.HasPrefix(http.DetectContentType(data), "image/") {
		return nil, false
	}
	return data, true
}

// serveStorageCover checks access before conditional responses and any read,
// so a revoked or hidden book's cover stops at once. A cover kept in artwork
// storage is then served from there. Otherwise it is read through its source,
// access is checked again after that read, and the cover is kept once both
// checks allow it.
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
	if data, kept := reg.keptStorageCover(r.Context(), key); kept {
		w.Header().Set("Content-Type", http.DetectContentType(data))
		http.ServeContent(w, r, path.Base(r.URL.Path), time.Time{}, bytes.NewReader(data))
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
