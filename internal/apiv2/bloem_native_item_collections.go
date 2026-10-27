package apiv2

import "net/http"

// Item collections: which server collections hold one title.
//
// Silo has no item-to-collections lookup; its clients find a title's
// collections by listing every collection and its items. This is Bloem's own
// answer for the detail page's "Part of a collection" row, so it lives on the
// native surface rather than adding a Bloem-only operation to /api/v2. It is
// served by chi (handlers.ItemCollectionsHandler) in the native client group.

// BloemItemCollectionsInput names the item whose collections to list.
type BloemItemCollectionsInput struct {
	ContentID string `path:"content_id" doc:"Content id of the item. A blank value answers 400." example:"movie:heat-1995"`
}

// BloemItemCollection is one visible server collection containing the item.
type BloemItemCollection struct {
	ID                string  `json:"id" example:"01J9Z8C3W4R5T6Y7U8I9O0P1Q2"`
	Title             string  `json:"title" example:"Oscar Winners"`
	CollectionType    string  `json:"collection_type" doc:"manual, mdblist, tmdb or trakt; smart collections are never listed." example:"manual"`
	LibraryID         string  `json:"library_id" doc:"The library the viewer reaches this collection through: the item's own library when the collection is scoped to it, else the first accessible one in library order." example:"1"`
	LibraryName       string  `json:"library_name" example:"Movies"`
	GroupID           *string `json:"group_id,omitempty" doc:"The collection group within that library, when it is grouped."`
	GroupName         string  `json:"group_name,omitempty"`
	Featured          bool    `json:"featured,omitempty"`
	PosterURL         string  `json:"poster_url" doc:"Presigned, short-lived; empty when none."`
	PosterThumbhash   string  `json:"poster_thumbhash,omitempty"`
	BackdropURL       string  `json:"backdrop_url,omitempty" doc:"Presigned, short-lived."`
	BackdropThumbhash string  `json:"backdrop_thumbhash,omitempty"`
	ItemCount         int     `json:"item_count" doc:"All members of the collection, as the server collection list counts them." example:"12"`
}

// BloemItemCollections is the item-collections document.
type BloemItemCollections struct {
	Collections []BloemItemCollection `json:"collections" doc:"At most 100, ordered by library order, then featured first, then collection order and title. Empty, never null."`
}

// BloemItemCollectionsOutput is the item-collections envelope.
type BloemItemCollectionsOutput struct {
	Body BloemItemCollections
}

func registerBloemItemCollections(reg *Registry) {
	op := bloemClientDocumentOp(reg, "GET", "/catalog/items/{content_id}/collections", "listBloemItemCollections", "catalog",
		"The visible server collections that contain one item. Visibility matches the server collection list; smart collections are evaluated lazily and are not included. An item the viewer may not see answers 404.", http.StatusOK)
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
		400: "bad_request: X-Profile-Id is missing or content_id is blank.",
		404: "not_found: the item does not exist or the selected profile may not see it, or X-Profile-Id names no profile of this account.",
		500: "internal_error: the viewer's access could not be resolved, or item access or the collections could not be read.",
	})
	registerBloemChiDocument[BloemItemCollectionsInput, BloemItemCollectionsOutput](reg, op)
}
