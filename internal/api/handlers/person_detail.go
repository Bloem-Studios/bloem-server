package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

// personFilmographyLimit bounds the filmography a person-detail document
// carries — the same "first frame" tradeoff watchHomeItemLimit makes for the
// Watch home document: a person credited on hundreds of items gets their most
// recent (by year) work, not a paginated catalog.
const personFilmographyLimit = 100

// personDetailPhotoVariant is the image size hint a person's own photo asks
// for — the same "featured" crop Watch credits (CatalogWatchReader.Credits)
// and the admin person endpoint (PeopleHandler.toResponse) already resolve a
// person's photo at.
const personDetailPhotoVariant = "featured"

// personFilmographyPosterVariant is the image size hint a filmography entry's
// poster asks for: a card in a scrolling list, the same variant Watch
// documents ask for (watchPosterVariant).
const personFilmographyPosterVariant = "card"

// PersonDetailPersonSource resolves a single person by id.
type PersonDetailPersonSource interface {
	Get(ctx context.Context, id int64) (*models.Person, error)
}

// PersonDetailBrowseSource lists the movies and series a profile may see,
// optionally filtered to those crediting a given person. It is the same
// capability WatchBrowseSource uses for the Watch home document.
type PersonDetailBrowseSource interface {
	BrowsePage(ctx context.Context, filters catalog.BrowseFilters, includeTotal bool) (*catalog.BrowseResult, error)
}

// PersonDetailRoleSource resolves a person's credited role (character name or
// job title) on a set of items already known to be visible to the viewer.
type PersonDetailRoleSource interface {
	RolesForItems(ctx context.Context, personID int64, contentIDs []string) (map[string]catalog.PersonItemRole, error)
}

// PersonDetailHandler serves the native person-detail document: a cast/crew
// member's bio plus the filmography of items they are credited on that the
// requesting profile may see.
//
// A person's identity (name, bio, photo) is not profile-scoped — anyone who
// can reach the endpoint may read it — but the filmography is: a hidden or
// restricted library must not leak through a person's credits any more than
// it would through browse or search. filmography enforces that the same way
// every other browse surface does, by running the viewer's library and
// content-rating predicates inside the same query as the person join (see
// catalog.BrowseFilters.PersonID), rather than filtering the result
// afterward.
type PersonDetailHandler struct {
	persons PersonDetailPersonSource
	browse  PersonDetailBrowseSource
	roles   PersonDetailRoleSource
	images  catalog.ImageResolver
}

// NewPersonDetailHandler creates a PersonDetailHandler. browse and roles may
// each be nil, in which case the document carries an empty filmography rather
// than failing to compose. images may be nil, in which case the document
// carries no photo or poster URLs rather than a stored path or a
// plugin-prefixed reference no client can fetch — the same optional-source
// convention CatalogWatchReader uses.
func NewPersonDetailHandler(
	persons PersonDetailPersonSource,
	browse PersonDetailBrowseSource,
	roles PersonDetailRoleSource,
	images catalog.ImageResolver,
) *PersonDetailHandler {
	return &PersonDetailHandler{persons: persons, browse: browse, roles: roles, images: images}
}

type personDetailResponse struct {
	ID          string                   `json:"id"`
	Name        string                   `json:"name"`
	Bio         string                   `json:"bio,omitempty"`
	BirthDate   *string                  `json:"birth_date,omitempty"`
	DeathDate   *string                  `json:"death_date,omitempty"`
	Birthplace  string                   `json:"birthplace,omitempty"`
	PhotoURL    string                   `json:"photo_url,omitempty"`
	Filmography []personFilmographyEntry `json:"filmography"`
}

// personFilmographyEntry is one item credited to the person the document
// names. Kind is the item's own kind ("movie" or "series") — what a client
// needs to route a tap to the right detail screen — and Role is the person's
// credit on that specific item: a character name for an acting credit, or a
// job title (e.g. "Director") for a non-acting one.
type personFilmographyEntry struct {
	ContentID string `json:"content_id"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Year      int    `json:"year,omitempty"`
	Role      string `json:"role,omitempty"`
	PosterURL string `json:"poster_url,omitempty"`
}

// HandleGetPersonDetail handles GET /api/v2/persons/{person_id}.
func (h *PersonDetailHandler) HandleGetPersonDetail(w http.ResponseWriter, r *http.Request) {
	if h.persons == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Person detail is not available")
		return
	}
	idStr := strings.TrimSpace(chi.URLParam(r, "person_id"))
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "person_id must be a positive integer")
		return
	}

	person, err := h.persons.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "Person not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load person")
		return
	}
	if person == nil {
		writeError(w, http.StatusNotFound, "not_found", "Person not found")
		return
	}

	resp := personDetailResponse{
		ID:         strconv.FormatInt(person.ID, 10),
		Name:       person.Name,
		Bio:        person.Bio,
		Birthplace: person.Birthplace,
	}
	if person.BirthDate != nil {
		s := person.BirthDate.Format("2006-01-02")
		resp.BirthDate = &s
	}
	if person.DeathDate != nil {
		s := person.DeathDate.Format("2006-01-02")
		resp.DeathDate = &s
	}
	if h.images != nil && person.PhotoPath != "" && person.PhotoPath != "-" {
		resp.PhotoURL = h.images.ResolveImageURL(r.Context(), person.PhotoPath, personDetailPhotoVariant)
	}

	filmography, err := h.filmography(r, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load filmography")
		return
	}
	resp.Filmography = filmography

	writeJSON(w, http.StatusOK, resp)
}

// filmography lists the movies and series credited to personID that the
// requesting profile may see, sorted by year descending.
func (h *PersonDetailHandler) filmography(r *http.Request, personID int64) ([]personFilmographyEntry, error) {
	if h.browse == nil {
		return []personFilmographyEntry{}, nil
	}
	filter := requestAccessFilter(r)
	result, err := h.browse.BrowsePage(r.Context(), catalog.BrowseFilters{
		Type:               itemTypeMovie + "," + itemTypeSeries,
		PersonID:           personID,
		LibraryIDs:         filter.AllowedLibraryIDs,
		DisabledLibraryIDs: filter.DisabledLibraryIDs,
		MaxContentRating:   filter.MaxContentRating,
		Sort:               "year",
		Order:              "desc",
		Limit:              personFilmographyLimit,
	}, false)
	if err != nil {
		return nil, err
	}
	if result == nil || len(result.Items) == 0 {
		return []personFilmographyEntry{}, nil
	}

	contentIDs := make([]string, 0, len(result.Items))
	posterPaths := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		if item == nil {
			continue
		}
		contentIDs = append(contentIDs, item.ContentID)
		if item.PosterPath != "" {
			posterPaths = append(posterPaths, item.PosterPath)
		}
	}

	var roles map[string]catalog.PersonItemRole
	if h.roles != nil {
		roles, err = h.roles.RolesForItems(r.Context(), personID, contentIDs)
		if err != nil {
			return nil, err
		}
	}

	var posterURLs map[string]string
	if h.images != nil && len(posterPaths) > 0 {
		posterURLs = h.images.ResolveImageURLs(r.Context(), posterPaths, personFilmographyPosterVariant)
	}

	// BrowsePage already orders by year descending (see its "year" sort case),
	// so the result is walked in that order rather than re-sorted here.
	entries := make([]personFilmographyEntry, 0, len(result.Items))
	for _, item := range result.Items {
		if item == nil {
			continue
		}
		entry := personFilmographyEntry{
			ContentID: item.ContentID,
			Title:     item.Title,
			Kind:      item.Type,
			Year:      item.Year,
		}
		if credit, ok := roles[item.ContentID]; ok {
			entry.Role = personCreditRole(credit)
		}
		if posterURLs != nil {
			entry.PosterURL = posterURLs[item.PosterPath]
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// personCreditRole renders a credit the same way Watch documents render one
// (CatalogWatchReader.Credits): an acting credit's character name, or a
// non-acting credit's job title.
func personCreditRole(credit catalog.PersonItemRole) string {
	switch credit.Kind {
	case models.PersonKindActor, models.PersonKindGuestStar:
		return credit.Character
	default:
		return credit.Kind.String()
	}
}
