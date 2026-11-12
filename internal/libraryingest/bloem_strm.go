package libraryingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"google.golang.org/protobuf/proto"

	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/remotesource"
	"github.com/Silo-Server/silo-server/internal/scanner"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// ConfigureRemoteSources is startup-only, like ConfigureNativeMovies. With a
// runtime, a movie library's storage source also catalogs `.strm` objects:
// the movie each name says, probed from the URL the object holds. Without
// one, `.strm` objects stay skipped.
func (c *StorageScanner) ConfigureRemoteSources(rt *remotesource.Runtime) {
	if c != nil {
		c.remote = rt
	}
}

func (c *StorageScanner) takesStrm(entry *storagev1.Entry) bool {
	return c.remote != nil && c.remote.Guard != nil && c.remote.Store != nil && remotesource.IsStrmPath(entry.GetName())
}

// prepareStrmMovie reads one pinned `.strm` object and probes its URL while
// the object stays open, so the scan's authority is rechecked with every probe
// read. ok is false when the URL is unusable (not a URL, refused, missing,
// not video): the entry is skipped and its remote source forgotten, and the
// rest of the page still publishes. A failure of the scan's own authority
// fails the page, as for any stored movie.
func (c *StorageScanner) prepareStrmMovie(ctx context.Context, location storagesource.Location, sealed *nativestorage.ScanPage, index int, entry *storagev1.Entry) (scanner.StorageMovie, bool, error) {
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
	if err := c.remote.Store.Put(ctx, remotesource.Source{FilePath: key, URL: result.URL, SourceRevision: entry.GetRevision(), RemoteSize: result.Size}); err != nil {
		return scanner.StorageMovie{}, false, err
	}
	return scanner.StorageMovie{Entry: proto.CloneOf(entry), Probe: result.Probe}, true, nil
}
