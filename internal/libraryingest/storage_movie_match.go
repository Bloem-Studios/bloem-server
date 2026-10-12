package libraryingest

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Silo-Server/silo-server/internal/metadata"
	"github.com/Silo-Server/silo-server/internal/scanner"
)

// NativeMovieMetadata is the metadata pipeline entry point. The admin match
// endpoints call the same Process; native movies never enter the path-based
// match queues, whose matcher reads identity from the opaque catalog key.
type NativeMovieMetadata interface {
	Process(ctx context.Context, req metadata.ProcessRequest) (*metadata.ProcessResult, error)
}

const (
	// nativeMovieMatchWorkers bounds one scan's concurrent provider matches.
	nativeMovieMatchWorkers = 4
	nativeMovieMatchTimeout = 2 * time.Minute
)

// ConfigureNativeMovieMatching is startup-only, before scans are admitted.
// Without it native movies publish pending and wait for a manual match.
func (c *StorageScanner) ConfigureNativeMovieMatching(meta NativeMovieMetadata) {
	if c != nil {
		c.movieMetadata = meta
	}
}

type nativeMovieMatchStats struct{ matched, unmatched, failed atomic.Int64 }

func (s *nativeMovieMatchStats) log(ctx context.Context, folderID int) {
	matched, unmatched, failed := s.matched.Load(), s.unmatched.Load(), s.failed.Load()
	if matched+unmatched+failed > 0 {
		slog.InfoContext(ctx, "storage movie matching finished", "component", "libraryingest", "folder_id", folderID,
			"matched", matched, "unmatched", unmatched, "failed", failed)
	}
}

// matchMovies applies provider matches to the items a committed page left
// pending. Folder or file provider tags apply exactly as an administrator's
// match/apply does; other titles search with their parsed title and year. A
// failure leaves the item pending, so the next scan retries it.
func (c *StorageScanner) matchMovies(ctx context.Context, folderID int, targets []scanner.StorageMovieMatch, stats *nativeMovieMatchStats) {
	work := make(chan scanner.StorageMovieMatch)
	var workers sync.WaitGroup
	for range min(nativeMovieMatchWorkers, len(targets)) {
		workers.Go(func() {
			for target := range work {
				c.matchMovie(ctx, folderID, target, stats)
			}
		})
	}
	for _, target := range targets {
		if ctx.Err() != nil {
			break
		}
		work <- target
	}
	close(work)
	workers.Wait()
}

func (c *StorageScanner) matchMovie(ctx context.Context, folderID int, target scanner.StorageMovieMatch, stats *nativeMovieMatchStats) {
	if ctx.Err() != nil {
		return
	}
	matchCtx, cancel := context.WithTimeout(ctx, nativeMovieMatchTimeout)
	defer cancel()
	result, err := c.movieMetadata.Process(matchCtx, nativeMovieMatchRequest(folderID, target))
	switch {
	case err != nil:
		stats.failed.Add(1)
		if ctx.Err() == nil {
			slog.WarnContext(ctx, "storage movie match failed; the next scan retries it", "component", "libraryingest",
				"folder_id", folderID, "content_id", target.ContentID, "error", err)
		}
	case result == nil || result.Pinned:
	case result.Updated:
		stats.matched.Add(1)
	default:
		stats.unmatched.Add(1)
		if err := c.scanner.MarkStorageMovieUnmatched(ctx, target.ContentID); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "storage movie unmatched status update failed", "component", "libraryingest",
				"folder_id", folderID, "content_id", target.ContentID, "error", err)
		}
	}
}

// nativeMovieMatchRequest builds the same request match/apply sends for a
// provider tag (TMDB first, the movie identity Silo anchors on), and the
// queued matcher's initial-match hints otherwise.
func nativeMovieMatchRequest(folderID int, target scanner.StorageMovieMatch) metadata.ProcessRequest {
	request := metadata.ProcessRequest{ContentID: target.ContentID, FolderID: strconv.Itoa(folderID)}
	switch {
	case target.TmdbID != "":
		request.Mode, request.ProviderIDs = metadata.ModeIdentify, map[string]string{"tmdb": target.TmdbID}
	default:
		alternates := make([]metadata.MatchIdentityHint, 0, len(target.Alternates))
		for _, alternate := range target.Alternates {
			alternates = append(alternates, metadata.MatchIdentityHint{Title: alternate.Title, Year: alternate.Year, Source: alternate.Source})
		}
		request.Mode = metadata.ModeInitialMatch
		request.Hints = &metadata.MatchHints{ImdbID: target.ImdbID, Title: target.Title, Year: target.Year, Type: "movie", HintSource: "scanner", AlternateIdentities: alternates}
	}
	return request
}
