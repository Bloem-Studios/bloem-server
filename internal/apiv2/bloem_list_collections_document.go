package apiv2

import (
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/listcollections"
)

type BloemListViewerInput struct{ BloemBookRequestViewerInput }
type BloemListIDInput struct {
	BloemListViewerInput
	ID string `path:"id" format:"uuid"`
}
type BloemListPreviewInput struct {
	BloemListViewerInput
	Body handlers.BloemListPreviewCommand
}
type BloemListCreateInput struct {
	BloemListViewerInput
	Body handlers.BloemListCreateCommand
}
type BloemListJob struct {
	JobID string `json:"job_id"`
}
type BloemListDisconnect struct {
	Status string `json:"status"`
}
type BloemListCapabilityOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         handlers.BloemListCapability
}
type BloemListPreviewOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         listcollections.PreviewStatus
}
type BloemListBindingOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         handlers.BloemListBindingStatus
}
type BloemListJobOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         BloemListJob
}
type BloemListDisconnectOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         BloemListDisconnect
}

func registerBloemListCollectionsDocument(reg *Registry) {
	errors := map[int]string{400: "invalid_list_source: malformed input or an unconfigured source origin.", 403: "list_denied: source or collection unavailable to this profile.", 409: "list_conflict or preview_expired: changed or expired source preview.", 503: "list_unavailable: list synchronization unavailable."}
	capability := bloemChiDocumentOp(reg, "GET", "/list-collections/capabilities", "getBloemListCollectionCapabilities", "collections", "List this profile's configured Pastime list connections.", "bearerAuth", 200)
	bloemDocumentErrors[BloemNativeError](reg, &capability, errors)
	registerBloemChiDocument[BloemListViewerInput, BloemListCapabilityOutput](reg, capability)
	preview := bloemChiDocumentOp(reg, "POST", "/list-collections/previews", "previewBloemListCollection", "collections", "Resolve a configured Pastime list URL to a short-lived authorized preview.", "bearerAuth", 202)
	bloemDocumentErrors[BloemNativeError](reg, &preview, errors)
	registerBloemChiDocument[BloemListPreviewInput, BloemListJobOutput](reg, preview)
	getPreview := bloemChiDocumentOp(reg, "GET", "/list-collections/previews/{id}", "getBloemListCollectionPreview", "collections", "Read this profile's list preview without revealing source credentials.", "bearerAuth", 200)
	bloemDocumentErrors[BloemNativeError](reg, &getPreview, errors)
	registerBloemChiDocument[BloemListIDInput, BloemListPreviewOutput](reg, getPreview)
	create := bloemChiDocumentOp(reg, "POST", "/list-collections/bindings", "createBloemListCollection", "collections", "Create a personal, shared personal or administrator server collection from a live preview.", "bearerAuth", 202)
	create.Description = "Membership follows the source order. Missing titles use native request policy and quota with the creating profile as requester. Availability does not imply ownership. Refresh failures retain the last successful membership."
	bloemDocumentErrors[BloemNativeError](reg, &create, errors)
	registerBloemChiDocument[BloemListCreateInput, BloemListBindingOutput](reg, create)
	get := bloemChiDocumentOp(reg, "GET", "/list-collections/bindings/{id}", "getBloemListCollectionBinding", "collections", "Read membership refresh status and separate native request outcomes.", "bearerAuth", 200)
	bloemDocumentErrors[BloemNativeError](reg, &get, errors)
	registerBloemChiDocument[BloemListIDInput, BloemListBindingOutput](reg, get)
	refresh := bloemChiDocumentOp(reg, "POST", "/list-collections/bindings/{id}/refresh", "refreshBloemListCollection", "collections", "Queue a refresh, coalescing an existing active job.", "bearerAuth", 202)
	bloemDocumentErrors[BloemNativeError](reg, &refresh, errors)
	registerBloemChiDocument[BloemListIDInput, BloemListJobOutput](reg, refresh)
	disconnect := bloemChiDocumentOp(reg, "POST", "/list-collections/bindings/{id}/disconnect", "disconnectBloemListCollection", "collections", "Restore manual editing and cancel unsent requests while retaining membership and issued requests.", "bearerAuth", 200)
	bloemDocumentErrors[BloemNativeError](reg, &disconnect, errors)
	registerBloemChiDocument[BloemListIDInput, BloemListDisconnectOutput](reg, disconnect)
}
