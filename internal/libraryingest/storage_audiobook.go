package libraryingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"

	"google.golang.org/protobuf/proto"

	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/scanner"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// ConfigureNativeAudio is startup-only. The optional gate must be supplied for
// a ready lane; delivery composition releases it after installing all adapters.
func (c *StorageScanner) ConfigureNativeAudio(ready bool, reader *nativestorage.ScanReader, cfg scanner.NativeProbeConfig, gates ...*atomic.Bool) error {
	if c == nil || c.resources == nil {
		return storagesource.ErrSourceUnavailable
	}
	if len(gates) > 1 {
		return scanner.ErrNativeProbeInvalid
	}
	var gate *atomic.Bool
	if len(gates) == 1 {
		gate = gates[0]
	}
	if ready && (gate == nil || c.scanner == nil || !c.scanner.NativeAudioScanConfigured() || reader == nil || cfg.Input.MaxBytes <= 0 || cfg.Input.ChunkBytes <= 0 || cfg.Input.ChunkBytes > 8<<20 || cfg.Input.MaxConcurrentReads <= 0 || cfg.Input.MaxConcurrentReads > 8 || cfg.Input.Timeout <= 0 || cfg.MaxJSONBytes <= 0 || cfg.MaxStderrBytes <= 0) {
		return scanner.ErrNativeProbeInvalid
	}
	cfg.Input.CancelSource, cfg.Input.Reauthorize, cfg.Input.ReleaseHost = nil, nil, nil
	c.nativeAudioReady, c.audioReader, c.audioProbeConfig, c.audioGate = ready, reader, cfg, gate
	c.resources.SetNativeAudioReady(ready)
	if reader != nil {
		reader.SetNativeAudioReady(ready)
	}
	return nil
}

func declaresNativeAudio(description *storagev1.DescribeResponse, sourceID string) bool {
	for _, source := range description.GetSources() {
		if source.GetId() == sourceID {
			return source.GetDirectoryOnlyListing() && slices.Contains(source.GetMediaKinds(), "audiobooks")
		}
	}
	return false
}

func (c *StorageScanner) prepareAudio(ctx context.Context, outage *audioOutage, location storagesource.Location, source storagesource.SourceConfig, lease storagesource.Lease, checkpoint storagesource.Checkpoint, page *storagev1.ListResponse) ([]scanner.StorageAudioTrack, int, error) {
	if !c.nativeAudioReady || c.audioReader == nil {
		return nil, 0, storagesource.ErrSourceUnavailable
	}
	page = proto.CloneOf(page)
	sealed, err := c.audioReader.AdmitScanPage(ctx, location, source, lease, checkpoint, page)
	if err != nil {
		return nil, 0, err
	}
	// A track whose revision an earlier scan already probed is not opened again.
	revisions := map[string]string{}
	for _, entry := range page.GetEntries() {
		if entry.GetKind() == storagev1.EntryKind_ENTRY_KIND_FILE {
			revisions[entry.GetId()] = entry.GetRevision()
		}
	}
	cached, err := c.sources.CachedAudioProbes(ctx, location.ID, revisions)
	if err != nil {
		return nil, 0, err
	}
	var tracks []scanner.StorageAudioTrack
	skipped := 0
	read, unavailable := false, false
	for i, entry := range page.GetEntries() {
		if entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE {
			continue
		}
		if _, ok := scanner.NativeAudioDemuxer(entry.GetName()); !ok {
			if scanner.SupportsAudioFile(entry.GetName()) {
				tracks = append(tracks, scanner.StorageAudioTrack{Entry: entry, Status: scanner.StorageAudioTrackUnsupported})
			} else {
				skipped++
			}
			continue
		}
		if hit, ok := cached[entry.GetId()]; ok {
			if track, err := scanner.CachedStorageAudioTrack(entry, hit.Status, hit.Probe); err == nil {
				tracks = append(tracks, track)
				continue
			}
		}
		track, err := c.probeAudio(ctx, sealed, i, entry)
		if err != nil {
			return nil, 0, err
		}
		read = read || track.Status != scanner.StorageAudioTrackUnavailable
		unavailable = unavailable || track.Status == scanner.StorageAudioTrackUnavailable
		tracks = append(tracks, track)
	}
	if err := outage.observe(read, unavailable); err != nil {
		return nil, 0, err
	}
	return tracks, skipped, nil
}

// probeAudio opens and probes one track. A failure that is the track's own
// (see scanner.StorageAudioProbeFailure) is recorded as its status, so one
// missing or unreadable file rejects only its book.
func (c *StorageScanner) probeAudio(ctx context.Context, sealed *nativestorage.ScanPage, index int, entry *storagev1.Entry) (scanner.StorageAudioTrack, error) {
	failed := func(err error) (scanner.StorageAudioTrack, bool) {
		status, ok := scanner.StorageAudioProbeFailure(err)
		if !ok || ctx.Err() != nil {
			return scanner.StorageAudioTrack{}, false
		}
		slog.WarnContext(ctx, "native audiobook track not probed", "entry_id", entry.GetId(), "status", status, "error", err)
		return scanner.StorageAudioTrack{Entry: proto.CloneOf(entry), Status: status}, true
	}
	file, err := c.audioReader.OpenScanEntry(ctx, sealed, index)
	if err != nil {
		if track, ok := failed(err); ok {
			return track, nil
		}
		return scanner.StorageAudioTrack{}, fmt.Errorf("open storage audiobook %q: %w", entry.GetName(), err)
	}
	cfg := c.audioProbeConfig
	cfg.Input.CancelSource, cfg.Input.Reauthorize, cfg.Input.ReleaseHost = file.Cancel, file.Reauthorize, nil
	track, probeErr := c.scanner.ProbeStorageAudio(file.Context(), entry, file, cfg)
	if err := errors.Join(probeErr, file.Close()); err != nil {
		if track, ok := failed(err); ok {
			return track, nil
		}
		return scanner.StorageAudioTrack{}, fmt.Errorf("probe storage audiobook %q: %w", entry.GetName(), err)
	}
	return track, nil
}

// audioOutageLimit is how many books in a row may each lose a track to an
// unavailable read before the scan stops. A provider whose storage is gone
// fails every book; stopping leaves the rest pending for a later scan instead
// of rejecting each of them.
const audioOutageLimit = 32

var errAudioOutage = errors.New("storage audiobook tracks unavailable for many books in a row")

// audioOutage counts, across a scan's workers, the books in a row whose
// tracks could not be read. A book with a track read since resets it.
type audioOutage struct{ run atomic.Int32 }

func (o *audioOutage) observe(read, unavailable bool) error {
	switch {
	case unavailable:
		if o.run.Add(1) >= audioOutageLimit {
			return errAudioOutage
		}
	case read:
		o.run.Store(0)
	}
	return nil
}
