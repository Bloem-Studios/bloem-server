package apiv2

import (
	"bytes"
	"context"
	"errors"
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
// (artworkkey.StorageCoverKey). Those covers are never in artwork storage.
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
	reg.serveStorageCover(w, r, contentID, revision, exp)
	return true
}

// serveStorageCover checks access before conditional responses and cached reads,
// then again after the read. Native artwork must revalidate after revocation.
func (reg *Registry) serveStorageCover(w http.ResponseWriter, r *http.Request, contentID, revision string, _ int64) {
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
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, path.Base(r.URL.Path), time.Time{}, bytes.NewReader(data))
}
