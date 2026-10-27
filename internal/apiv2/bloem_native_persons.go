package apiv2

import "net/http"

// Person detail on the native surface, served by chi
// (handlers.PersonDetailHandler) in the native client group and mounted only
// when the deployment has a people repository.

// BloemPersonInput names the person to fetch.
type BloemPersonInput struct {
	PersonID string `path:"person_id" doc:"Person identity: a positive integer. Anything else answers 400."`
}

// BloemPersonFilmographyEntry is one item credited to a person. Kind is the
// item's own kind ("movie" or "series"), not the credit's: a client renders the
// destination, and the credit itself is Role.
type BloemPersonFilmographyEntry struct {
	ContentID string `json:"content_id" doc:"Item this credit points at."`
	Title     string `json:"title"`
	Kind      string `json:"kind" doc:"The item's kind, not the credit's." example:"movie"`
	Year      int    `json:"year,omitempty"`
	Role      string `json:"role,omitempty" doc:"How this person was credited: a character name or a job title."`
	PosterURL string `json:"poster_url,omitempty"`
}

// BloemPersonDetail is one person and everything they are credited in that this
// viewer may see.
type BloemPersonDetail struct {
	ID          string                        `json:"id"`
	Name        string                        `json:"name"`
	Bio         string                        `json:"bio,omitempty"`
	BirthDate   *string                       `json:"birth_date,omitempty" doc:"Calendar date (YYYY-MM-DD), when known."`
	DeathDate   *string                       `json:"death_date,omitempty" doc:"Calendar date (YYYY-MM-DD), when known."`
	Birthplace  string                        `json:"birthplace,omitempty"`
	PhotoURL    string                        `json:"photo_url,omitempty"`
	Filmography []BloemPersonFilmographyEntry `json:"filmography" doc:"At most 100 movies and series this viewer may see, newest year first. Empty, never null."`
}

// BloemPersonOutput is the person detail envelope.
type BloemPersonOutput struct {
	Body BloemPersonDetail
}

func registerBloemPersons(reg *Registry) {
	op := bloemClientDocumentOp(reg, "GET", "/persons/{person_id}", "getBloemPersonDetail", "catalog",
		"One person and their credits, filtered to what this viewer may see.", http.StatusOK)
	op.Description = "The person's own details are not profile-scoped; the filmography is filtered by the selected profile's libraries and rating limits."
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
		400: "bad_request: X-Profile-Id is missing or person_id is not a positive integer.",
		404: "not_found: no person has this ID, or X-Profile-Id names no profile of this account.",
		500: "internal_error: the viewer's access could not be resolved, or the person or filmography could not be loaded.",
	})
	registerBloemChiDocument[BloemPersonInput, BloemPersonOutput](reg, op)
}
