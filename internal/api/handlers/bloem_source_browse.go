package handlers

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
)

func nativeSourceBrowseRequest(r *http.Request) (nativestorage.BrowseRequest, error) {
	if len(r.URL.RawQuery) > nativestorage.MaxBrowseQueryBytes {
		return nativestorage.BrowseRequest{}, &catalog.NativeOnboardingError{Code: "browse_limit_exceeded"}
	}
	values, err := nativeStorageQuery(r, "directory_ref", "cursor", "limit")
	if err != nil {
		return nativestorage.BrowseRequest{}, err
	}
	if err := nativeStorageNoBody(r); err != nil {
		return nativestorage.BrowseRequest{}, err
	}
	limit := 50
	if raw := values.Get("limit"); raw != "" {
		limit, err = nativeStoragePositiveID(raw)
		if err != nil || limit > 100 {
			return nativestorage.BrowseRequest{}, nativeStorageInvalid()
		}
	}
	return nativestorage.BrowseRequest{DirectoryRef: values.Get("directory_ref"), Cursor: values.Get("cursor"), Limit: limit}, nil
}

// HandleBrowseSource is mounted by the owned library-location registrar behind
// the existing administrative context middleware. It never starts a scan.
func (h *BloemNativeStorageManagementHandler) HandleBrowseSource(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	request, err := nativeSourceBrowseRequest(r)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	key, ok := nativeStoragePathUUID(w, r, "source_key")
	if !ok {
		return
	}
	if h.Browser == nil {
		writeNativeStorageError(w, &catalog.NativeOnboardingError{Code: nativeStorageUnavailableCode}, true)
		return
	}
	page, err := h.Browser.Browse(r.Context(), actor, key, request)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	nativeStorageWrite(w, http.StatusOK, page)
}
