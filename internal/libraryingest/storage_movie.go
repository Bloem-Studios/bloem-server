package libraryingest

import (
	"context"
	"errors"

	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/scanner"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"google.golang.org/protobuf/proto"
)

// ConfigureNativeMovies is startup-only, before this coordinator or its reader
// and resource store admit work. False is the default. The host must complete
// playback acceptance before setting ready in production; configuring budgets
// alone does not release the hold. No setting or API request may call this.
// Every numerical limit is explicit. File-specific lifetime callbacks are
// supplied from OpenScanEntry, never retained from this configuration.
func (c *StorageScanner) ConfigureNativeMovies(ready bool, reader *nativestorage.ScanReader, cfg scanner.NativeProbeConfig) error {
	if c == nil || c.resources == nil {
		return storagesource.ErrSourceUnavailable
	}
	if ready && (reader == nil || cfg.Input.MaxBytes <= 0 || cfg.Input.ChunkBytes <= 0 || cfg.Input.ChunkBytes > 8<<20 ||
		cfg.Input.MaxConcurrentReads <= 0 || cfg.Input.MaxConcurrentReads > 8 || cfg.Input.Timeout <= 0 || cfg.MaxJSONBytes <= 0 || cfg.MaxStderrBytes <= 0) {
		return scanner.ErrNativeProbeInvalid
	}
	cfg.Input.CancelSource, cfg.Input.Reauthorize, cfg.Input.ReleaseHost = nil, nil, nil
	c.nativeMovieReady, c.movieReader, c.movieProbeConfig = ready, reader, cfg
	c.resources.SetNativeMovieReady(ready)
	if reader != nil {
		reader.SetNativeMovieReady(ready)
	}
	return nil
}

// prepareMovies probes the page's movie files. Files Silo's walk would not
// publish (extras, samples, ignored folders) are neither opened nor probed;
// their IDs are returned so a file published by an older scan goes missing.
func (c *StorageScanner) prepareMovies(ctx context.Context, location storagesource.Location, source storagesource.SourceConfig, lease storagesource.Lease, checkpoint storagesource.Checkpoint, page *storagev1.ListResponse) ([]scanner.StorageMovie, int, []string, error) {
	if !c.nativeMovieReady || c.movieReader == nil {
		return nil, 0, nil, storagesource.ErrSourceUnavailable
	}
	// Retain only our own validated listing data. The reader seals another copy
	// and proves pinned Stat metadata against exactly that pending checkpoint.
	page = proto.CloneOf(page)
	skipped := 0
	var candidates []int
	var excluded []string
	for i, entry := range page.GetEntries() {
		if entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE {
			continue
		}
		if _, ok := scanner.NativeMovieDemuxer(entry.GetName()); !ok {
			skipped++
			continue
		}
		if scanner.StorageMovieFileSkipped(entry) {
			skipped++
			excluded = append(excluded, entry.GetId())
			continue
		}
		candidates = append(candidates, i)
	}
	if len(candidates) == 0 {
		return nil, skipped, excluded, nil
	}
	sealed, err := c.movieReader.AdmitScanPage(ctx, location, source, lease, checkpoint, page)
	if err != nil {
		return nil, 0, nil, err
	}
	movies := make([]scanner.StorageMovie, 0, len(candidates))
	for _, i := range candidates {
		entry := page.GetEntries()[i]
		file, err := c.movieReader.OpenScanEntry(ctx, sealed, i)
		if err != nil {
			return nil, 0, nil, err
		}
		cfg := c.movieProbeConfig
		cfg.Input.CancelSource, cfg.Input.Reauthorize, cfg.Input.ReleaseHost = file.Cancel, file.Reauthorize, nil
		movie, probeErr := c.scanner.ProbeStorageMovie(file.Context(), entry, file, cfg)
		// ScanEntryFile.Close is idempotent: successful ToolInput owns cleanup;
		// failed admission/configuration still leaves this handle with us.
		if err := errors.Join(probeErr, file.Close()); err != nil {
			return nil, 0, nil, err
		}
		movies = append(movies, movie)
	}
	return movies, skipped, excluded, nil
}
