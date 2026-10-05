package sections

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

// SectionFetchRequest preserves host-resolved identity and library authority.
type SectionFetchRequest struct {
	Section    ResolvedSection
	LibraryID  *int
	LibraryIDs []int
	UserID     int
	ProfileID  string
	Access     catalog.AccessFilter
}

// SectionResolver resolves an extension using host-scoped facts.
// Configure the registry before requests start; never mutate it concurrently.
type SectionResolver func(context.Context, SectionFetchRequest) (SectionWithItems, error)
