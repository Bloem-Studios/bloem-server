package scanner

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// NewBloemAttachmentBitrateBackfiller is Silo's attachment bitrate backfill
// for Bloem's catalog, where not every file path is a file ffprobe can open.
func NewBloemAttachmentBitrateBackfiller(repo *FileRepository, ffprobePath string) *AttachmentBitrateBackfiller {
	b := NewAttachmentBitrateBackfiller(repo, ffprobePath)
	skipNativeAttachmentBitrateProbes(b)
	return b
}

// skipNativeAttachmentBitrateProbes leaves native storage files and `.strm`
// files to their own scans. A native file's path is a catalog key and a
// `.strm` file holds a URL, so the media tool guard refuses to launch ffprobe
// on them, the pass would count them unreadable on every run and list them
// to read again forever, and it would never finish. Their own probes already
// store the attachment-free rate (convertProbeData), so only a native file
// scanned before that keeps its rate, until its entry changes.
//
// Answering with a size no file has makes the pass count the file as changed
// since its scan: not read, not written and not listed again.
func skipNativeAttachmentBitrateProbes(b *AttachmentBitrateBackfiller) {
	if b == nil || b.probe == nil {
		return
	}
	probe := b.probe
	b.probe = func(ctx context.Context, ffprobePath, filePath string) (containerBitrate, error) {
		if storagesource.IsNativeLocation(filePath) {
			return containerBitrate{size: -1}, nil
		}
		return probe(ctx, ffprobePath, filePath)
	}
}
