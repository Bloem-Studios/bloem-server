package apiv2

import (
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/google/uuid"
)

// BloemSourceBrowseInput documents the owned administrative GET contract. The
// library-location registrar adds it alongside its capability and routes.
type BloemSourceBrowseInput struct {
	SourceKey    uuid.UUID `path:"source_key" doc:"Retained authorized storage source UUID."`
	DirectoryRef string    `query:"directory_ref" required:"false" maxLength:"4096" doc:"Opaque host directory reference: a selection_ref from a previous page (current, breadcrumbs or directories). Omit to start at the retained configured root. Never a filesystem path or provider URL."`
	Cursor       string    `query:"cursor" required:"false" maxLength:"4096" doc:"Opaque next_cursor from the previous page, sent with the same directory_ref and limit. A cursor without directory_ref is refused; to page the configured root, send that page's current.selection_ref as directory_ref. Combined encoded query is at most 6144 bytes. Expired, evicted or changed pages require restarting browsing."`
	Limit        int       `query:"limit" required:"false" default:"50" minimum:"1" maximum:"100"`
}
type BloemSourceBrowseOutput struct{ Body nativestorage.BrowsePage }
