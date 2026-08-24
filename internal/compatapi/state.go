package compatapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// State operations adapt the canonical subject-owned state: progress,
// watched, favorites, bookmarks, collections, playlists, and downloads.
// Everything remains owned by Bloem; companions keep no authoritative copy.

func (h *Handler) stateService(w http.ResponseWriter, r *http.Request) (StateService, bool) {
	if h.svc.State == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "unavailable", "state is unavailable")
		return nil, false
	}
	return h.svc.State, true
}

func (h *Handler) handleGetProgress(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	progress, err := state.Progress(r.Context(), rc.subject, chi.URLParam(r, "itemID"))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, progress)
}

func (h *Handler) handleSetProgress(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	var body ProgressUpdate
	if !h.decodeBody(w, r, &body) {
		return
	}
	progress, err := state.SetProgress(r.Context(), rc.subject, chi.URLParam(r, "itemID"), body)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, progress)
}

func (h *Handler) handleSetWatched(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	var body struct {
		Watched bool `json:"watched"`
	}
	if !h.decodeBody(w, r, &body) {
		return
	}
	if err := state.SetWatched(r.Context(), rc.subject, chi.URLParam(r, "itemID"), body.Watched); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleListFavorites(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := h.pageParams(w, r, Operation{ID: "listFavorites"})
	if !ok {
		return
	}
	page, err := state.Favorites(r.Context(), rc.subject, cursor, limit)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeItemPage(w, "listFavorites", page)
}

func (h *Handler) handleSetFavorite(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	var body struct {
		Favorite bool `json:"favorite"`
	}
	if !h.decodeBody(w, r, &body) {
		return
	}
	if err := state.SetFavorite(r.Context(), rc.subject, chi.URLParam(r, "itemID"), body.Favorite); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleListBookmarks(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	bookmarks, err := state.Bookmarks(r.Context(), rc.subject, r.URL.Query().Get("item_id"))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if bookmarks == nil {
		bookmarks = []Bookmark{}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"bookmarks": bookmarks})
}

func (h *Handler) handleCreateBookmark(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	var body BookmarkCreate
	if !h.decodeBody(w, r, &body) {
		return
	}
	if body.ItemID == "" {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "item_id is required")
		return
	}
	bookmark, err := state.CreateBookmark(r.Context(), rc.subject, body)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, bookmark)
}

func (h *Handler) handleDeleteBookmark(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	if err := state.DeleteBookmark(r.Context(), rc.subject, chi.URLParam(r, "bookmarkID")); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleListCollections(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := h.pageParams(w, r, Operation{ID: "listCollections"})
	if !ok {
		return
	}
	page, err := state.Collections(r.Context(), rc.subject, cursor, limit)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if page.Collections == nil {
		page.Collections = []Collection{}
	}
	h.writeJSON(w, http.StatusOK, h.listPayload("listCollections", "collections", page.Collections, page.NextCursor))
}

func (h *Handler) handleGetCollection(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	collection, err := state.Collection(r.Context(), rc.subject, chi.URLParam(r, "collectionID"))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, collection)
}

func (h *Handler) handleListPlaylists(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := h.pageParams(w, r, Operation{ID: "listPlaylists"})
	if !ok {
		return
	}
	page, err := state.Playlists(r.Context(), rc.subject, cursor, limit)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if page.Playlists == nil {
		page.Playlists = []Playlist{}
	}
	h.writeJSON(w, http.StatusOK, h.listPayload("listPlaylists", "playlists", page.Playlists, page.NextCursor))
}

func (h *Handler) handleCreatePlaylist(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	var body PlaylistWrite
	if !h.decodeBody(w, r, &body) {
		return
	}
	if body.Name == "" {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	playlist, err := state.CreatePlaylist(r.Context(), rc.subject, body)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, playlist)
}

func (h *Handler) handleUpdatePlaylist(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	var body PlaylistWrite
	if !h.decodeBody(w, r, &body) {
		return
	}
	if body.Name == "" {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	playlist, err := state.UpdatePlaylist(r.Context(), rc.subject, chi.URLParam(r, "playlistID"), body)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, playlist)
}

func (h *Handler) handleDeletePlaylist(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	if err := state.DeletePlaylist(r.Context(), rc.subject, chi.URLParam(r, "playlistID")); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleListDownloads(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	downloads, err := state.Downloads(r.Context(), rc.subject)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if downloads == nil {
		downloads = []Download{}
	}
	for _, download := range downloads {
		if !h.checkGrant(w, r, rc.app, download.Grant) {
			return
		}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"downloads": downloads})
}

func (h *Handler) handleCreateDownload(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	var body struct {
		ItemID string `json:"item_id"`
	}
	if !h.decodeBody(w, r, &body) {
		return
	}
	if body.ItemID == "" {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "item_id is required")
		return
	}
	download, err := state.CreateDownload(r.Context(), rc.subject, body.ItemID)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if !h.checkGrant(w, r, rc.app, download.Grant) {
		return
	}
	h.writeJSON(w, http.StatusCreated, download)
}

func (h *Handler) handleDeleteDownload(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	state, ok := h.stateService(w, r)
	if !ok {
		return
	}
	if err := state.DeleteDownload(r.Context(), rc.subject, chi.URLParam(r, "downloadID")); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
