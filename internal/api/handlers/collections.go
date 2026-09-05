package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/s3client"
	"github.com/Silo-Server/silo-server/internal/usercollections"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// CollectionHandler handles personal collection CRUD endpoints.
type CollectionHandler struct {
	storeProvider      userstore.UserStoreProvider
	LibraryCollections collectionPreferenceLibraryReader
	Executor           *catalog.QueryExecutor
	S3GP               *s3client.Client
	HTTPClient         *http.Client
	PresignTTL         time.Duration
}

// NewCollectionHandler creates a new CollectionHandler.
func NewCollectionHandler(provider userstore.UserStoreProvider) *CollectionHandler {
	return &CollectionHandler{storeProvider: provider}
}

// --- Request/Response types ---

type PersonalCollectionCreateRequest struct {
	Name                       string          `json:"name"`
	CollectionType             string          `json:"collection_type"`
	IsShared                   bool            `json:"is_shared"`
	AllowedProfileIDs          []string        `json:"allowed_profile_ids"`
	QueryDefinition            json.RawMessage `json:"query_definition"`
	SortConfig                 json.RawMessage `json:"sort_config"`
	DisplayQueryDefinition     json.RawMessage `json:"display_query_definition"`
	IncludeInServerCollections bool            `json:"include_in_server_collections"`
	PosterSourceURL            string          `json:"poster_source_url"`
}

type updateCollectionRequest struct {
	Name                       *string                `json:"name"`
	Description                *string                `json:"description"`
	IsShared                   *bool                  `json:"is_shared"`
	AllowedProfileIDs          *[]string              `json:"allowed_profile_ids"`
	QueryDefinition            json.RawMessage        `json:"query_definition"`
	SortConfig                 json.RawMessage        `json:"sort_config"`
	SourceURL                  *string                `json:"source_url"`
	MaxItems                   *int                   `json:"max_items"`
	LibraryIDs                 *[]int                 `json:"library_ids"`
	DisplayQueryDefinition     json.RawMessage        `json:"display_query_definition"`
	IncludeInServerCollections *bool                  `json:"include_in_server_collections"`
	PosterSourceURL            *string                `json:"poster_source_url"`
	GroupID                    optionalNullableString `json:"group_id"`
}

type collectionItemRequest struct {
	Position int `json:"position"`
}

type PersonalCollectionView struct {
	ID                         string          `json:"id"`
	ProfileID                  string          `json:"profile_id"`
	CreatorProfileID           string          `json:"creator_profile_id"`
	Name                       string          `json:"name"`
	Description                string          `json:"description,omitempty"`
	CollectionType             string          `json:"collection_type"`
	IsShared                   bool            `json:"is_shared"`
	AllowedProfileIDs          []string        `json:"allowed_profile_ids"`
	QueryDefinition            json.RawMessage `json:"query_definition"`
	SortConfig                 json.RawMessage `json:"sort_config"`
	SortOrder                  int             `json:"sort_order"`
	GroupID                    *string         `json:"group_id"`
	SourceURL                  string          `json:"source_url,omitempty"`
	SourceConfig               json.RawMessage `json:"source_config,omitempty"`
	SyncSchedule               string          `json:"sync_schedule,omitempty"`
	NextSyncAt                 string          `json:"next_sync_at,omitempty"`
	LastSyncAt                 string          `json:"last_sync_at,omitempty"`
	LastSyncStatus             string          `json:"last_sync_status,omitempty"`
	LastSyncMessage            string          `json:"last_sync_message,omitempty"`
	DisplayQueryDefinition     json.RawMessage `json:"display_query_definition,omitempty"`
	ItemCount                  int             `json:"item_count"`
	IncludeInServerCollections bool            `json:"include_in_server_collections"`
	PosterURL                  string          `json:"poster_url,omitempty"`
	PosterThumbhash            string          `json:"poster_thumbhash,omitempty"`
	CreatedAt                  string          `json:"created_at"`
	UpdatedAt                  string          `json:"updated_at"`
}

type PersonalCollectionListView struct {
	Collections []PersonalCollectionView `json:"collections"`
	Groups      []CollectionGroupView    `json:"groups"`
}

type CollectionCapabilitiesView struct {
	// DisplayFilterFields are the catalog query fields a personal-collection
	// display filter may use. Clients build a display_query_definition fragment
	// from these rather than a bespoke enum.
	DisplayFilterFields       []string                           `json:"display_filter_fields"`
	DisplayFilterPresets      CollectionDisplayFilterPresetsView `json:"display_filter_presets"`
	CollectionDefaultSort     bool                               `json:"collection_default_sort"`
	CollectionSortPreferences bool                               `json:"collection_sort_preferences"`
	EffectiveCollectionSort   bool                               `json:"effective_collection_sort"`
	// SortPreferenceKinds are the collection_kind values this server accepts on
	// the sort-preference endpoints. CollectionSortPreferences alone cannot
	// distinguish a server that also stores the personal-list kinds
	// ('watchlist', 'favorites') from one that rejects them.
	SortPreferenceKinds []string `json:"sort_preference_kinds"`
}

type CollectionDisplayFilterPresetsView struct {
	Watched []string `json:"watched"`
	Media   []string `json:"media"`
}

type CollectionGroupView struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	DefaultSortMode string `json:"default_sort_mode"`
	SortOrder       int    `json:"sort_order"`
}

type CollectionGroupCreateRequest struct {
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	DefaultSortMode string `json:"default_sort_mode"`
}

type CollectionGroupUpdateRequest struct {
	Name            *string `json:"name"`
	Slug            *string `json:"slug"`
	DefaultSortMode *string `json:"default_sort_mode"`
}

type reorderCollectionGroupsRequest struct {
	OrderedIDs []string `json:"ordered_ids"`
}

type collectionItemResponse struct {
	CollectionID string `json:"collection_id"`
	MediaItemID  string `json:"media_item_id"`
	Position     int    `json:"position"`
	AddedAt      string `json:"added_at"`
}

type collectionItemsListResponse struct {
	Items []collectionItemResponse `json:"items"`
}

type previewCollectionRequest struct {
	QueryDefinition json.RawMessage `json:"query_definition"`
	Limit           int             `json:"limit"`
}

type previewCollectionResponse struct {
	Items []previewCollectionItemResponse `json:"items"`
	Total int                             `json:"total"`
}

type previewCollectionItemResponse struct {
	ContentID string `json:"content_id"`
	Title     string `json:"title"`
	Type      string `json:"type"`
}

// --- Handler methods ---

// HandleListCollections handles GET /collections.
func (h *CollectionHandler) HandleListCollections(w http.ResponseWriter, r *http.Request) {
	resp, err := h.ListPersonalCollections(r.Context(), apimw.GetUserID(r.Context()), apimw.GetProfileID(r.Context()))
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleCapabilities exposes additive feature support for collection clients.
func (h *CollectionHandler) HandleCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.Capabilities())
}

// HandleCreateCollection handles POST /collections.
func (h *CollectionHandler) HandleCreateCollection(w http.ResponseWriter, r *http.Request) {
	var req PersonalCollectionCreateRequest
	if err := decodeJSONOrMultipart(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	created, err := h.CreatePersonalCollection(r.Context(), PersonalCollectionCreateCommand{
		UserID:     apimw.GetUserID(r.Context()),
		ProfileID:  apimw.GetProfileID(r.Context()),
		Request:    req,
		PosterFile: posterFileReader(r),
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// HandleUpdateCollection handles PUT /collections/{id}.
func (h *CollectionHandler) HandleUpdateCollection(w http.ResponseWriter, r *http.Request) {
	userID := apimw.GetUserID(r.Context())
	collectionID := chi.URLParam(r, "id")

	if collectionID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Collection ID is required")
		return
	}

	var req updateCollectionRequest
	if err := decodeJSONOrMultipart(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}

	store, err := h.storeProvider.ForUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access user store")
		return
	}

	profileID := apimw.GetProfileID(r.Context())
	input := userstore.UpdateCollectionInput{
		ID:                         collectionID,
		RequestProfileID:           profileID,
		Name:                       req.Name,
		Description:                req.Description,
		AllowedProfileIDs:          req.AllowedProfileIDs,
		IncludeInServerCollections: req.IncludeInServerCollections,
	}
	if req.DisplayQueryDefinition != nil {
		displayQueryDefinition, err := catalog.NormalizeDisplayQueryFragment(req.DisplayQueryDefinition)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		input.DisplayQueryDefinition = &displayQueryDefinition
	}
	if len(req.QueryDefinition) > 0 {
		normalized, err := normalizeSmartCollectionQueryDefinitionJSON(req.QueryDefinition, true, true)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "Invalid query_definition")
			return
		}
		value := string(normalized)
		input.QueryDefinition = &value
	}
	if len(req.SortConfig) > 0 {
		value, err := NormalizeCollectionSortConfig(req.SortConfig, true)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		input.SortConfig = &value
	}
	input.IsShared = req.IsShared
	if req.GroupID.Set() {
		groupID := req.GroupID.Value()
		if groupID != nil && strings.TrimSpace(*groupID) == "" {
			groupID = nil
		}
		if groupID != nil {
			if err := store.EnsureCollectionGroup(r.Context(), *groupID); err != nil {
				if errors.Is(err, userstore.ErrCollectionGroupNotFound) {
					writeError(w, http.StatusBadRequest, "bad_request", "Collection group not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to validate collection group")
				return
			}
		}
		input.GroupID = &groupID
	}

	// Source URL and max items both live inside source_config (Limit / URL).
	// Load the existing collection and re-marshal so the unaffected fields
	// (preset, media_type, etc.) survive untouched.
	if req.SourceURL != nil || req.MaxItems != nil || req.LibraryIDs != nil {
		existing, err := store.GetCollection(r.Context(), collectionID)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				writeError(w, http.StatusNotFound, "not_found", "Collection not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load collection")
			return
		}
		cfg, err := usercollections.ParseSourceConfig(existing.SourceConfig)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to parse source config")
			return
		}
		if req.LibraryIDs != nil {
			if !catalog.IsSyncableType(existing.CollectionType) {
				writeError(w, http.StatusBadRequest, "bad_request", "library_ids can only be edited for imported collections")
				return
			}
			if err := validateOptionalLibraryIDs(*req.LibraryIDs); err != nil {
				writeAPIError(w, err)
				return
			}
			cfg.LibraryIDs = append([]int(nil), (*req.LibraryIDs)...)
			emptyQuery := "{}"
			input.QueryDefinition = &emptyQuery
		}
		if req.MaxItems != nil {
			if *req.MaxItems < 0 {
				writeError(w, http.StatusBadRequest, "bad_request", "max_items must be zero or positive")
				return
			}
			if *req.MaxItems == 0 {
				cfg.Limit = nil
			} else {
				value := *req.MaxItems
				cfg.Limit = &value
			}
		}
		if req.SourceURL != nil {
			if existing.CollectionType != "mdblist" {
				writeError(w, http.StatusBadRequest, "bad_request", "source_url can only be edited for MDBList collections")
				return
			}
			normalized, err := usercollections.CanonicalMDBListURL(*req.SourceURL)
			if err != nil {
				writeError(w, http.StatusBadRequest, "bad_request", "source_url must be an MDBList list (https://mdblist.com/lists/...)")
				return
			}
			cfg.URL = normalized
			topURL := normalized
			input.SourceURL = &topURL
		}
		raw, err := usercollections.MarshalSourceConfig(cfg)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to encode source config")
			return
		}
		input.SourceConfig = &raw
	}

	if err := store.UpdateCollection(r.Context(), input); err != nil {
		if strings.Contains(err.Error(), "creator") {
			writeError(w, http.StatusForbidden, "forbidden", "Only the creator can edit this collection")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to update collection")
		return
	}

	posterSource := pointerStringValue(req.PosterSourceURL)
	if _, err := h.processCollectionPoster(r.Context(), store, collectionID, profileID, posterFileReader(r), posterSource); err != nil {
		if errors.Is(err, errCollectionForbidden) {
			writeError(w, http.StatusForbidden, "forbidden", "Only the creator can edit this collection")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	// Re-read the collection to return updated state.
	collection, err := store.GetCollection(r.Context(), collectionID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "not_found", "Collection not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve updated collection")
		return
	}

	writeJSON(w, http.StatusOK, h.toCollectionResponse(r, *collection))
}

func (h *CollectionHandler) HandlePreviewCollection(w http.ResponseWriter, r *http.Request) {
	if h.Executor == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Preview is not configured")
		return
	}

	var req previewCollectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}

	var def catalog.QueryDefinition
	if len(req.QueryDefinition) > 0 {
		normalized, err := normalizeQueryDefinitionJSON(req.QueryDefinition, true, true)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "Invalid query_definition")
			return
		}
		if err := json.Unmarshal(normalized, &def); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "Invalid query_definition")
			return
		}
	}

	items, total, err := h.Executor.Preview(r.Context(), def, requestAccessFilter(r), req.Limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	resp := previewCollectionResponse{Items: make([]previewCollectionItemResponse, 0, len(items)), Total: total}
	for _, item := range items {
		resp.Items = append(resp.Items, previewCollectionItemResponse{
			ContentID: item.ContentID,
			Title:     item.Title,
			Type:      item.Type,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleDeleteCollection handles DELETE /collections/{id}.
func (h *CollectionHandler) HandleDeleteCollection(w http.ResponseWriter, r *http.Request) {
	userID := apimw.GetUserID(r.Context())
	collectionID := chi.URLParam(r, "id")

	if collectionID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Collection ID is required")
		return
	}

	store, err := h.storeProvider.ForUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access user store")
		return
	}

	if err := store.DeleteCollection(r.Context(), collectionID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to delete collection")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleListCollectionItems handles GET /collections/{id}/items.
func (h *CollectionHandler) HandleListCollectionItems(w http.ResponseWriter, r *http.Request) {
	userID := apimw.GetUserID(r.Context())
	collectionID := chi.URLParam(r, "id")

	if collectionID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Collection ID is required")
		return
	}

	store, err := h.storeProvider.ForUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access user store")
		return
	}

	items, err := store.ListCollectionItems(r.Context(), collectionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list collection items")
		return
	}

	resp := collectionItemsListResponse{
		Items: make([]collectionItemResponse, 0, len(items)),
	}
	for _, ci := range items {
		resp.Items = append(resp.Items, collectionItemResponse{
			CollectionID: ci.CollectionID,
			MediaItemID:  ci.MediaItemID,
			Position:     ci.Position,
			AddedAt:      ci.AddedAt,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleAddCollectionItem handles PUT /collections/{id}/items/{item_id}.
func (h *CollectionHandler) HandleAddCollectionItem(w http.ResponseWriter, r *http.Request) {
	userID := apimw.GetUserID(r.Context())
	collectionID := chi.URLParam(r, "id")
	itemID := chi.URLParam(r, "item_id")

	if collectionID == "" || itemID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Collection ID and item ID are required")
		return
	}

	var req collectionItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Default position to 0 if body is empty or invalid.
		req.Position = 0
	}

	store, err := h.storeProvider.ForUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access user store")
		return
	}

	if err := store.AddCollectionItem(r.Context(), collectionID, itemID, req.Position); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to add item to collection")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type reorderRequest struct {
	OrderedIDs []string `json:"ordered_ids"`
	// GroupID scopes the reorder to one section. nil/absent targets Ungrouped.
	GroupID *string `json:"group_id,omitempty"`
}

// HandleReorderCollections handles PUT /collections/order.
// The body must contain every collection in scope; concurrent edits that
// would silently drop one are rejected.
func (h *CollectionHandler) HandleReorderCollections(w http.ResponseWriter, r *http.Request) {
	var req reorderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	if err := h.ReorderPersonalCollections(r.Context(), apimw.GetUserID(r.Context()), apimw.GetProfileID(r.Context()), req.GroupID, req.OrderedIDs); err != nil {
		writeAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleCreateCollectionGroup handles POST /collections/groups.
func (h *CollectionHandler) HandleCreateCollectionGroup(w http.ResponseWriter, r *http.Request) {
	var req CollectionGroupCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	group, err := h.CreateCollectionGroup(r.Context(), apimw.GetUserID(r.Context()), req)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, group)
}

// HandleUpdateCollectionGroup handles PUT /collections/groups/{id}.
func (h *CollectionHandler) HandleUpdateCollectionGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "id is required")
		return
	}
	var req CollectionGroupUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	group, err := h.UpdateCollectionGroup(r.Context(), apimw.GetUserID(r.Context()), id, req)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, group)
}

// HandleDeleteCollectionGroup handles DELETE /collections/groups/{id}.
func (h *CollectionHandler) HandleDeleteCollectionGroup(w http.ResponseWriter, r *http.Request) {
	if err := h.DeleteCollectionGroup(r.Context(), apimw.GetUserID(r.Context()), chi.URLParam(r, "id")); err != nil {
		writeAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleReorderCollectionGroups handles PUT /collections/groups/order.
func (h *CollectionHandler) HandleReorderCollectionGroups(w http.ResponseWriter, r *http.Request) {
	var req reorderCollectionGroupsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	if err := h.ReorderCollectionGroups(r.Context(), apimw.GetUserID(r.Context()), req.OrderedIDs); err != nil {
		writeAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleReorderCollectionItems handles PUT /collections/{id}/items/order.
// The body must contain every item currently in the collection.
func (h *CollectionHandler) HandleReorderCollectionItems(w http.ResponseWriter, r *http.Request) {
	userID := apimw.GetUserID(r.Context())
	collectionID := chi.URLParam(r, "id")

	if collectionID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Collection ID is required")
		return
	}

	var req reorderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}

	store, err := h.storeProvider.ForUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access user store")
		return
	}

	if err := store.ReorderCollectionItems(r.Context(), collectionID, req.OrderedIDs); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleRemoveCollectionItem handles DELETE /collections/{id}/items/{item_id}.
func (h *CollectionHandler) HandleRemoveCollectionItem(w http.ResponseWriter, r *http.Request) {
	userID := apimw.GetUserID(r.Context())
	collectionID := chi.URLParam(r, "id")
	itemID := chi.URLParam(r, "item_id")

	if collectionID == "" || itemID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Collection ID and item ID are required")
		return
	}

	store, err := h.storeProvider.ForUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access user store")
		return
	}

	if err := store.RemoveCollectionItem(r.Context(), collectionID, itemID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to remove item from collection")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Helpers ---

func toCollectionResponse(c userstore.Collection) PersonalCollectionView {
	queryDefinition := defaultJSON([]byte(c.QueryDefinition))
	sortConfig := defaultJSON([]byte(c.SortConfig))
	resp := PersonalCollectionView{
		ID:                         c.ID,
		ProfileID:                  c.ProfileID,
		CreatorProfileID:           c.CreatorProfileID,
		Name:                       c.Name,
		Description:                c.Description,
		CollectionType:             c.CollectionType,
		IsShared:                   c.IsShared,
		AllowedProfileIDs:          append([]string(nil), c.AllowedProfileIDs...),
		QueryDefinition:            queryDefinition,
		SortConfig:                 sortConfig,
		SortOrder:                  c.SortOrder,
		GroupID:                    c.GroupID,
		SourceURL:                  c.SourceURL,
		LastSyncStatus:             c.LastSyncStatus,
		LastSyncMessage:            c.LastSyncMessage,
		ItemCount:                  c.ItemCount,
		IncludeInServerCollections: c.IncludeInServerCollections,
		PosterURL:                  c.PosterURL,
		PosterThumbhash:            c.PosterThumbhash,
		CreatedAt:                  c.CreatedAt,
		UpdatedAt:                  c.UpdatedAt,
	}
	if strings.TrimSpace(c.DisplayQueryDefinition) != "" {
		resp.DisplayQueryDefinition = json.RawMessage(c.DisplayQueryDefinition)
	}
	if c.SourceConfig != "" && c.SourceConfig != "{}" {
		resp.SourceConfig = json.RawMessage(c.SourceConfig)
	}
	if c.SyncSchedule != nil {
		resp.SyncSchedule = *c.SyncSchedule
	}
	if c.NextSyncAt != nil {
		resp.NextSyncAt = c.NextSyncAt.UTC().Format(time.RFC3339)
	}
	if c.LastSyncAt != nil {
		resp.LastSyncAt = c.LastSyncAt.UTC().Format(time.RFC3339)
	}
	return resp
}

func defaultJSON(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(raw)
}

func normalizeQueryDefinitionJSON(raw []byte, allowPersonalizedSorts, allowPersonalizedFields bool) (json.RawMessage, error) {
	var def catalog.QueryDefinition
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &def); err != nil {
			return nil, err
		}
	}
	def = def.Normalize()
	if err := def.ValidateWithOptions(allowPersonalizedSorts, allowPersonalizedFields); err != nil {
		return nil, err
	}
	normalized, err := json.Marshal(def)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func normalizeSmartCollectionQueryDefinitionJSON(raw []byte, allowPersonalizedSorts, allowPersonalizedFields bool) (json.RawMessage, error) {
	normalized, err := normalizeQueryDefinitionJSON(raw, allowPersonalizedSorts, allowPersonalizedFields)
	if err != nil {
		return nil, err
	}
	var def catalog.QueryDefinition
	if err := json.Unmarshal(normalized, &def); err != nil {
		return nil, err
	}
	def = catalog.ApplySmartCollectionItemLimit(def)
	out, err := json.Marshal(def)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func firstNonEmptyCollection(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// errCollectionForbidden is returned by the artwork pipeline when the caller
// is not the collection's creator. It surfaces as a 403 to the client.
var errCollectionForbidden = errors.New("only the creator can edit this collection")

// HandleDeleteCollectionImage clears the poster on a personal collection.
// The query parameter "type" is required and currently only "poster" is
// supported.
func (h *CollectionHandler) HandleDeleteCollectionImage(w http.ResponseWriter, r *http.Request) {
	collectionID := chi.URLParam(r, "id")
	if collectionID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Collection ID is required")
		return
	}
	imageType := r.URL.Query().Get("type")
	if imageType != "poster" {
		writeError(w, http.StatusBadRequest, "bad_request", `type must be "poster"`)
		return
	}

	userID := apimw.GetUserID(r.Context())
	profileID := apimw.GetProfileID(r.Context())
	store, err := h.storeProvider.ForUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access user store")
		return
	}
	collection, err := store.GetCollection(r.Context(), collectionID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Collection not found")
		return
	}
	if collection.CreatorProfileID != profileID {
		writeError(w, http.StatusForbidden, "forbidden", "Only the creator can edit this collection")
		return
	}

	if err := removeCollectionImageVariants(r.Context(), h.S3GP, userCollectionImagePrefix, collectionID, imageType); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to delete images")
		return
	}
	empty := ""
	if err := store.UpdateCollection(r.Context(), userstore.UpdateCollectionInput{
		ID:               collectionID,
		RequestProfileID: profileID,
		PosterURL:        &empty,
		PosterThumbhash:  &empty,
	}); err != nil {
		if strings.Contains(err.Error(), "creator") {
			writeError(w, http.StatusForbidden, "forbidden", "Only the creator can edit this collection")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to clear poster")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// posterInputProvided reports whether the request body or form contained
// poster artwork inputs that the upload pipeline would act on.
// processCollectionPoster persists an uploaded or sourced poster image on the
// given user collection. posterFile reads the uploaded part (nil when the
// request carried no multipart body; http.ErrMissingFile when the part is
// absent). It is a no-op, answering false, when no poster input was provided.
// The caller is responsible for ensuring the request profile owns the
// collection.
func (h *CollectionHandler) processCollectionPoster(
	ctx context.Context,
	store userstore.UserStore,
	collectionID, requestProfileID string,
	posterFile func() ([]byte, error),
	sourceURL string,
) (bool, error) {
	source := strings.TrimSpace(sourceURL)

	var fileData []byte
	if posterFile != nil {
		data, err := posterFile()
		switch {
		case err == nil:
			fileData = data
		case errors.Is(err, http.ErrMissingFile):
			// fall through to source URL handling
		default:
			return true, fmt.Errorf("poster: %w", err)
		}
	}
	if fileData == nil {
		if source == "" {
			return false, nil
		}
		downloaded, err := downloadCollectionImageURL(ctx, h.HTTPClient, source)
		if err != nil {
			return true, fmt.Errorf("poster source: %w", err)
		}
		fileData = downloaded
	}

	if h.S3GP == nil {
		return true, fmt.Errorf("poster upload requires configured object storage")
	}
	if err := removeCollectionImageVariants(ctx, h.S3GP, userCollectionImagePrefix, collectionID, "poster"); err != nil {
		return true, fmt.Errorf("clearing previous poster: %w", err)
	}
	s3Path, thumbhash, err := uploadCollectionImageVariants(ctx, h.S3GP, userCollectionImagePrefix, collectionID, "poster", fileData)
	if err != nil {
		return true, fmt.Errorf("poster: %w", err)
	}

	if err := store.UpdateCollection(ctx, userstore.UpdateCollectionInput{
		ID:               collectionID,
		RequestProfileID: requestProfileID,
		PosterURL:        &s3Path,
		PosterThumbhash:  &thumbhash,
	}); err != nil {
		if strings.Contains(err.Error(), "creator") {
			return true, errCollectionForbidden
		}
		return true, fmt.Errorf("persisting poster: %w", err)
	}
	return true, nil
}

// presignUserCollectionPoster returns a presigned URL for the card-sized
// variant of the stored poster, mirroring the admin pipeline. Empty paths
// return "".
func (h *CollectionHandler) presignUserCollectionPoster(ctx context.Context, path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if strings.HasPrefix(path, "/") {
		return path
	}
	if h.S3GP == nil {
		return ""
	}
	ttl := h.PresignTTL
	if ttl <= 0 {
		ttl = 4 * time.Hour
	}
	url, err := h.S3GP.PresignGetURL(ctx, h.S3GP.Bucket(), cardThumbnailPath(path), ttl)
	if err != nil {
		return ""
	}
	return url
}

// toCollectionResponse mirrors the package-level helper but presigns artwork
// URLs using the handler's S3 client. Use this method when serving HTTP
// responses; the package-level function is reserved for callers without a
// presign capability.
func (h *CollectionHandler) toCollectionResponse(r *http.Request, c userstore.Collection) PersonalCollectionView {
	return h.collectionView(r.Context(), c)
}
