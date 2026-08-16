package compatapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Catalog operations are read-only adapters over the policy-filtered catalog.
// Adult-content and tenancy filtering happens inside the CatalogService;
// hidden content is indistinguishable from absent content here.

func (h *Handler) catalogService(w http.ResponseWriter, r *http.Request) (CatalogService, bool) {
	if h.svc.Catalog == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "unavailable", "catalog is unavailable")
		return nil, false
	}
	return h.svc.Catalog, true
}

func (h *Handler) handleListLibraries(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	catalog, ok := h.catalogService(w, r)
	if !ok {
		return
	}
	libraries, err := catalog.Libraries(r.Context(), rc.subject)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if libraries == nil {
		libraries = []Library{}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"libraries": libraries})
}

// itemPageWire signs the domain continuation token before it reaches the
// wire.
type itemPageWire struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func (h *Handler) writeItemPage(w http.ResponseWriter, op string, page ItemPage) {
	if page.Items == nil {
		page.Items = []Item{}
	}
	h.writeJSON(w, http.StatusOK, itemPageWire{
		Items:      page.Items,
		NextCursor: h.encodeCursor(op, page.NextCursor),
	})
}

func (h *Handler) handleListItems(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	catalog, ok := h.catalogService(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := h.pageParams(w, r, Operation{ID: "listItems"})
	if !ok {
		return
	}
	query := r.URL.Query()
	page, err := catalog.Items(r.Context(), rc.subject, ItemQuery{
		LibraryID: query.Get("library_id"),
		Kind:      query.Get("kind"),
		Sort:      query.Get("sort"),
		Cursor:    cursor,
		Limit:     limit,
	})
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeItemPage(w, "listItems", page)
}

func (h *Handler) handleGetItem(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	catalog, ok := h.catalogService(w, r)
	if !ok {
		return
	}
	item, err := catalog.Item(r.Context(), rc.subject, chi.URLParam(r, "itemID"))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, item)
}

func (h *Handler) handleListItemChildren(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	catalog, ok := h.catalogService(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := h.pageParams(w, r, Operation{ID: "listItemChildren"})
	if !ok {
		return
	}
	page, err := catalog.Children(r.Context(), rc.subject, chi.URLParam(r, "itemID"), ItemQuery{Cursor: cursor, Limit: limit})
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeItemPage(w, "listItemChildren", page)
}

func (h *Handler) handleSearchItems(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	catalog, ok := h.catalogService(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := h.pageParams(w, r, Operation{ID: "searchItems"})
	if !ok {
		return
	}
	page, err := catalog.Search(r.Context(), rc.subject, SearchQuery{
		Query:  r.URL.Query().Get("q"),
		Cursor: cursor,
		Limit:  limit,
	})
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeItemPage(w, "searchItems", page)
}

func (h *Handler) handleGetPerson(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	catalog, ok := h.catalogService(w, r)
	if !ok {
		return
	}
	person, err := catalog.Person(r.Context(), rc.subject, chi.URLParam(r, "personID"))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, person)
}

func (h *Handler) handleResolveArtwork(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	catalog, ok := h.catalogService(w, r)
	if !ok {
		return
	}
	grant, err := catalog.ArtworkGrant(r.Context(), rc.subject, rc.app.ApplicationID, chi.URLParam(r, "itemID"), chi.URLParam(r, "artworkKind"))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if !h.checkGrant(w, r, rc.app, &grant) {
		return
	}
	h.writeJSON(w, http.StatusOK, grant)
}
