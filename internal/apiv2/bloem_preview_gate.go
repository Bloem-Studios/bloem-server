package apiv2

import (
	"net/http"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

// previewAccess classifies a v2 operation for preview principals. The zero
// value denies, so an operation nobody listed, including every new upstream
// operation, is refused.
const previewEventsTicketOperation = "createEventsSocketTicket"

type previewAccess int

const (
	previewDenied previewAccess = iota
	// previewSafe operations serve every preview principal: boot, browse
	// and plumbing reads.
	previewSafe
	// previewPersonalOnly operations serve personal previews but not guests
	// (plan 4b): their own sessions and TV device approval.
	previewPersonalOnly
)

var previewOperations = map[string]previewAccess{
	// Boot.
	"getCurrentUser": previewSafe, "listProfiles": previewSafe,
	"getPlaybackCapabilities": previewSafe, "getEventsCapabilities": previewSafe,
	"getSettingsContract": previewSafe, "getSettingsContractCapabilities": previewSafe,
	"getUserLibraryCapabilities": previewSafe, "listUserLibraries": previewSafe,
	"getHomeCapabilities": previewSafe, "getCatalogSearchCapabilities": previewSafe,
	"getCollectionCapabilities": previewSafe, "getDownloadCapability": previewSafe,
	"getEbookCapability": previewSafe, "getTrailersCapability": previewSafe,
	"getRatingsCapability": previewSafe, "getThemeSongsCapability": previewSafe,
	"getImageCapabilities": previewSafe, "getOnboardingCapabilities": previewSafe,
	// Catalog.
	"queryCatalogItems": previewSafe, "queryCatalogLetters": previewSafe,
	"getCatalogFilters": previewSafe, "searchCatalogFacet": previewSafe,
	"getHomeLayout": previewSafe, "listHomeSections": previewSafe, "getHomeSectionItems": previewSafe,
	"listLibrarySections": previewSafe, "getLibrarySectionItems": previewSafe,
	"getLibraryCollections": previewSafe, "getLibraryCollectionItems": previewSafe,
	"getCatalogItem": previewSafe, "listCatalogItemEpisodes": previewSafe,
	"listSeriesSeasons": previewSafe, "getSeriesSeason": previewSafe, "listSeasonEpisodes": previewSafe,
	"getLiteraryWork": previewSafe, "listAudiobookGroups": previewSafe, "getCalendar": previewSafe,
	"listCollections": previewSafe, "getCollection": previewSafe, "getCollectionItems": previewSafe,
	"listServerCollections": previewSafe,
	// Recommendations: global rows only; For You, discover and taste are refused.
	"listPopular": previewSafe, "listRecentlyAdded": previewSafe, "listSimilar": previewSafe,
	// Plumbing.
	previewEventsTicketOperation: previewPersonalOnly, "logout": previewSafe,
	// Personal previews only.
	"listSessions": previewPersonalOnly, "getLoginSessionCapabilities": previewPersonalOnly,
	"deleteSession": previewPersonalOnly, "approveDeviceLogin": previewPersonalOnly,
	"denyDeviceLogin": previewPersonalOnly,
}

// previewGate refuses a preview principal an operation it may not run.
// Everyone else passes untouched.
func previewGate(operationID string) func(http.Handler) http.Handler {
	access := previewOperations[operationID]
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			guestRefused := access == previewPersonalOnly && apimw.IsPreviewGuest(ctx)
			if !apimw.IsPreviewPrincipal(ctx) || (access != previewDenied && !guestRefused) {
				next.ServeHTTP(w, r)
				return
			}
			apimw.WritePreviewOnly(w)
		})
	}
}
