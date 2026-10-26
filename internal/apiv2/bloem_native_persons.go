package apiv2

import "context"

// Person detail on the native surface.

// BloemPersonInput names the person to fetch.
type BloemPersonInput struct {
	PersonID string `path:"person_id" doc:"Person identity."`
}

// BloemPersonFilmographyEntry is one item credited to a person. Kind is the
// item's own kind ("movie" or "series"), not the credit's: a client renders the
// destination, and the credit itself is Role.
type BloemPersonFilmographyEntry struct {
	ContentID string `json:"content_id" doc:"Item this credit points at."`
	Title     string `json:"title"`
	Kind      string `json:"kind" doc:"The item's kind, not the credit's." example:"movie"`
	Year      int    `json:"year,omitempty"`
	Role      string `json:"role,omitempty" doc:"How this person was credited."`
	PosterURL string `json:"poster_url,omitempty"`
}

// BloemPersonDetail is one person and everything they are credited in that this
// viewer may see.
type BloemPersonDetail struct {
	ID          string                        `json:"id"`
	Name        string                        `json:"name"`
	Bio         string                        `json:"bio,omitempty"`
	BirthDate   *string                       `json:"birth_date,omitempty" doc:"RFC 3339 date, when known."`
	DeathDate   *string                       `json:"death_date,omitempty" doc:"RFC 3339 date, when known."`
	Birthplace  string                        `json:"birthplace,omitempty"`
	PhotoURL    string                        `json:"photo_url,omitempty"`
	Filmography []BloemPersonFilmographyEntry `json:"filmography" doc:"Credits filtered to what this viewer may see."`
}

// BloemPersonOutput is the person detail envelope.
type BloemPersonOutput struct {
	Body BloemPersonDetail
}

func registerBloemPersons(reg *Registry) {
	Register(reg, Operation{
		Operation: bloemOp("GET", "/persons/{person_id}", "getBloemPersonDetail", "catalog",
			"One person and their credits, filtered to what this viewer may see."),
		Class: ClassProfileScoped,
	}, func(context.Context, *BloemPersonInput) (*BloemPersonOutput, error) {
		return &BloemPersonOutput{}, nil
	})
}
