package catalog

// Episode upsert artwork merge (Bloem). Artwork is the one column set in the
// episode upserts that the row may already own a better value for. still_path
// holds a cached object key once the image cache job has run; a provider
// refresh carries only the provider's own URL, because the provider has no
// idea we cached anything. Writing EXCLUDED unconditionally therefore replaces
// a working cached still with a raw provider URL, and the cached object
// becomes unreferenced -- which is how 804,490 episodes on one deployment lost
// their stills while every one of their cache jobs was recorded as succeeded.
//
// metadata.preserveCachedArtwork already applies this rule in Go, but only on
// the path where the caller successfully loaded the existing row first. That
// read comes from a prefetch map, so a miss produces a provider-only write
// that arrives here looking new. This is the same rule expressed where it
// cannot be bypassed: a cached path is never overwritten by a URL.
//
// still_source_path still takes EXCLUDED. The provider's source is
// authoritative, and a changed source is what makes the row a candidate for
// re-caching -- so the artwork refreshes through the cache pipeline rather
// than by serving the provider URL directly.
const (
	episodeStillPathMergeSQL = `CASE
				WHEN EXCLUDED.still_path LIKE '%://%'
					AND COALESCE(episodes.still_path, '') <> ''
					AND episodes.still_path NOT LIKE '%://%'
				THEN episodes.still_path
				ELSE EXCLUDED.still_path
			END`
	episodeStillThumbhashMergeSQL = `CASE
				WHEN EXCLUDED.still_path LIKE '%://%'
					AND COALESCE(episodes.still_path, '') <> ''
					AND episodes.still_path NOT LIKE '%://%'
				THEN episodes.still_thumbhash
				ELSE EXCLUDED.still_thumbhash
			END`
)
