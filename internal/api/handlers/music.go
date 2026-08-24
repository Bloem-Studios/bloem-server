package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/music"
	"github.com/go-chi/chi/v5"
)

const musicArtistPageSize = 100

type MusicHandler struct {
	repo   music.Repository
	itemsH *ItemsHandler
	native bool
}

// NewNativeMusicHandler serves the Bloem-only /api/v2 contract. Viewer scope
// is already resolved by the native client middleware, so it never depends on
// or changes the Silo-compatible Items handler.
func NewNativeMusicHandler(repo music.Repository) *MusicHandler {
	return &MusicHandler{repo: repo, native: true}
}

func NewMusicHandler(repo music.Repository, itemsH *ItemsHandler) *MusicHandler {
	return &MusicHandler{repo: repo, itemsH: itemsH}
}

func (h *MusicHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	filter, ok := h.accessFilter(w, r)
	if !ok {
		return
	}
	value, err := h.repo.Status(r.Context(), filter)
	if err != nil {
		h.writeFailure(w, r, err)
		return
	}
	if value.LibraryIDs == nil {
		value.LibraryIDs = []int{}
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *MusicHandler) HandleArtists(w http.ResponseWriter, r *http.Request) {
	libraryID, ok := musicLibraryID(w, r)
	if !ok {
		return
	}
	filter, ok := h.accessFilter(w, r)
	if !ok {
		return
	}
	page, err := h.repo.ListArtists(r.Context(), libraryID, strings.TrimSpace(r.URL.Query().Get("cursor")), musicArtistPageSize, filter)
	if err != nil {
		h.writeFailure(w, r, err)
		return
	}
	for i := range page.Items {
		page.Items[i].ArtworkURL = h.artworkURL(r, page.Items[i].ArtworkPath)
	}
	if page.Items == nil {
		page.Items = []music.Artist{}
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *MusicHandler) HandleArtist(w http.ResponseWriter, r *http.Request) {
	libraryID, ok := musicLibraryID(w, r)
	if !ok {
		return
	}
	filter, ok := h.accessFilter(w, r)
	if !ok {
		return
	}
	value, err := h.repo.Artist(r.Context(), libraryID, chi.URLParam(r, "id"), filter)
	if err != nil {
		h.writeFailure(w, r, err)
		return
	}
	value.Artist.ArtworkURL = h.artworkURL(r, value.Artist.ArtworkPath)
	for i := range value.Albums {
		value.Albums[i].ArtworkURL = h.artworkURL(r, value.Albums[i].ArtworkPath)
	}
	if value.Albums == nil {
		value.Albums = []music.Album{}
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *MusicHandler) HandleAlbum(w http.ResponseWriter, r *http.Request) {
	libraryID, ok := musicLibraryID(w, r)
	if !ok {
		return
	}
	filter, ok := h.accessFilter(w, r)
	if !ok {
		return
	}
	value, err := h.repo.Album(r.Context(), libraryID, chi.URLParam(r, "id"), filter)
	if err != nil {
		h.writeFailure(w, r, err)
		return
	}
	value.Album.ArtworkURL = h.artworkURL(r, value.Album.ArtworkPath)
	for i := range value.Tracks {
		value.Tracks[i].ArtworkURL = h.artworkURL(r, value.Tracks[i].ArtworkPath)
	}
	if value.Tracks == nil {
		value.Tracks = []music.Track{}
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *MusicHandler) accessFilter(w http.ResponseWriter, r *http.Request) (catalogFilter catalog.AccessFilter, ok bool) {
	if h == nil || h.repo == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Music is not configured")
		return catalogFilter, false
	}
	if h.native {
		scope, found := access.GetScope(r.Context())
		if !found {
			writeError(w, http.StatusForbidden, "viewer_scope_required", "Viewer scope is required")
			return catalogFilter, false
		}
		return catalog.AccessFilter{
			AllowedLibraryIDs:  scope.AllowedLibraryIDs,
			DisabledLibraryIDs: scope.DisabledLibraryIDs,
			UserID:             apimw.GetUserID(r.Context()),
			ProfileID:          apimw.GetProfileID(r.Context()),
		}, true
	}
	if h.itemsH == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Music is not configured")
		return catalogFilter, false
	}
	return h.itemsH.accessFilterOrError(w, r)
}

func (h *MusicHandler) artworkURL(r *http.Request, path string) string {
	if h == nil || h.itemsH == nil {
		return ""
	}
	return h.itemsH.presignURL(r, path, "card")
}

func (h *MusicHandler) writeFailure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, music.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Music resource not found")
		return
	}
	slog.ErrorContext(r.Context(), "music request failed", "component", "api", "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "Music request failed")
}

func musicLibraryID(w http.ResponseWriter, r *http.Request) (int, bool) {
	value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("library_id")))
	if err != nil || value <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "library_id must be a positive integer")
		return 0, false
	}
	return value, true
}
