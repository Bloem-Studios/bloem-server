package userstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

const (
	PlaybackSourcePostgres = "postgres"
	PlaybackSourceSQLite   = "sqlite"
)

// PlaybackSourceRef identifies one account's exact durable source selection.
// A handle never follows a later selection or provisions a missing source.
type PlaybackSourceRef struct {
	Backend             string
	AccountID           int
	SourceID            string
	SelectionGeneration int64
}

func (r PlaybackSourceRef) Validate() error {
	if (r.Backend != PlaybackSourcePostgres && r.Backend != PlaybackSourceSQLite) || r.AccountID <= 0 || r.SelectionGeneration <= 0 {
		return ErrPlaybackSourceInvalid
	}
	id, err := uuid.Parse(r.SourceID)
	if err != nil || id == uuid.Nil || id.String() != r.SourceID {
		return fmt.Errorf("source ID must be a canonical nonzero UUID: %w", ErrPlaybackSourceInvalid)
	}
	return nil
}

type PlaybackSourceProvider interface {
	OpenPlaybackSink(context.Context, PlaybackSourceRef) (PlaybackSinkHandle, error)
}

// Close releases only this handle and waits for its in-flight operations.
// Operations after Close fail; closing one handle does not close its provider.
type PlaybackSinkHandle interface {
	PlaybackProgressSink
	Source() PlaybackSourceRef
	Close() error
}

var (
	ErrPlaybackSourceInvalid     = errors.New("invalid playback source reference")
	ErrPlaybackSourceUnavailable = errors.New("playback source unavailable")
	ErrPlaybackSourceMismatch    = errors.New("playback source identity mismatch")
	ErrPlaybackSourceUnbound     = errors.New("playback sink requires an exact source handle")
	ErrPlaybackSourceClosed      = errors.New("playback source handle closed")
	ErrPlaybackSourceDurability  = errors.New("playback source durability policy not satisfied")
)
