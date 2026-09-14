package apiv2

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/watchdoc"
)

// The Watch surface and offline progress sync: what a television renders, and
// how a client reconciles playback positions it recorded while offline.
//
// Both are Bloem-only. The Watch document composes a whole home screen in one
// response rather than making a client assemble it from several catalog calls,
// and the sync endpoint takes a batch with per-item results instead of failing
// the whole request on one bad entry. Neither shape exists upstream.
//
// The Watch bodies reuse watchdoc.Document directly. The sync shapes restate
// handlers' unexported request and response types, held in step by
// TestBloemSyncProgressDocumentMatchesTheServedShape.

// BloemWatchDocumentOutput is a composed Watch document.
type BloemWatchDocumentOutput struct {
	Body watchdoc.Document
}

// BloemWatchItemInput names the item to compose a document for.
type BloemWatchItemInput struct {
	ContentID string `path:"content_id" doc:"Catalog content identity."`
}

// BloemWatchSearchInput carries the query a search document is composed for.
type BloemWatchSearchInput struct {
	Query string `query:"q" doc:"Search terms." required:"true"`
}

// BloemSyncProgressItem is one playback position a client recorded, possibly
// while offline.
type BloemSyncProgressItem struct {
	MediaItemID    string  `json:"media_item_id" doc:"Item this position belongs to."`
	Position       float64 `json:"position" doc:"Playback position in seconds."`
	Duration       float64 `json:"duration" doc:"Total runtime in seconds, as the client observed it."`
	ForceOverwrite bool    `json:"force_overwrite" doc:"Replace a newer server-side position rather than losing to it."`
	// UpdatedAt is the client's EVENT time, not the time of the request. An
	// offline queue replays hours later, so without it every replayed item
	// would look newer than the server's state and win a last-write-wins
	// comparison it should lose. The server clamps it to now plus a skew
	// allowance and uses it only as the comparison key.
	UpdatedAt *string `json:"updated_at,omitempty" doc:"RFC 3339 time the client recorded this position. Omit for a live update." required:"false"`
}

// BloemSyncProgressInput is a batch of recorded positions.
type BloemSyncProgressInput struct {
	Body struct {
		Items []BloemSyncProgressItem `json:"items" doc:"Positions to reconcile."`
	}
}

// BloemSyncProgressResult is one item's outcome. Results are per item because a
// batch must not fail wholesale on one bad entry: a client replaying an offline
// queue would otherwise have no way to make progress past it.
type BloemSyncProgressResult struct {
	MediaItemID string `json:"media_item_id" doc:"Item this result belongs to."`
	Status      string `json:"status" doc:"Outcome for this item."`
	Error       string `json:"error,omitempty" doc:"Why this item was not applied, when it was not."`
}

// BloemSyncProgressOutput carries one result per submitted item.
type BloemSyncProgressOutput struct {
	Body struct {
		Results []BloemSyncProgressResult `json:"results"`
	}
}

func registerBloemWatch(reg *Registry) {
	Register(reg, Operation{
		Operation: bloemOp("GET", "/watch/home", "getBloemWatchHome", "watch",
			"The composed home document for this viewer."),
		Class: ClassProfileScoped,
	}, func(context.Context, *struct{}) (*BloemWatchDocumentOutput, error) {
		return &BloemWatchDocumentOutput{}, nil
	})

	Register(reg, Operation{
		Operation: bloemOp("GET", "/watch/items/{content_id}", "getBloemWatchItem", "watch",
			"The composed document for one item."),
		Class: ClassProfileScoped,
	}, func(context.Context, *BloemWatchItemInput) (*BloemWatchDocumentOutput, error) {
		return &BloemWatchDocumentOutput{}, nil
	})

	Register(reg, Operation{
		Operation: bloemOp("GET", "/watch/search", "searchBloemWatch", "watch",
			"A composed document for a search query."),
		Class: ClassProfileScoped,
	}, func(context.Context, *BloemWatchSearchInput) (*BloemWatchDocumentOutput, error) {
		return &BloemWatchDocumentOutput{}, nil
	})

	Register(reg, Operation{
		Operation: bloemOp("POST", "/sync/progress", "syncBloemProgress", "watch",
			"Reconcile playback positions, including ones recorded offline."),
		Class: ClassProfileScoped,
		// Replaying the same batch converges on the same state: positions are
		// reconciled last-write-wins against UpdatedAt, not accumulated. An
		// offline queue that retries after an uncertain response is the normal
		// case rather than an error.
		RetrySafety: RetrySafetyNaturalIdempotent,
	}, func(context.Context, *BloemSyncProgressInput) (*BloemSyncProgressOutput, error) {
		return &BloemSyncProgressOutput{}, nil
	})
}
