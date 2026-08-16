// Package watchdoc composes the Watch documents the TV clients consume.
//
// A Watch document is the contracts' watch_document_v1 shape: one snapshot of
// what a profile may watch, carrying the items, the profile's progress rows for
// those items, and a single server-chosen featured content identifier. Serving
// the composition rather than the joins keeps the first frame of a television
// home screen to one request, and keeps the editorial featured choice on the
// server instead of in whichever client implemented it last.
//
// The clients' validators are strict, and a document that fails one is rejected
// whole rather than partially rendered. This package therefore owns the
// invariants rather than trusting its inputs:
//
//   - every episode item carries series_id, season_number, episode_number and a
//     positive file_id;
//   - season and episode numbers are at least one, so specials never reach a
//     validator that requires a positive number;
//   - a content identifier appears at most once, and a file identifier at most
//     once;
//   - an item that is itself playable but has no playable file is omitted
//     rather than emitted with a zero file_id;
//   - a progress row is emitted only for an item the document lists, so a
//     restricted item cannot be named through the progress array.
//
// The document deliberately carries no layout, view tree or focus order: the
// featured identifier is a name, and the client's own presentation rules decide
// what to do with it.
package watchdoc

// SchemaWatchDocumentV1 is the schema token every Watch document carries, and
// the capability token clients feature-detect on.
const SchemaWatchDocumentV1 = "watch_document_v1"

// Item kinds. These are the contract's enum, not the catalog's media_items.type
// vocabulary, and are the only kinds a Watch document may carry.
const (
	KindMovie   = "movie"
	KindSeries  = "series"
	KindEpisode = "episode"
)

// Document is the wire shape of watch_document_v1. Field names and omission
// rules match the contracts schema exactly; unknown fields are permitted by the
// schema, but nothing here emits one the clients were not built against.
type Document struct {
	Schema   string `json:"schema"`
	Snapshot string `json:"snapshot"`
	// FeaturedContentID names an item present in Items. It is omitted rather
	// than emitted empty: the schema requires a non-empty string when present,
	// and an empty library has nothing to feature.
	FeaturedContentID string             `json:"featured_content_id,omitempty"`
	Items             []DocumentItem     `json:"items"`
	Progress          []DocumentProgress `json:"progress"`
}

// DocumentItem is one entry in a Watch document. The episode-only fields carry
// `omitempty` because the schema requires them for episodes and the clients
// treat their presence as the signal that an item is playable in place.
type DocumentItem struct {
	Kind      string `json:"kind"`
	ContentID string `json:"content_id"`
	Title     string `json:"title"`

	Year           int      `json:"year,omitempty"`
	RuntimeSeconds int      `json:"runtime_seconds,omitempty"`
	Rating         string   `json:"rating,omitempty"`
	Overview       string   `json:"overview,omitempty"`
	Genres         []string `json:"genres,omitempty"`
	Keywords       []string `json:"keywords,omitempty"`

	// PosterURL is a resolved, directly fetchable URL — never a stored path or
	// a plugin-prefixed reference. The Reader resolves it before this package
	// ever sees the item, the same way it resolves everything else here: this
	// package composes a document from plain data, and never itself dials a
	// plugin, a CDN or a database. Omitted when the item has no poster.
	PosterURL string `json:"poster_url,omitempty"`

	// Series-only.
	SeasonCount int              `json:"season_count,omitempty"`
	Seasons     []DocumentSeason `json:"seasons,omitempty"`

	// Episode-only. Required together by the contract, and never emitted
	// partially.
	SeriesID      string `json:"series_id,omitempty"`
	SeasonNumber  int    `json:"season_number,omitempty"`
	EpisodeNumber int    `json:"episode_number,omitempty"`

	// FileID is the media file the client sends to /playback/start. Zero is
	// never emitted: an item with no playable file is dropped instead.
	FileID int64 `json:"file_id,omitempty"`

	// Featured marks the item the document's featured_content_id names, so a
	// client that renders the array without cross-referencing still agrees with
	// the server's choice.
	Featured bool `json:"featured,omitempty"`
}

// DocumentSeason is a season summary on a series item.
type DocumentSeason struct {
	SeasonNumber int    `json:"season_number"`
	Title        string `json:"title,omitempty"`
}

// DocumentProgress is one durable progress row for the requesting profile.
//
// ContentID is the item the row belongs to — the series for an episode, so the
// row is attributable through the document that named it — and EpisodeID names
// the episode itself. The server's stored row carries neither series linkage
// nor a file identifier; both come from the document's items.
type DocumentProgress struct {
	ContentID       string  `json:"content_id"`
	EpisodeID       string  `json:"episode_id,omitempty"`
	PositionSeconds float64 `json:"position_seconds"`
	DurationSeconds float64 `json:"duration_seconds"`
	Completed       bool    `json:"completed"`
	UpdatedAt       string  `json:"updated_at"`
}
