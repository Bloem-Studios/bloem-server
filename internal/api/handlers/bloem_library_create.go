package handlers

import (
	"context"
	"net/http"
	"path/filepath"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/librarykind"
)

// LibraryCreateAttachment extends the ordinary creation transaction.
type LibraryCreateAttachment func(context.Context, pgx.Tx, int) error

type bloemLibraryAttachmentKey struct{}

// CreateLibraryWithAttachment shares Silo's validation, defaults and follow-up
// work. The explicit attachment owns any non-filesystem locations.
func (h *LibraryHandler) CreateLibraryWithAttachment(ctx context.Context, req LibraryCreateRequest, attach LibraryCreateAttachment) (LibraryView, error) {
	if req.StorageSource != nil {
		return LibraryView{}, fieldError("storage_source", "An explicit attachment cannot use legacy storage_source")
	}
	if attach == nil {
		return LibraryView{}, fieldError("paths", "An explicit library attachment is required")
	}
	return h.CreateLibrary(context.WithValue(ctx, bloemLibraryAttachmentKey{}, attach), req)
}

// bloemLibraryAttachment is Bloem's part of CreateLibrary: the storage
// location a new library is attached to inside Silo's creation transaction,
// and whether its caller receives the typed attachment errors (explicit, from
// CreateLibraryWithAttachment) or Silo's logged internal error (the v2
// createLibrary operation's legacy storage_source). A library with no paths
// is marked so normalizeNewLibrary does not require them.
func (h *LibraryHandler) bloemLibraryAttachment(ctx context.Context, req *LibraryCreateRequest) (LibraryCreateAttachment, bool, error) {
	if attach, ok := ctx.Value(bloemLibraryAttachmentKey{}).(LibraryCreateAttachment); ok && attach != nil {
		req.bloemPathless = len(req.Paths) == 0
		return attach, true, nil
	}
	if req.StorageSource == nil {
		return nil, false, nil
	}
	if req.Type == "" || req.Name == "" {
		return nil, false, apiError(http.StatusBadRequest, "bad_request", "Paths, type, and name are required")
	}
	// A library reads either filesystem paths or one storage source.
	if len(req.Paths) != 0 {
		return nil, false, fieldError("storage_source", "A library uses either paths or a storage source, not both")
	}
	// Attachment checks actual movie readiness and source authority in tx.
	if !librarykind.SupportsNativeStorage(req.Type, true) {
		return nil, false, fieldError("storage_source", "Storage sources support ebook and movie libraries only")
	}
	if h.StorageLocations == nil {
		return nil, false, apiError(http.StatusServiceUnavailable, "storage_unavailable", "Storage sources are not available on this server")
	}
	source := *req.StorageSource
	req.bloemPathless = true
	return func(ctx context.Context, tx pgx.Tx, folderID int) error {
		return h.StorageLocations.AttachTx(ctx, tx, source, folderID)
	}, false, nil
}

// bloemNormalizePathlessLibrary applies Silo's checks for a new library
// (normalizeNewLibrary) to one whose locations are storage sources only. It
// runs them on a copy that carries a placeholder path, so every check but the
// paths requirement stays Silo's, then keeps the library's own empty paths.
func (h *LibraryHandler) bloemNormalizePathlessLibrary(req *LibraryCreateRequest) error {
	probe := *req
	probe.bloemPathless = false
	probe.Paths = []string{string(filepath.Separator)}
	if err := h.normalizeNewLibrary(&probe); err != nil {
		return err
	}
	probe.Paths = req.Paths
	probe.bloemPathless = true
	*req = probe
	return nil
}
