package apiv2

import (
	"bytes"
	"context"
	"errors"
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
// (artworkkey.StorageCoverKey). Those covers are never in artwork storage.
type StorageCoverService interface {
	ReadCover(ctx context.Context, contentID, revision string) ([]byte, error)
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
		return false
	}
	reg.serveStorageCover(w, r, contentID, revision, exp)
	return true
}

// serveStorageCover reads a book cover through its storage plugin. The key's
// revision names the cover's bytes, so it is the ETag and the response is
// cached like any revisioned artwork; a revalidation never reaches the plugin.
func (reg *Registry) serveStorageCover(w http.ResponseWriter, r *http.Request, contentID, revision string, exp int64) {
	etag := `"` + revision + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age="+strconv.FormatInt(max(exp-time.Now().Unix(), 0), 10)+", immutable")
	if r.Header.Get(ifNoneMatchField) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	notFound := func() {
		w.Header().Del("ETag")
		w.Header().Del("Cache-Control")
		writeProblem(w, r, NewProblem(TypeNotFound, "Artwork not found."))
	}
	if reg.deps.StorageCovers == nil {
		notFound()
		return
	}
	data, err := reg.deps.StorageCovers.ReadCover(r.Context(), contentID, revision)
	if err != nil {
		// A changed, removed or oversized cover, or a source that can no
		// longer serve the library, is simply absent: clients fall back to
		// the placeholder.
		if code := status.Code(err); code == codes.NotFound || code == codes.FailedPrecondition ||
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
	contentType := http.DetectContentType(data)
	if !strings.HasPrefix(contentType, "image/") {
		notFound()
		return
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, path.Base(r.URL.Path), time.Time{}, bytes.NewReader(data))
}
