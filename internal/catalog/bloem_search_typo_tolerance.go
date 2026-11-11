package catalog

// Bloem: forgive one typo in words of three letters or more.
//
// Meilisearch forgives one typo only from five letters, so "mda max" or
// "the wrie" never matched a title. On a scratch copy of the live catalog
// (2026-11-11) a typo in a 3-4 letter word put the right title in the top 10
// for 60.5% of searches at five letters and 86.8% at three (with the
// finished-word name query of bloem_search_title_boost.go), while exact
// titles, long-word typos and descriptive searches stayed the same: exact
// matches still rank above typo matches.
//
// The setting is search-time only: changing it needs no reindex, and it is not
// part of the schema version, so it never forces a rebuild. Indexes built
// before this change get it from a one-time settings update.

const (
	bloemOneTypoMinWordSize  = 3
	bloemTwoTyposMinWordSize = 9 // Meilisearch's default, unchanged
)

func withBloemSearchSettings(settings map[string]any) map[string]any {
	settings["typoTolerance"] = map[string]any{
		"minWordSizeForTypos": map[string]any{
			"oneTypo":  bloemOneTypoMinWordSize,
			"twoTypos": bloemTwoTyposMinWordSize,
		},
	}
	return settings
}
