package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/ambience"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/branding"
)

// ambienceRegistry is the seam the ambience handlers talk to; production is
// *ambience.Service, tests use a fake.
type ambienceRegistry interface {
	List(ctx context.Context) ([]ambience.Pack, error)
	Create(ctx context.Context, createdBy int, in ambience.Input) (*ambience.Pack, error)
	Update(ctx context.Context, id string, in ambience.Input) (*ambience.Pack, error)
	Delete(ctx context.Context, id string) error
	AttachAsset(ctx context.Context, packID, slot string, data []byte) (*ambience.Pack, string, error)
	StoreAsset(ctx context.Context, req ambience.StoreRequest) (*ambience.StoredAsset, error)
	ServeAsset(ctx context.Context, ref string) ([]byte, string, error)
	HasStorage() bool
}

// AmbienceHandler exposes the S-3 seasonal pack registry
// (docs/specs/client-engagement.md section C): admin CRUD + artwork attach
// under /admin/ambience, and the public artwork serving route.
type AmbienceHandler struct {
	registry ambienceRegistry
}

// NewAmbienceHandler creates the handler; nil-safe when no registry is wired.
func NewAmbienceHandler(svc *ambience.Service) *AmbienceHandler {
	h := &AmbienceHandler{}
	if svc != nil {
		h.registry = svc
	}
	return h
}

func writeAmbienceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ambience.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Ambience pack not found")
	case errors.Is(err, ambience.ErrInvalid):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, ambience.ErrInvalidSlot):
		writeError(w, http.StatusBadRequest, "bad_request", "slot must be banner or sprite")
	case errors.Is(err, ambience.ErrUnsupportedImage):
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_image", "Artwork must be a PNG, WebP, JPEG, or GIF image")
	case errors.Is(err, ambience.ErrStorageUnavailable):
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Asset upload storage (S3) is not configured")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Ambience operation failed")
	}
}

func (h *AmbienceHandler) unavailable(w http.ResponseWriter) bool {
	if h == nil || h.registry == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Ambience is not available")
		return true
	}
	return false
}

func decodeAmbienceInput(w http.ResponseWriter, r *http.Request) (ambience.Input, bool) {
	var in ambience.Input
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return in, false
	}
	return in, true
}

// HandleList handles GET /admin/ambience.
func (h *AmbienceHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w) {
		return
	}
	packs, err := h.registry.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list ambience packs")
		return
	}
	if packs == nil {
		packs = []ambience.Pack{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"packs": packs, "storage_available": h.registry.HasStorage(), "yearly_scheduling": true})
}

// HandleCreate handles POST /admin/ambience.
func (h *AmbienceHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w) {
		return
	}
	in, ok := decodeAmbienceInput(w, r)
	if !ok {
		return
	}
	created, err := h.registry.Create(r.Context(), apimw.GetUserID(r.Context()), in)
	if err != nil {
		writeAmbienceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// HandleUpdate handles PUT /admin/ambience/{id} (full replacement).
func (h *AmbienceHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w) {
		return
	}
	in, ok := decodeAmbienceInput(w, r)
	if !ok {
		return
	}
	updated, err := h.registry.Update(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		writeAmbienceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// HandleDelete handles DELETE /admin/ambience/{id}.
func (h *AmbienceHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w) {
		return
	}
	if err := h.registry.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAmbienceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readArtworkUpload caps the body, parses the multipart form and returns the
// `file` part bytes. Writes the error response itself on failure.
func readArtworkUpload(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	// ParseMultipartForm's argument is only the in-memory threshold; the
	// reader caps the body so oversized uploads never spool to disk.
	r.Body = http.MaxBytesReader(w, r.Body, ambience.MaxAssetBytes+(1<<20))
	if err := r.ParseMultipartForm(ambience.MaxAssetBytes + (1 << 20)); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "Artwork exceeds the maximum upload size")
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid multipart form")
		return nil, false
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Missing file field")
		return nil, false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, ambience.MaxAssetBytes+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to read upload")
		return nil, false
	}
	if int64(len(data)) > ambience.MaxAssetBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "Artwork exceeds the maximum upload size")
		return nil, false
	}
	return data, true
}

// HandleUploadAsset handles POST /admin/ambience/assets: a standalone
// multipart upload as sent by the authoring side (`file` part; text fields
// `asset_id`, `kind`, `checksum` = sha256 hex of the bytes, `content_type` —
// the bytes are sniffed, the declared type is ignored), stored under the
// public asset bucket and recorded by asset_id (idempotent on retry), not
// attached to any pack. Responds with the public URL at the top level and
// the stored asset under `asset`.
func (h *AmbienceHandler) HandleUploadAsset(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w) {
		return
	}
	if !h.registry.HasStorage() {
		writeAmbienceError(w, ambience.ErrStorageUnavailable)
		return
	}
	data, ok := readArtworkUpload(w, r)
	if !ok {
		return
	}
	if want := r.FormValue("checksum"); want != "" {
		sum := sha256.Sum256(data)
		if !strings.EqualFold(want, hex.EncodeToString(sum[:])) {
			writeError(w, http.StatusBadRequest, "bad_request", "checksum does not match the uploaded bytes")
			return
		}
	}
	stored, err := h.registry.StoreAsset(r.Context(), ambience.StoreRequest{
		AssetID: strings.TrimSpace(r.FormValue("asset_id")),
		Kind:    strings.TrimSpace(r.FormValue("kind")),
		Data:    data,
	})
	if err != nil {
		writeAmbienceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"url": stored.URL, "asset": stored})
}

// HandleAttachAsset handles POST /admin/ambience/{id}/assets: a multipart
// upload (`file` field, `slot` field = banner|sprite, default banner) stored
// under the public asset bucket and attached to the pack. Responds with the
// updated pack and the public URL of the stored artwork.
func (h *AmbienceHandler) HandleAttachAsset(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w) {
		return
	}
	if !h.registry.HasStorage() {
		writeAmbienceError(w, ambience.ErrStorageUnavailable)
		return
	}
	data, ok := readArtworkUpload(w, r)
	if !ok {
		return
	}
	slot := r.FormValue("slot")
	if slot == "" {
		slot = ambience.SlotBanner
	}
	pack, url, err := h.registry.AttachAsset(r.Context(), chi.URLParam(r, "id"), slot, data)
	if err != nil {
		writeAmbienceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"url": url, "slot": slot, "pack": pack})
}

// HandleServeAsset streams stored pack artwork. Public: ambience renders on
// the login screen. Refs are content-addressed, so responses are immutable.
func (h *AmbienceHandler) HandleServeAsset(w http.ResponseWriter, r *http.Request) {
	if h.unavailable(w) {
		return
	}
	ref := chi.URLParam(r, "ref")
	data, contentType, err := h.registry.ServeAsset(r.Context(), ref)
	switch {
	case errors.Is(err, ambience.ErrAssetNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Unknown ambience asset")
		return
	case errors.Is(err, ambience.ErrStorageUnavailable):
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Asset storage is not configured")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load asset")
		return
	}
	etag := `"` + ref + `"`
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", branding.AssetContentSecurityPolicy)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
