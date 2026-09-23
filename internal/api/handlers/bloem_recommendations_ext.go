package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/models"
)

// resolvedSimilarItem is a Similar rail card: enough to render a tile without
// the weight of a full item-detail response (ratings from every source,
// studios, networks, ...) a rail has no use for.
type resolvedSimilarItem struct {
	ContentID     string  `json:"content_id"`
	Type          string  `json:"type"`
	Title         string  `json:"title"`
	Year          int     `json:"year,omitempty"`
	RuntimeSecond int     `json:"runtime_seconds,omitempty"`
	ContentRating string  `json:"content_rating,omitempty"`
	PosterURL     string  `json:"poster_url,omitempty"`
	Score         float64 `json:"score"`
	Reason        string  `json:"reason"`
	ReasonDetail  string  `json:"reason_detail,omitempty"`
}

type resolvedSimilarItemsResponse struct {
	Items []resolvedSimilarItem `json:"items"`
}

// HandleSimilarResolved handles GET /recommendations/similar/{item_id}/resolved.
//
// The plain /similar/{item_id} endpoint names only a content identifier per
// title — deliberately: it is also the input to co-watch and taste-profile
// scoring, and never carried title or poster data. A rail that wants to
// display what it names had one path before this endpoint: one detail request
// per item, round after round. This does the same recommendation call and
// resolves every result in one additional batched fetch, so a rail of ten
// costs two requests total instead of eleven.
func (h *RecommendationsHandler) HandleSimilarResolved(w http.ResponseWriter, r *http.Request) {
	if !h.enabled {
		writeJSON(w, http.StatusOK, resolvedSimilarItemsResponse{Items: []resolvedSimilarItem{}})
		return
	}

	itemID := chi.URLParam(r, "item_id")
	if itemID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Item ID is required")
		return
	}

	limit, _ := parsePagination(r)
	if limit > 50 {
		limit = 50
	}
	if limit <= 0 {
		limit = 20
	}

	scored, err := h.engine.SimilarItems(r.Context(), itemID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to fetch similar items")
		return
	}

	response := resolvedSimilarItemsResponse{Items: []resolvedSimilarItem{}}
	if len(scored) == 0 || h.Fetcher == nil {
		writeJSON(w, http.StatusOK, response)
		return
	}

	ids := make([]string, 0, len(scored))
	for _, item := range scored {
		ids = append(ids, item.MediaItemID)
	}
	found, err := h.Fetcher.FetchItemsByContentIDs(r.Context(), ids, requestAccessFilter(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to resolve similar items")
		return
	}
	byID := make(map[string]*models.MediaItem, len(found))
	for _, item := range found {
		if item != nil {
			byID[item.ContentID] = item
		}
	}

	// The recommender's own order is a relevance ranking; a batched fetch
	// answers in its own order (or omits an item the viewer's access no longer
	// permits), so the response is rebuilt in the order SimilarItems named,
	// dropping whatever the fetch could not resolve.
	for _, scoredItem := range scored {
		item, ok := byID[scoredItem.MediaItemID]
		if !ok {
			continue
		}
		posterURL := ""
		if item.PosterPath != "" && h.DetailSvc != nil {
			posterURL = h.DetailSvc.PresignURL(r.Context(), item.PosterPath, "card")
		}
		response.Items = append(response.Items, resolvedSimilarItem{
			ContentID:     item.ContentID,
			Type:          item.Type,
			Title:         item.Title,
			Year:          item.Year,
			RuntimeSecond: item.Runtime * 60,
			ContentRating: item.ContentRating,
			PosterURL:     posterURL,
			Score:         scoredItem.Score,
			Reason:        scoredItem.Reason,
			ReasonDetail:  scoredItem.ReasonDetail,
		})
	}

	writeJSON(w, http.StatusOK, response)
}
