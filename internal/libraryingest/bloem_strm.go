package libraryingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/remotesource"
	"github.com/Silo-Server/silo-server/internal/scanner"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// ConfigureRemoteSources is startup-only, like ConfigureNativeMovies. With a
// runtime, a movie or TV library's storage source also catalogs `.strm`
// objects: the movie or episode each name says, probed from the URL the
// object holds. Without one, `.strm` objects stay skipped.
func (c *StorageScanner) ConfigureRemoteSources(rt *remotesource.Runtime) {
	if c != nil {
		c.remote = rt
	}
}

func (c *StorageScanner) takesStrm(entry *storagev1.Entry) bool {
	return c.remote != nil && c.remote.Guard != nil && c.remote.Store != nil && remotesource.IsStrmPath(entry.GetName())
}

// prepareStrms publishes a page's `.strm` objects: unchanged ones with their
// stored probe and without being opened, the rest probed from their URL. An
// unusable URL skips only its entry, returned as excluded so a file an older
// scan published goes missing. probes, the TV lane's gate, bounds the probes
// as for any episode: one over budget stays unpublished until a later scan.
func (c *StorageScanner) prepareStrms(ctx context.Context, location storagesource.Location, source storagesource.SourceConfig, lease storagesource.Lease, checkpoint storagesource.Checkpoint, page *storagev1.ListResponse, strms []int, probes *seriesProbeGate) ([]scanner.StorageMovie, []string, error) {
	videos, strms, err := c.reuseStrmVideos(ctx, location, page, strms)
	if err != nil {
		return nil, nil, err
	}
	strms = slices.DeleteFunc(strms, func(int) bool { return !probes.admit() })
	if len(strms) == 0 {
		return videos, nil, nil
	}
	sealed, err := c.movieReader.AdmitScanPage(ctx, location, source, lease, checkpoint, page)
	if err != nil {
		return nil, nil, err
	}
	var excluded []string
	for _, i := range strms {
		entry := page.GetEntries()[i]
		if err := probes.wait(ctx); err != nil {
			return nil, nil, err
		}
		video, ok, err := c.prepareStrmVideo(ctx, location, sealed, i, entry)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			excluded = append(excluded, entry.GetId())
			continue
		}
		videos = append(videos, video)
	}
	return videos, excluded, nil
}

// prepareStrmVideo reads one pinned `.strm` object and probes its URL while
// the object stays open, so the scan's authority is rechecked with every probe
// read. ok is false when the URL is unusable (not a URL, refused, missing,
// not video): the entry is skipped and its remote source forgotten, and the
// rest of the page still publishes. A failure of the scan's own authority
// fails the page, as for any stored movie.
func (c *StorageScanner) prepareStrmVideo(ctx context.Context, location storagesource.Location, sealed *nativestorage.ScanPage, index int, entry *storagev1.Entry) (scanner.StorageMovie, bool, error) {
	key, err := storagesource.CatalogLocation(location.ID, entry.GetId())
	if err != nil {
		return scanner.StorageMovie{}, false, err
	}
	file, err := c.movieReader.OpenScanEntry(ctx, sealed, index)
	if err != nil {
		return scanner.StorageMovie{}, false, fmt.Errorf("open storage .strm %q: %w", entry.GetName(), err)
	}
	defer file.Close()
	result, probeErr := c.scanner.ProbeStorageStrm(file.Context(), c.remote.Guard, file)
	if probeErr != nil {
		if err := errors.Join(file.Err(), file.Reauthorize(ctx)); err != nil {
			return scanner.StorageMovie{}, false, storageMovieProbeError(entry.GetName(), err)
		}
		// The URL is never logged: it may carry a token.
		slog.WarnContext(ctx, "storage scan: .strm source refused", "component", "libraryingest", "entry", entry.GetName(), "error", probeErr)
		if err := c.remote.Store.Delete(ctx, key); err != nil {
			return scanner.StorageMovie{}, false, err
		}
		return scanner.StorageMovie{}, false, nil
	}
	src, err := result.Source(key, entry.GetRevision())
	if err != nil {
		return scanner.StorageMovie{}, false, err
	}
	if err := c.remote.Store.Put(ctx, src); err != nil {
		return scanner.StorageMovie{}, false, err
	}
	return scanner.StorageMovie{Entry: proto.CloneOf(entry), Probe: result.Probe}, true, nil
}

// strmReuseWorkers bounds the one-byte validator requests a page sends at
// once. Most `.strm` files of a library point at one host.
const strmReuseWorkers = 8

// reuseStrmVideos finds the page's `.strm` objects that need no new probe:
// the object revision is the one the stored URL was read from, and the remote
// answers with the same size and ETag (or Last-Modified). Those publish with
// the stored probe and are not opened; the rest are returned for probing. The
// stored rows are read in one query and the validator requests run
// concurrently, so a rescan of thousands of unchanged entries stays quick.
func (c *StorageScanner) reuseStrmVideos(ctx context.Context, location storagesource.Location, page *storagev1.ListResponse, strms []int) ([]scanner.StorageMovie, []int, error) {
	if len(strms) == 0 {
		return nil, strms, nil
	}
	keys := make([]string, len(strms))
	for j, i := range strms {
		key, err := storagesource.CatalogLocation(location.ID, page.GetEntries()[i].GetId())
		if err != nil {
			return nil, nil, err
		}
		keys[j] = key
	}
	stored, err := c.remote.Store.GetMany(ctx, keys)
	if err != nil {
		return nil, nil, err
	}
	results := make([]scanner.RemoteProbe, len(strms))
	ok := make([]bool, len(strms))
	work := make(chan int)
	var wg sync.WaitGroup
	for range min(strmReuseWorkers, len(strms)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range work {
				entry := page.GetEntries()[strms[j]]
				src, found := stored[keys[j]]
				results[j], ok[j] = scanner.ReuseRemoteProbe(ctx, c.remote.Guard, src, found, src.URL, entry.GetRevision())
			}
		}()
	}
	for j := range strms {
		work <- j
	}
	close(work)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	var movies []scanner.StorageMovie
	var probe []int
	for j, i := range strms {
		if !ok[j] {
			probe = append(probe, i)
			continue
		}
		movies = append(movies, scanner.StorageMovie{Entry: proto.CloneOf(page.GetEntries()[i]), Probe: results[j].Probe})
	}
	return movies, probe, nil
}
