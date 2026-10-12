package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/Silo-Server/silo-server/internal/mediaprobe"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/processmetrics"
)

var (
	ErrNativeProbeOutputBudget = errors.New("native probe output budget exhausted")
	ErrNativeProbeInvalid      = errors.New("invalid native probe input or output")
)

// NativeProbeDemuxer selects a fixed reviewed standalone container reader.
// It is not a format or library-kind admission policy; callers make that
// decision separately. Arbitrary manifests/demuxer names are not accepted.
type NativeProbeDemuxer uint8

const (
	NativeProbeMOV NativeProbeDemuxer = iota + 1
	NativeProbeMatroska
	NativeProbeMP3
	NativeProbeFLAC
	NativeProbeOgg
	NativeProbeWAV
)

// NativeProbeConfig comes from the trusted scan consumer. Every bound is
// explicit; Input.MaxBytes covers HTTP and direct Matroska reads together.
type NativeProbeConfig struct {
	Demuxer        NativeProbeDemuxer
	Input          nativestorage.ToolInputConfig
	MaxJSONBytes   int64
	MaxStderrBytes int64
}

// ProbeNative consumes an already scan-authorized pinned file. ffprobePath must
// come from the server's trusted binary configuration, never provider metadata
// or an API request. A ScanEntryFile supplies ctx, Input.CancelSource and
// Input.Reauthorize from its Context, Cancel and Reauthorize methods.
//
// Ownership transfers only after NewToolInput succeeds. An admission/config
// failure leaves the file with the caller. After transfer, every outcome joins
// the child and HTTP reads, closes the file and releases its host admission.
// This prerequisite reports failures without classifying ingestion rejections.
func ProbeNative(ctx context.Context, ffprobePath string, file mediasource.File, cfg NativeProbeConfig) (*ProbeData, error) {
	probe, _, err := probeNative(ctx, ffprobePath, file, cfg)
	return probe, err
}

// probeNative retains bounded raw metadata for owned native consumers that
// require exact timing. ProbeNative keeps Silo's integer-second projection.
func probeNative(ctx context.Context, ffprobePath string, file mediasource.File, cfg NativeProbeConfig) (*ProbeData, *ffprobeOutput, error) {
	if ctx == nil || file == nil || strings.TrimSpace(ffprobePath) == "" || cfg.MaxJSONBytes <= 0 || cfg.MaxStderrBytes <= 0 {
		return nil, nil, ErrNativeProbeInvalid
	}
	var demuxer, formats string
	switch cfg.Demuxer {
	case NativeProbeMOV:
		demuxer, formats = "mov", "mov"
	case NativeProbeMatroska:
		demuxer, formats = "matroska", "matroska,webm"
	case NativeProbeMP3:
		demuxer, formats = "mp3", "mp3"
	case NativeProbeFLAC:
		demuxer, formats = "flac", "flac"
	case NativeProbeOgg:
		demuxer, formats = "ogg", "ogg"
	case NativeProbeWAV:
		demuxer, formats = "wav", "wav"
	default:
		return nil, nil, ErrNativeProbeInvalid
	}
	lifetime, cancel := context.WithTimeout(ctx, cfg.Input.Timeout)
	defer cancel()
	reader := &nativeProbeFile{File: file, ctx: lifetime, reauthorize: cfg.Input.Reauthorize, maxBytes: cfg.Input.MaxBytes, info: file.Info()}
	inputConfig := cfg.Input
	if cancelSource := inputConfig.CancelSource; cancelSource != nil {
		// ToolInput cancels the source only from Close: after the child joined,
		// or with its lifetime already ended and recorded. A read failing after
		// this point is the teardown of a range the child abandoned.
		inputConfig.CancelSource = func() {
			reader.tearDown()
			cancelSource()
		}
	}
	input, err := nativestorage.NewToolInput(lifetime, reader, inputConfig)
	if err != nil {
		return nil, nil, &nativeProbeFailure{err}
	}
	var output []byte
	var grant string
	var tracks []mediaprobe.MatroskaTrack
	var tracksErr error
	err = input.Run(func(runCtx context.Context, argument string) error {
		// ToolInput has no interim HTTP-handler join that retains the file.
		// Cache only bounded track metadata before the child starts; apply it
		// after Run's final child/handler join, never alongside HTTP reads.
		tracks, tracksErr = mediaprobe.ReadMatroskaTracks(reader, reader.info.Size)
		if err := reader.failure(); err != nil {
			return err
		}
		childCtx, stopChild := context.WithCancel(runCtx)
		defer stopChild()
		stdout := &nativeProbeOutput{limit: cfg.MaxJSONBytes, cancel: stopChild, retain: true}
		stderr := &nativeProbeOutput{limit: cfg.MaxStderrBytes, cancel: stopChild}
		args := []string{
			"-v", "error", "-protocol_whitelist", "http,tcp", "-http_proxy", "",
			"-f", demuxer, "-format_whitelist", formats,
			"-print_format", "json", "-show_format", "-show_streams", "-show_chapters",
		}
		if cfg.Demuxer == NativeProbeMOV {
			args = append(args, "-enable_drefs", "0", "-use_absolute_path", "0")
		}
		args = append(args, argument)
		cmd := exec.CommandContext(childCtx, ffprobePath, args...)
		cmd.Env = nativeProbeEnvironment(os.Environ())
		cmd.Stdout, cmd.Stderr = stdout, stderr
		cmd.WaitDelay = cfg.Input.Timeout
		commandErr := runNativeProbeProcess(childCtx, cmd) // Wait joins the bounded output copiers.
		processmetrics.Record(processmetrics.Probe, cmd.ProcessState, commandErr, childCtx.Err())
		// A successful exit does not authorize publication. Check again while
		// the opened file is still live, before Run joins/cleans the bridge.
		authorityErr := cfg.Input.Reauthorize(runCtx)
		if err := errors.Join(commandErr, runCtx.Err(), stdout.err, stderr.err, reader.failure(), authorityErr); err != nil {
			return err
		}
		output, grant = stdout.data.Bytes(), argument
		return nil
	})
	if err = errors.Join(err, reader.failure()); err != nil {
		return nil, nil, &nativeProbeFailure{err}
	}
	var raw ffprobeOutput
	// Unmarshal rejects a trailing document and any non-whitespace tail.
	if err := json.Unmarshal(output, &raw); err != nil {
		return nil, nil, &nativeProbeFailure{err}
	}
	if raw.Format.FormatName == "" || len(raw.Streams) == 0 {
		return nil, nil, ErrNativeProbeInvalid
	}
	// Filename is only an extension hint for containerForFile. Never pass
	// this name to an OS path wrapper or keep the ephemeral HTTP argument.
	raw.Format.Filename = reader.info.Name
	raw.Format.Size = strconv.FormatInt(reader.info.Size, 10)
	probe := convertProbeData(&raw)
	if probe.Container == containerMKV && subtitleTracksMissingContainerID(probe.SubtitleTracks) {
		if tracksErr != nil {
			return nil, nil, &nativeProbeFailure{tracksErr}
		}
		subtitles := make([]matroskaSubtitleStream, len(probe.SubtitleTracks))
		for i, track := range probe.SubtitleTracks {
			subtitles[i] = matroskaSubtitleStream{Index: track.Index, Codec: track.Codec}
		}
		ids, err := matroskaSubtitleTrackIDs(tracks, len(probe.VideoTracks), len(probe.AudioTracks), subtitles)
		if err != nil {
			return nil, nil, &nativeProbeFailure{err}
		}
		for i := range probe.SubtitleTracks {
			if probe.SubtitleTracks[i].ContainerTrackID == "" {
				probe.SubtitleTracks[i].ContainerTrackID = ids[i]
			}
		}
	}
	// The trusted binary must not echo its private argument into persistable
	// metadata. Ordinary media tags containing unrelated URLs stay intact.
	encoded, err := json.Marshal(struct {
		Probe *ProbeData
		Raw   *ffprobeOutput
	}{probe, &raw})
	if err != nil || bytes.Contains(encoded, []byte(grant)) {
		return nil, nil, ErrNativeProbeInvalid
	}
	return probe, &raw, nil
}

// Keep the installed binary's ordinary runtime/library environment. Only
// remove routing/report controls that could bypass the pinned bridge or write
// its ephemeral argument to disk. The parent's environment is untouched.
func nativeProbeEnvironment(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "FFREPORT":
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

type nativeProbeFailure struct{ cause error }

func (*nativeProbeFailure) Error() string   { return "native media probe failed" }
func (e *nativeProbeFailure) Unwrap() error { return e.cause }

// File is owned by ToolInput; only ReadAt is used by its HTTP adapter and the
// direct parser. The counter reserves every attempted read before provider IO.
type nativeProbeFile struct {
	mediasource.File
	ctx         context.Context
	reauthorize func(context.Context) error
	info        mediasource.Info
	maxBytes    int64
	mu          sync.Mutex
	spent       int64
	sticky      error
	tearingDown bool
}

func (f *nativeProbeFile) Info() mediasource.Info { return f.info }

func (f *nativeProbeFile) failure() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sticky
}

// fail records the first failure and returns it. Once ToolInput tears down,
// a failing read still returns its error, so it delivers no byte, but it is
// not recorded: it is a consequence of that teardown, as for ToolInput itself.
func (f *nativeProbeFile) fail(err error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sticky == nil && !f.tearingDown {
		f.sticky = err
	}
	if f.sticky != nil {
		return f.sticky
	}
	return err
}
func (f *nativeProbeFile) tearDown() {
	f.mu.Lock()
	f.tearingDown = true
	f.mu.Unlock()
}
func (f *nativeProbeFile) ReadAt(p []byte, offset int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := f.failure(); err != nil {
		clear(p)
		return 0, err
	}
	if err := errors.Join(f.ctx.Err(), f.reauthorize(f.ctx)); err != nil {
		clear(p)
		return 0, f.fail(err)
	}
	if offset < 0 {
		clear(p)
		return 0, f.fail(ErrNativeProbeInvalid)
	}
	want := min(int64(len(p)), max(int64(0), f.info.Size-offset))
	f.mu.Lock()
	if want > f.maxBytes-f.spent {
		f.mu.Unlock()
		clear(p)
		return 0, f.fail(nativestorage.ErrToolInputBudget)
	}
	f.spent += want
	f.mu.Unlock()
	n, err := f.File.ReadAt(p, offset)
	after := errors.Join(f.ctx.Err(), f.reauthorize(f.ctx))
	if sticky, ok := f.File.(interface{ Err() error }); ok {
		after = errors.Join(after, sticky.Err())
	}
	// EOF is ordinary only when the known size explains every returned byte.
	//nolint:errorlint // Joined EOF failures must remain terminal.
	if err != nil && (err != io.EOF || int64(n) != want || n >= len(p)) {
		after = errors.Join(after, err)
	}
	if int64(n) != want {
		after = errors.Join(after, io.ErrUnexpectedEOF)
	}
	after = errors.Join(after, f.failure())
	if after != nil {
		clear(p)
		return 0, f.fail(after)
	}
	return n, err
}

type nativeProbeOutput struct {
	data         bytes.Buffer
	limit, spent int64
	retain       bool
	cancel       context.CancelFunc
	err          error
}

func (w *nativeProbeOutput) Write(p []byte) (int, error) {
	if int64(len(p)) > w.limit-w.spent {
		w.err = ErrNativeProbeOutputBudget
		w.cancel()
		return 0, w.err
	}
	w.spent += int64(len(p))
	if w.retain {
		return w.data.Write(p)
	}
	return len(p), nil
}
