package listcollections

import "testing"

func TestListFollowIdentityMatching(t *testing.T) {
	catalog := []Candidate{
		{ContentID: "a", Kind: "movie", ExternalIDs: map[string]string{"tmdb": "42", "imdb": "tt0000123"}},
		{ContentID: "b", Kind: "series", ExternalIDs: map[string]string{"tmdb": "42"}},
		{ContentID: "c", Kind: "movie", ExternalIDs: map[string]string{"tmdb": "43"}},
	}
	in := []Entry{
		{Kind: "movie", ExternalIDs: map[string]string{"tmdb": "43"}},
		{Kind: "movie", ExternalIDs: map[string]string{"imdb": "tt0000123"}},
		{Kind: "movie", ExternalIDs: map[string]string{"tmdb": "42"}},
		{Kind: "series", ExternalIDs: map[string]string{"tmdb": "42"}},
		{Kind: "movie", Title: "Title must not match", ExternalIDs: map[string]string{"tmdb": "99"}},
		{Kind: "movie", ExternalIDs: map[string]string{"tmdb": "42", "imdb": "tt0000999"}},
		{Kind: "unsupported", Title: "Unknown", SourceEntryID: "native:1", UnsupportedReason: "missing_identity"},
	}
	got, err := MatchEntries(in, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ContentIDs) != 3 || got.ContentIDs[0] != "c" || got.ContentIDs[1] != "a" || got.ContentIDs[2] != "b" {
		t.Fatal("ordering/kind/deduplication", got)
	}
	if len(got.Unmatched) != 3 || got.Unmatched[0].Position != 4 || got.Unmatched[0].Reason != "missing" || got.Unmatched[1].Reason != "conflicting_identity" || got.Unmatched[2].Reason != "missing_identity" {
		t.Fatal("unmatched classification", got)
	}
}

func TestListFollowAmbiguousCatalogIdentityIsRefused(t *testing.T) {
	in := []Entry{{Kind: "ebook", ExternalIDs: map[string]string{"isbn": "9780306406157"}}}
	got, err := MatchEntries(in, []Candidate{
		{ContentID: "edition-a", Kind: "ebook", ExternalIDs: in[0].ExternalIDs},
		{ContentID: "edition-b", Kind: "ebook", ExternalIDs: in[0].ExternalIDs},
	})
	if err != nil || len(got.ContentIDs) != 0 || len(got.Unmatched) != 1 || got.Unmatched[0].Reason != "ambiguous_identity" {
		t.Fatal(got, err)
	}
}

func TestListFollowConflictingProvidersNeverChooseOne(t *testing.T) {
	in := []Entry{{Kind: "movie", ExternalIDs: map[string]string{"tmdb": "42", "imdb": "tt0000999"}}}
	got, err := MatchEntries(in, []Candidate{
		{ContentID: "a", Kind: "movie", ExternalIDs: map[string]string{"tmdb": "42"}},
		{ContentID: "b", Kind: "movie", ExternalIDs: map[string]string{"imdb": "tt0000999"}},
	})
	if err != nil || len(got.ContentIDs) != 0 || len(got.Unmatched) != 1 || got.Unmatched[0].Reason != "conflicting_identity" {
		t.Fatal(got, err)
	}
}
