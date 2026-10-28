package main

import (
	"sync/atomic"
	"time"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/libraryingest"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// newNativeMovieStorageScanner is called only at startup, before installing the
// ingestor or starting queues. Playback acceptance authorizes this composition;
// configuration alone never proves a particular backend or movie is readable.
func newNativeMovieStorageScanner(deps *api.Dependencies) (*libraryingest.StorageScanner, error) {
	if deps == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	deps.NativeMovieScannerReady = false
	deps.NativeAudioScannerReady = false
	deps.NativeAudioGate = new(atomic.Bool)
	if deps.DB == nil || deps.NativeStorage == nil || deps.FileRepo == nil || !deps.Scanner.NativeMovieScanConfigured() {
		return nil, storagesource.ErrSourceUnavailable
	}
	storage, err := libraryingest.NewStorageScanner(deps.NativeStorage, storagesource.NewRepository(deps.DB), resourcetenancy.NewStore(deps.DB), deps.Scanner)
	if err != nil {
		return nil, err
	}
	// A failed storage scan resumes its journal instead of waiting for the
	// next scheduled or manual scan.
	storage.SetRetry(5, 30*time.Second)
	reader, err := nativestorage.NewScanReader(deps.DB, deps.NativeStorage)
	if err != nil {
		return nil, err
	}
	// The reads of a page's files share one authority check per second
	// instead of a locked check before and after every read; a probe makes
	// hundreds of reads. Each page is still published under the locks.
	reader.SetAuthorityReuse(time.Second)
	// The scanner keeps its trusted startup ffprobe path. Bounds cover all
	// provider bytes (including Matroska header reads) and tool diagnostics.
	cfg := scanner.NativeProbeConfig{Input: nativestorage.ToolInputConfig{MaxBytes: 128 << 20, ChunkBytes: 128 << 10, MaxConcurrentReads: 2, Timeout: 60 * time.Second}, MaxJSONBytes: 4 << 20, MaxStderrBytes: 64 << 10}
	if err := storage.ConfigureNativeMovies(true, reader, cfg); err != nil {
		return nil, err
	}
	if deps.Scanner.NativeAudioScanConfigured() {
		// Every audio probe read is a provider range request, so larger
		// reads cut the requests per probe.
		audio := cfg
		audio.Input.ChunkBytes = 1 << 20
		if err := storage.ConfigureNativeAudio(true, reader, audio, deps.NativeAudioGate); err != nil {
			return nil, err
		}
		deps.NativeAudioScannerReady = true
	}
	// Native movies match through the same metadata Process the admin
	// match/apply endpoint uses, never through the path-based match queues.
	if deps.MetadataService != nil {
		storage.ConfigureNativeMovieMatching(deps.MetadataService)
	}
	deps.NativeMovieScannerReady = true
	return storage, nil
}
