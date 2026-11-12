package libraryingest

import (
	"context"
	"slices"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"google.golang.org/protobuf/proto"
)

// ConfigureNativeSeries is startup-only, after ConfigureNativeMovies and before
// scans are admitted. Episodes are probed with the movie reader and limits, so
// the series lane is ready only where the movie lane is. False is the default
// and holds every TV library at scan and resource admission.
func (c *StorageScanner) ConfigureNativeSeries(ready bool) error {
	if c == nil || c.resources == nil {
		return storagesource.ErrSourceUnavailable
	}
	if ready && (!c.nativeMovieReady || c.movieReader == nil) {
		return scanner.ErrNativeProbeInvalid
	}
	c.nativeSeriesReady = ready
	c.resources.SetNativeSeriesReady(ready)
	if c.movieReader != nil {
		c.movieReader.SetNativeSeriesReady(ready)
	}
	return nil
}

// preparedSeries is one TV page ready to publish: its episode files (probed,
// or unchanged and not read) and the subtitle sidecars it lists.
type preparedSeries struct {
	episodes []scanner.StorageMovie
	sidecars []scanner.StorageSeriesSidecar
	skipped  int
	excluded []string
}

// prepareSeries sorts a TV page's files. Subtitle sidecars are kept for their
// episode; extras, samples, zero-byte objects and files without an episode
// number are neither opened nor published, and a file an older scan published
// goes missing. Only new or changed episodes are probed, within the scan's
// probe budget and its source's probe rate; an episode over budget stays
// unpublished until a later scan.
func (c *StorageScanner) prepareSeries(ctx context.Context, folder *models.MediaFolder, selection storagesource.LocationSelection, source storagesource.SourceConfig, lease storagesource.Lease, checkpoint storagesource.Checkpoint, page *storagev1.ListResponse, probes *seriesProbeGate) (preparedSeries, error) {
	var out preparedSeries
	if !c.nativeSeriesReady || c.movieReader == nil {
		return out, storagesource.ErrSourceUnavailable
	}
	page = proto.CloneOf(page)
	var candidates []int
	for i, entry := range page.GetEntries() {
		if entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE {
			continue
		}
		if sub, ok := scanner.StorageSeriesSidecarFromEntry(entry); ok {
			out.sidecars = append(out.sidecars, sub)
			continue
		}
		if _, ok := scanner.NativeMovieDemuxer(entry.GetName()); !ok {
			out.skipped++
			continue
		}
		if scanner.StorageSeriesFileSkipped(entry) {
			out.skipped++
			out.excluded = append(out.excluded, entry.GetId())
			continue
		}
		candidates = append(candidates, i)
	}
	entries := make([]*storagev1.Entry, len(candidates))
	for j, i := range candidates {
		entries[j] = page.GetEntries()[i]
	}
	episodic := scanner.StorageSeriesEpisodic(entries, scanner.StorageSeriesParseRoots(selection.Roots))
	candidates = slices.DeleteFunc(candidates, func(i int) bool {
		if episodic[page.GetEntries()[i].GetId()] {
			return false
		}
		out.skipped++
		out.excluded = append(out.excluded, page.GetEntries()[i].GetId())
		return true
	})
	episodes, err := c.prepareVideos(ctx, folder, selection.Location, source, lease, checkpoint, page, candidates, probes)
	out.episodes = episodes
	return out, err
}

// seriesDirectory carries one directory's videos and still unpaired sidecars
// across its listing pages. A listing finishes a directory before the next, so
// a sidecar cut off from its episode by a page boundary still finds it.
type seriesDirectory struct {
	id      string
	videos  []*storagev1.Entry
	pending []scanner.StorageSeriesSidecar
}

// pair adds a committed page's videos and sidecars and returns the sidecars
// that now have their episode, keyed by the episode's entry ID.
func (d *seriesDirectory) pair(directoryID string, videos []*storagev1.Entry, sidecars []scanner.StorageSeriesSidecar) (map[string][]scanner.StorageSeriesSidecar, seriesDirectory) {
	next := seriesDirectory{id: directoryID}
	if d != nil && d.id == directoryID {
		next.videos, next.pending = slices.Clone(d.videos), slices.Clone(d.pending)
	}
	next.videos = append(next.videos, videos...)
	next.pending = append(next.pending, sidecars...)
	paired := scanner.PairStorageSeriesSidecars(next.videos, next.pending)
	next.pending = slices.DeleteFunc(next.pending, func(s scanner.StorageSeriesSidecar) bool {
		for _, subs := range paired {
			if slices.ContainsFunc(subs, func(p scanner.StorageSeriesSidecar) bool { return p.Entry.GetId() == s.Entry.GetId() }) {
				return true
			}
		}
		return false
	})
	return paired, next
}
