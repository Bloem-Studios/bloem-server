package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/artworkurl"
	"github.com/Silo-Server/silo-server/internal/catalog"
)

// ItemCollectionsSource lists the visible server collections containing one
// item, narrowed to the viewer's library scope.
type ItemCollectionsSource interface {
	ListContainingItem(ctx context.Context, contentID string, filter catalog.AccessFilter) ([]catalog.ItemCollectionMembership, error)
}

// ItemAccessChecker reports catalog.ErrItemNotFound when an item is outside the
// viewer's scope (library allow/deny lists, content-rating ceiling).
type ItemAccessChecker interface {
	EnsureAccessible(ctx context.Context, contentID string, filter catalog.AccessFilter) error
}

// ItemCollectionsHandler serves the "part of a collection" document for a
// title's detail page: every visible server collection that contains it.
//
// Silo has no item-to-collections lookup, so before this a client scanned the
// server collection list and every collection's items to find the ones holding
// the title. This answers it with one indexed query instead. Visibility is the
// same as GET /collections/server; see catalog.ListContainingItem for the rules
// and for why smart collections are not included.
type ItemCollectionsHandler struct {
	collections ItemCollectionsSource
	items       ItemAccessChecker
	artwork     *LibraryCollectionHandler
}

// NewItemCollectionsHandler creates an ItemCollectionsHandler. resolver may be
// nil, in which case stored artwork keys resolve to no URL rather than to a
// path no client can fetch.
func NewItemCollectionsHandler(collections ItemCollectionsSource, items ItemAccessChecker, resolver artworkurl.Resolver) *ItemCollectionsHandler {
	// Artwork URLs are presigned exactly the way the server-collection list
	// presigns them, by the same function, so the two surfaces cannot drift.
	return &ItemCollectionsHandler{
		collections: collections,
		items:       items,
		artwork:     &LibraryCollectionHandler{ArtworkResolver: resolver},
	}
}

type itemCollectionsResponse struct {
	Collections []itemCollectionEntry `json:"collections"`
}

type itemCollectionEntry struct {
	ID                string  `json:"id"`
	Title             string  `json:"title"`
	CollectionType    string  `json:"collection_type"`
	LibraryID         string  `json:"library_id"`
	LibraryName       string  `json:"library_name"`
	GroupID           *string `json:"group_id,omitempty"`
	GroupName         string  `json:"group_name,omitempty"`
	Featured          bool    `json:"featured,omitempty"`
	PosterURL         string  `json:"poster_url"`
	PosterThumbhash   string  `json:"poster_thumbhash,omitempty"`
	BackdropURL       string  `json:"backdrop_url,omitempty"`
	BackdropThumbhash string  `json:"backdrop_thumbhash,omitempty"`
	ItemCount         int     `json:"item_count"`
}

// HandleListItemCollections handles
// GET /api/bloem/v1/catalog/items/{content_id}/collections.
//
// An item the viewer may not see answers 404, exactly as its detail page does,
// so the endpoint cannot be used to probe for hidden titles.
func (h *ItemCollectionsHandler) HandleListItemCollections(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.collections == nil || h.items == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Item collections are not available")
		return
	}
	contentID := strings.TrimSpace(chi.URLParam(r, "content_id"))
	if contentID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "content_id is required")
		return
	}
	filter := requestAccessFilter(r)
	if err := h.items.EnsureAccessible(r.Context(), contentID, filter); err != nil {
		if errors.Is(err, catalog.ErrItemNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Item not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to check item access")
		return
	}
	memberships, err := h.collections.ListContainingItem(r.Context(), contentID, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load collections")
		return
	}
	resp := itemCollectionsResponse{Collections: make([]itemCollectionEntry, 0, len(memberships))}
	for _, m := range memberships {
		resp.Collections = append(resp.Collections, itemCollectionEntry{
			ID:                m.ID,
			Title:             m.Title,
			CollectionType:    m.CollectionType,
			LibraryID:         strconv.Itoa(m.LibraryID),
			LibraryName:       m.LibraryName,
			GroupID:           m.GroupID,
			GroupName:         m.GroupName,
			Featured:          m.Featured,
			PosterURL:         h.artwork.presignGPURLCtx(r.Context(), m.PosterURL),
			PosterThumbhash:   m.PosterThumbhash,
			BackdropURL:       h.artwork.presignGPURLCtx(r.Context(), m.BackdropURL),
			BackdropThumbhash: m.BackdropThumbhash,
			ItemCount:         m.ItemCount,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}
