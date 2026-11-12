package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/fspath"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/remotesource"
)

// Bloem catalogs `.strm` files: a text file whose first non-empty line is the
// http or https URL of a video anywhere. The item is the movie or episode the
// file's name says, like any other video; its probe, size and container come
// from the remote media, read through remotesource's guard. The URL is kept in
// bloem_remote_sources, never in a catalog path. See
// docs/architecture/bloem-strm.md.

// bloemStrmFile reports whether a library walk takes a path as a `.strm`
// item. Silo's video extensions leave `.strm` out; Bloem adds it here.
func bloemStrmFile(filePath string) bool { return remotesource.IsStrmPath(filePath) }

// strmRuntime supplies the shared guard and store. Tests replace it.
var strmRuntime = func(s *Scanner) *remotesource.Runtime {
	if s == nil || s.fileRepo == nil || s.fileRepo.Pool() == nil {
		return nil
	}
	return remotesource.ForPool(s.fileRepo.Pool())
}

// strmProbeConfig uses the native movie limits: 128 MiB of reads, 60 s,
// 4 MiB of JSON and 64 KiB of stderr.
func strmProbeConfig() NativeProbeConfig {
	return NativeProbeConfig{
		Input:          nativestorage.ToolInputConfig{MaxBytes: 128 << 20, ChunkBytes: 128 << 10, MaxConcurrentReads: 2, Timeout: 60 * time.Second},
		MaxJSONBytes:   4 << 20,
		MaxStderrBytes: 64 << 10,
	}
}

// ErrStrmNotVideo is remote media ffprobe read but found no video in, or a
// container Bloem has no reviewed demuxer for.
var ErrStrmNotVideo = errors.New("remote source: not a video Bloem can read")

// RemoteProbe is the probe of one `.strm` URL. Reused is true when it is
// the stored probe of an unchanged remote, not a new one.
type RemoteProbe struct {
	URL        string
	Probe      *ProbeData
	Size       int64
	Validators remotesource.Validators
	Reused     bool
}

// Source is the remote-source row that records this probe.
func (p RemoteProbe) Source(filePath, sourceRevision string) (remotesource.Source, error) {
	probe, err := json.Marshal(p.Probe)
	if err != nil {
		return remotesource.Source{}, err
	}
	return remotesource.Source{FilePath: filePath, URL: p.URL, SourceRevision: sourceRevision, RemoteSize: p.Size, Validators: p.Validators, Probe: probe}, nil
}

// ReuseRemoteProbe returns the stored probe of a `.strm` URL when a rescan
// finds the same URL (and storage object revision) and the remote answers a
// one-byte request with the same size and ETag, or Last-Modified when it
// sent no ETag. ok is false whenever that cannot be shown; the caller then
// probes as usual. The check goes through the guard like any other request.
func ReuseRemoteProbe(ctx context.Context, guard *remotesource.Guard, stored remotesource.Source, found bool, rawURL, sourceRevision string) (RemoteProbe, bool) {
	if guard == nil || !found || len(stored.Probe) == 0 || stored.URL != rawURL || stored.Revision != remotesource.Revision(rawURL) || stored.SourceRevision != sourceRevision {
		return RemoteProbe{}, false
	}
	if err := guard.Check(ctx, rawURL); err != nil {
		return RemoteProbe{}, false
	}
	fresh, err := remotesource.Stat(ctx, guard.Client(), rawURL)
	if err != nil || !stored.Validators.Unchanged(fresh) {
		return RemoteProbe{}, false
	}
	var probe ProbeData
	if err := json.Unmarshal(stored.Probe, &probe); err != nil || len(probe.VideoTracks) == 0 || probe.CodecVideo == "" {
		return RemoteProbe{}, false
	}
	return RemoteProbe{URL: rawURL, Probe: &probe, Size: fresh.Size, Validators: fresh, Reused: true}, true
}

// ProbeRemote probes the media at rawURL with the scanner's trusted ffprobe.
// ffprobe never sees the URL: it reads a private loopback bridge whose reads
// are range requests through the guard. authority, when set, is rechecked with
// every bridge read (a storage scan's entry authority).
func (s *Scanner) ProbeRemote(ctx context.Context, guard *remotesource.Guard, rawURL string, authority func(context.Context) error) (RemoteProbe, error) {
	if s == nil || guard == nil || strings.TrimSpace(s.ffprobePath) == "" {
		return RemoteProbe{}, ErrNativeProbeInvalid
	}
	if err := guard.Check(ctx, rawURL); err != nil {
		return RemoteProbe{}, err
	}
	file, err := remotesource.Open(ctx, guard.Client(), rawURL, remotesource.NameHint(rawURL))
	if err != nil {
		return RemoteProbe{}, err
	}
	defer file.Close()
	demuxer, ok := sniffRemoteDemuxer(file)
	if !ok {
		return RemoteProbe{}, ErrStrmNotVideo
	}
	cfg := strmProbeConfig()
	cfg.Demuxer = demuxer
	cfg.Input.CancelSource = file.Cancel
	cfg.Input.Reauthorize = func(check context.Context) error {
		if err := file.Err(); err != nil {
			return err
		}
		if authority != nil {
			return authority(check)
		}
		return nil
	}
	probe, err := ProbeNative(file.Context(), s.ffprobePath, file, cfg)
	if err != nil {
		return RemoteProbe{}, err
	}
	if len(probe.VideoTracks) == 0 || probe.CodecVideo == "" {
		return RemoteProbe{}, ErrStrmNotVideo
	}
	return RemoteProbe{URL: rawURL, Probe: probe, Size: file.Info().Size, Validators: file.Validators()}, nil
}

// sniffRemoteDemuxer picks the reviewed demuxer from the first bytes, then
// from the URL's extension.
func sniffRemoteDemuxer(file *remotesource.File) (NativeProbeDemuxer, bool) {
	head := make([]byte, 1024)
	n, _ := file.ReadAt(head, 0)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte{0x1a, 0x45, 0xdf, 0xa3}):
		return NativeProbeMatroska, true
	case len(head) >= 12 && bytes.Equal(head[0:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("AVI ")):
		return NativeProbeAVI, true
	case len(head) >= 8 && isMOVAtom(head[4:8]):
		return NativeProbeMOV, true
	case tsSync(head, 0, 188) || tsSync(head, 4, 192):
		return NativeProbeMPEGTS, true
	}
	switch strings.ToLower(path.Ext(file.Info().Name)) {
	case ".mp4", ".m4v", ".mov":
		return NativeProbeMOV, true
	case ".mkv", ".webm":
		return NativeProbeMatroska, true
	case ".avi", ".divx":
		return NativeProbeAVI, true
	case ".ts", ".m2ts", ".mts":
		return NativeProbeMPEGTS, true
	}
	return 0, false
}

func isMOVAtom(atom []byte) bool {
	switch string(atom) {
	case "ftyp", "moov", "mdat", "free", "wide", "skip", "pnot":
		return true
	}
	return false
}

func tsSync(head []byte, first, stride int) bool {
	if len(head) <= first+2*stride {
		return false
	}
	return head[first] == 0x47 && head[first+stride] == 0x47 && head[first+2*stride] == 0x47
}

// readLocalStrm reads at most remotesource.MaxStrmBytes of a local file.
func readLocalStrm(filePath string) ([]byte, error) {
	f, err := fspath.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, remotesource.MaxStrmBytes+1))
}

// probeBloemStrm stands in for Silo's local ffprobe on a `.strm` file. A good
// URL records its remote source; any failure forgets it, so a `.strm` changed
// to a refused or dead address stops playing. A file whose content is not a
// URL, or whose media is not video, counts as a probe rejection; a refused
// address or a network failure stays retryable, so allowing private
// addresses or a server coming back takes effect at the next scan.
func (s *Scanner) probeBloemStrm(ctx context.Context, filePath string) (*ProbeData, string, bool) {
	rt := strmRuntime(s)
	if rt == nil || strings.TrimSpace(s.ffprobePath) == "" {
		return nil, probeSourceLocal, false
	}
	result, err := s.readAndProbeStrm(ctx, rt, filePath)
	if err != nil {
		if delErr := rt.Store.Delete(ctx, filePath); delErr != nil {
			slog.WarnContext(ctx, "scanner: could not forget a .strm source", "component", "scanner", "path", filePath, "error", delErr)
		}
		// The URL is never logged: it may carry a token.
		slog.WarnContext(ctx, "scanner: .strm source refused", "component", "scanner", "path", filePath, "error", err)
		rejected := errors.Is(err, remotesource.ErrInvalidStrm) || errors.Is(err, ErrStrmNotVideo) || errors.Is(err, ErrNativeProbeInvalid)
		return nil, probeSourceLocal, rejected
	}
	if result.Reused {
		return result.Probe, probeSourceLocal, false
	}
	src, err := result.Source(filePath, "")
	if err == nil {
		err = rt.Store.Put(ctx, src)
	}
	if err != nil {
		slog.WarnContext(ctx, "scanner: could not record a .strm source", "component", "scanner", "path", filePath, "error", err)
		return nil, probeSourceLocal, false
	}
	return result.Probe, probeSourceLocal, false
}

func (s *Scanner) readAndProbeStrm(ctx context.Context, rt *remotesource.Runtime, filePath string) (RemoteProbe, error) {
	content, err := readLocalStrm(filePath)
	if err != nil {
		return RemoteProbe{}, err
	}
	rawURL, err := remotesource.ParseStrm(content)
	if err != nil {
		return RemoteProbe{}, err
	}
	stored, found, err := rt.Store.Get(ctx, filePath)
	if err != nil {
		return RemoteProbe{}, err
	}
	if reused, ok := ReuseRemoteProbe(ctx, rt.Guard, stored, found, rawURL, ""); ok {
		return reused, nil
	}
	return s.ProbeRemote(ctx, rt.Guard, rawURL, nil)
}

// ProbeStorageStrm reads a `.strm` object already opened by a storage scan
// and probes the URL it holds. The entry stays open, and its authority is
// rechecked with every probe read.
func (s *Scanner) ProbeStorageStrm(ctx context.Context, guard *remotesource.Guard, file nativeStrmEntry) (RemoteProbe, error) {
	size := file.Info().Size
	if size < 0 || size > remotesource.MaxStrmBytes {
		return RemoteProbe{}, remotesource.ErrInvalidStrm
	}
	content := make([]byte, size)
	if _, err := file.ReadAt(content, 0); err != nil && !errors.Is(err, io.EOF) {
		return RemoteProbe{}, err
	}
	rawURL, err := remotesource.ParseStrm(content)
	if err != nil {
		return RemoteProbe{}, err
	}
	return s.ProbeRemote(ctx, guard, rawURL, file.Reauthorize)
}

// nativeStrmEntry is the part of nativestorage.ScanEntryFile a `.strm` probe
// uses.
type nativeStrmEntry interface {
	io.ReaderAt
	Info() mediasource.Info
	Reauthorize(context.Context) error
}
