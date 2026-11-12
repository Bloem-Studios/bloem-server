package libraryingest

import (
	"context"
	"log/slog"
	"strconv"
	"sync"

	"github.com/Silo-Server/silo-server/internal/metadata"
	"github.com/Silo-Server/silo-server/internal/scanner"
)

// NativeSeriesMetadata is the metadata pipeline entry point for native series:
// the Process the admin match endpoints call, and the fallback episode
// synthesis the queued series matcher runs for a series no provider knows.
// Native series never enter the path-based series root queue, whose matcher
// reparses the opaque catalog key.
type NativeSeriesMetadata interface {
	NativeMovieMetadata
	SynthesizeFallbackEpisodes(ctx context.Context, seriesID string) error
}

// ConfigureNativeSeriesMatching is startup-only, before scans are admitted.
// Without it native series publish pending and wait for a manual match.
func (c *StorageScanner) ConfigureNativeSeriesMatching(meta NativeSeriesMetadata) {
	if c != nil {
		c.seriesMetadata = meta
	}
}

// seriesWork is one committed page's follow-up: series to match, matched
// series whose new files need linking, and unmatched series whose new files
// need fallback episodes.
type seriesWork struct {
	matches        []scanner.StorageSeriesMatch
	link, fallback []string
}

func (w seriesWork) empty() bool {
	return len(w.matches) == 0 && len(w.link) == 0 && len(w.fallback) == 0
}

// matchSeries runs a page's series follow-up with at most
// nativeMovieMatchWorkers concurrent provider calls. A failure leaves the
// series pending, so the next scan retries it.
func (c *StorageScanner) matchSeries(ctx context.Context, folderID int, work seriesWork, stats *nativeMovieMatchStats) {
	jobs := make(chan func())
	var workers sync.WaitGroup
	for range min(nativeMovieMatchWorkers, len(work.matches)+len(work.link)+len(work.fallback)) {
		workers.Go(func() {
			for job := range jobs {
				job()
			}
		})
	}
	send := func(job func()) bool {
		if ctx.Err() != nil {
			return false
		}
		jobs <- job
		return true
	}
	func() {
		for _, target := range work.matches {
			if !send(func() { c.matchOneSeries(ctx, folderID, target, stats) }) {
				return
			}
		}
		for _, id := range work.link {
			if !send(func() { c.linkSeries(ctx, folderID, id) }) {
				return
			}
		}
		for _, id := range work.fallback {
			if !send(func() { c.synthesizeSeries(ctx, folderID, id) }) {
				return
			}
		}
	}()
	close(jobs)
	workers.Wait()
}

func (c *StorageScanner) matchOneSeries(ctx context.Context, folderID int, target scanner.StorageSeriesMatch, stats *nativeMovieMatchStats) {
	if ctx.Err() != nil {
		return
	}
	matchCtx, cancel := context.WithTimeout(ctx, nativeMovieMatchTimeout)
	defer cancel()
	result, err := c.seriesMetadata.Process(matchCtx, nativeSeriesMatchRequest(folderID, target))
	switch {
	case err != nil:
		stats.failed.Add(1)
		if ctx.Err() == nil {
			slog.WarnContext(ctx, "storage series match failed; the next scan retries it", "component", "libraryingest",
				"folder_id", folderID, "content_id", target.ContentID, "error", err)
		}
	case result == nil:
	case result.Pinned:
		// An administrator left the series unmatched on purpose; its files
		// still get episodes, as the queued matcher gives them.
		c.synthesizeSeries(ctx, folderID, target.ContentID)
	case result.Updated:
		stats.matched.Add(1)
	default:
		stats.unmatched.Add(1)
		if err := c.scanner.MarkStorageSeriesUnmatched(ctx, target.ContentID); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "storage series unmatched status update failed", "component", "libraryingest",
				"folder_id", folderID, "content_id", target.ContentID, "error", err)
		}
		c.synthesizeSeries(ctx, folderID, target.ContentID)
	}
}

func (c *StorageScanner) linkSeries(ctx context.Context, folderID int, seriesID string) {
	if _, err := c.scanner.LinkStorageSeries(ctx, seriesID); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "storage series episode linking failed; the next change retries it", "component", "libraryingest",
			"folder_id", folderID, "content_id", seriesID, "error", err)
	}
}

func (c *StorageScanner) synthesizeSeries(ctx context.Context, folderID int, seriesID string) {
	if err := c.seriesMetadata.SynthesizeFallbackEpisodes(ctx, seriesID); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "storage series fallback episodes failed", "component", "libraryingest",
			"folder_id", folderID, "content_id", seriesID, "error", err)
	}
}

// nativeSeriesMatchRequest identifies a series by its show folder's provider
// tag exactly as an administrator's match/apply does (TVDB first, the ID the
// verified ClearStream folders carry), and searches with the folder's title
// and year otherwise.
func nativeSeriesMatchRequest(folderID int, target scanner.StorageSeriesMatch) metadata.ProcessRequest {
	request := metadata.ProcessRequest{ContentID: target.ContentID, FolderID: strconv.Itoa(folderID)}
	switch {
	case target.TvdbID != "":
		request.Mode, request.ProviderIDs = metadata.ModeIdentify, map[string]string{"tvdb": target.TvdbID}
	case target.TmdbID != "":
		request.Mode, request.ProviderIDs = metadata.ModeIdentify, map[string]string{"tmdb": target.TmdbID}
	default:
		request.Mode = metadata.ModeInitialMatch
		request.Hints = &metadata.MatchHints{ImdbID: target.ImdbID, Title: target.Title, Year: target.Year, Type: "series", HintSource: "scanner"}
	}
	return request
}
