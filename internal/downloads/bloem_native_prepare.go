package downloads

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// NativePreparation lets download preparation read native-storage and `.strm`
// videos, whose catalog path is a reference rather than a file. A prepared
// copy is made for the downloads waiting for it, so it reads the source under
// the authority of one of them, rechecked around every read as for playback.
type NativePreparation interface {
	// DownloadFile returns the download view of a native file, as
	// NativeOriginals does: the copy whose size is what the reader serves.
	DownloadFile(ctx context.Context, file *models.MediaFile) (*models.MediaFile, bool)
	// BeginPreparation authorizes one preparation attempt (attemptID) of the
	// artifact artifactID of file for requester, a download waiting for it.
	// The returned context carries the source as a context-scoped media input
	// (playback.WithContextMediaHost); the attempt runs on this server. end
	// withdraws the attempt's authority; call it once the attempt has
	// finished.
	BeginPreparation(ctx context.Context, artifactID, attemptID string, requester *Download, file *models.MediaFile) (prepared context.Context, end func(), err error)
}

// errNativePreparationRefused is the reason a failed native preparation
// records on its job and downloads. The cause is logged, never shown: it may
// name storage details.
var errNativePreparationRefused = errors.New("the source cannot be read for this download")

// nativeArtifactFiles is the artifact manager's file resolver. Once
// SetNativePreparation installs a preparation, a native file resolves to its
// download view, so a job reads the same file facts (a `.strm` item's served
// size, which its tone-map revision records) as the request that queued it.
type nativeArtifactFiles struct {
	FileResolver
	native atomic.Pointer[nativePreparationBox]
}

type nativePreparationBox struct{ NativePreparation }

// bloemNativeArtifacts prepares an artifact manager's preparer and file
// resolver for native inputs (see SetNativePreparation). Until a preparation
// is installed both behave exactly as given.
func bloemNativeArtifacts(preparer EncodePreparer, files FileResolver) (EncodePreparer, FileResolver) { //nolint:iface // the manager keeps Silo's FileResolver field
	if files == nil {
		return preparer, nil
	}
	return preparer, &nativeArtifactFiles{FileResolver: files}
}

func (r *nativeArtifactFiles) GetByID(ctx context.Context, id int) (*models.MediaFile, error) {
	file, err := r.FileResolver.GetByID(ctx, id)
	if err != nil || file == nil || !storagesource.IsNativeLocation(file.FilePath) {
		return file, err
	}
	if box := r.native.Load(); box != nil {
		if view, ok := box.DownloadFile(ctx, file); ok && view != nil && view.ID == file.ID && view.FilePath == file.FilePath {
			return view, nil
		}
	}
	return file, nil
}

// SetNativePreparation lets prepared downloads (bitrate presets, and copies of
// originals a device cannot play) read native-storage and `.strm` videos.
// Without it such a job fails. It is installed with SetNativeOriginals.
func (s *Service) SetNativePreparation(native NativePreparation) {
	if s == nil || native == nil || s.artifacts == nil {
		return
	}
	if files, ok := s.artifacts.fileRepo.(*nativeArtifactFiles); ok {
		files.native.Store(&nativePreparationBox{native})
	}
}

func (m *ArtifactManager) nativePreparation() NativePreparation {
	if files, ok := m.fileRepo.(*nativeArtifactFiles); ok {
		if box := files.native.Load(); box != nil {
			return box.NativePreparation
		}
	}
	return nil
}

// bloemPrepareFile is encodeOne's preparation call. A native input is
// prepared under the authority of a download waiting for it: the oldest one
// whose viewer may still read the source. Every other input goes to the
// preparer exactly as before.
func (m *ArtifactManager) bloemPrepareFile(ctx context.Context, attemptID string, file *models.MediaFile, a *Artifact, opts playback.TranscodeOpts) (PreparedArtifact, error) {
	if file == nil || a == nil || !storagesource.IsNativeLocation(opts.InputPath) {
		return m.preparer.PrepareFile(ctx, attemptID, opts, a.OutputPath)
	}
	native := m.nativePreparation()
	if native == nil || m.downloads == nil {
		slog.WarnContext(ctx, "native download preparation is not composed", "component", "downloads", "artifact_id", a.ID)
		return PreparedArtifact{}, errNativePreparationRefused
	}
	requesters, err := m.waitingDownloads(ctx, a.ID)
	if err != nil {
		return PreparedArtifact{}, err
	}
	var refusals error
	for _, requester := range requesters {
		prepared, end, err := native.BeginPreparation(ctx, a.ID, attemptID, requester, file)
		if err != nil {
			if ctx.Err() != nil {
				return PreparedArtifact{}, ctx.Err()
			}
			refusals = errors.Join(refusals, err)
			continue
		}
		result, err := m.prepareNativeHere(prepared, attemptID, opts, a.OutputPath)
		end()
		return result, err
	}
	slog.WarnContext(ctx, "no waiting download may read the native source", "component", "downloads", "artifact_id", a.ID, "downloads", len(requesters), "error", refusals)
	return PreparedArtifact{}, errNativePreparationRefused
}

// nativeRequesterWait bounds how long a job waits for its first download: a
// request queues the job just before it links its download row.
var nativeRequesterWait = 15 * time.Second

const nativeRequesterBackoff = 250 * time.Millisecond

// waitingDownloads lists the downloads waiting for artifactID, waiting
// briefly for the first one to be linked.
func (m *ArtifactManager) waitingDownloads(ctx context.Context, artifactID string) ([]*Download, error) {
	deadline := time.Now().Add(nativeRequesterWait)
	for {
		requesters, err := m.downloads.bloemPreparingDownloads(ctx, artifactID)
		if err != nil || len(requesters) > 0 || !time.Now().Before(deadline) {
			return requesters, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(nativeRequesterBackoff):
		}
	}
}

// bloemPreparingDownloads lists the downloads waiting for an artifact,
// oldest first.
func (r *Repository) bloemPreparingDownloads(ctx context.Context, artifactID string) ([]*Download, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+downloadColumns+` FROM downloads
		 WHERE artifact_id = $1 AND status = 'preparing'
		 ORDER BY created_at, id LIMIT 20`,
		artifactID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing downloads waiting for a prepared file: %w", err)
	}
	defer rows.Close()
	return scanDownloads(rows)
}

// prepareNativeHere prepares a native input on this server. A pooled
// preparer would send it to a transcode node, which reads its sources only
// through a core source grant naming a file path; a native input is not one.
// The pooled preparer's local path still applies its local fallback setting
// and storage gate.
func (m *ArtifactManager) prepareNativeHere(ctx context.Context, attemptID string, opts playback.TranscodeOpts, outputPath string) (PreparedArtifact, error) {
	if pooled, ok := m.preparer.(*NodeAwarePreparer); ok {
		return pooled.prepareLocally(ctx, attemptID, opts, outputPath)
	}
	return m.preparer.PrepareFile(ctx, attemptID, opts, outputPath)
}
