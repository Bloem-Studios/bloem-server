package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Silo-Server/silo-server/internal/fspath"
	"github.com/Silo-Server/silo-server/internal/procgroup"
	"github.com/Silo-Server/silo-server/internal/tonemap"
)

// errPrepareSourceUnbound refuses a native input that arrived without a
// source: its catalog path is a reference, never a file ffmpeg may open.
var errPrepareSourceUnbound = errors.New("prepare-file: source input is not available")

// prepareContextFile is PrepareFile's hook for inputs that are not files on
// this host. A source installed on opts, bound to the request (a worker's
// native grant) or offered by the request's context-scoped media host (the
// API's download preparation) is prepared through the supervised input
// bridge. A native location without one is refused rather than handed to
// ffmpeg. Every other input reports false, and PrepareFile reads its path.
func prepareContextFile(ctx context.Context, opts TranscodeOpts, outputPath string) (bool, error) {
	if opts.InputSource != nil {
		return true, prepareSourceFile(ctx, opts, outputPath)
	}
	if !TranscodeInputSourceMatches(ctx, opts.InputPath) && contextMediaHost(ctx, opts.InputPath) == nil {
		if procgroup.NativeLocation(opts.InputPath) {
			return true, errPrepareSourceUnbound
		}
		return false, nil
	}
	opts, err := bindTranscodeInput(ctx, opts)
	if err != nil {
		return true, fmt.Errorf("prepare-file: %w", err)
	}
	if opts.InputSource == nil {
		return true, errPrepareSourceUnbound
	}
	return true, prepareSourceFile(ctx, opts, outputPath)
}

// prepareSourceFile is PrepareFile for a typed source. It follows PrepareFile
// step by step: the same encoder, hardware and tone-map resolution, the same
// arguments (with the source's fixed demuxer), the same software retry and
// the same atomic finalization. Each run opens the source afresh, so a retry
// rechecks the source's authority. ffmpeg's diagnostics are discarded, as for
// every native execution, because they would name the private input address.
func prepareSourceFile(ctx context.Context, opts TranscodeOpts, outputPath string) error {
	if outputPath == "" {
		return errors.New("prepare-file: empty output path")
	}
	opts = normalizeTranscodeOptsContext(ctx, opts)
	if err := validateTranscodeInput(opts); err != nil {
		return fmt.Errorf("prepare-file: %w", err)
	}
	if err := validateToneMapOpts(opts); err != nil {
		return fmt.Errorf("prepare-file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil { //nolint:gosec // as PrepareFile: the node serves this directory
		return fmt.Errorf("prepare-file: create output dir: %w", err)
	}
	partPath := outputPath + ".part"
	_ = os.Remove(partPath)

	hwDevice, releaseHWDevice := AcquireHWDevice(opts.HWDevice, opts.HWAccel)
	opts.HWDevice = hwDevice
	defer releaseHWDevice()
	var err error
	if opts, err = resolveHEVCTranscodeEncoder(ctx, opts); err != nil {
		return fmt.Errorf("prepare-file: %w", err)
	}
	if opts, err = resolveVAAPIRateControl(ctx, opts); err != nil {
		return fmt.Errorf("prepare-file: %w", err)
	}
	if opts.HWAccel == transcodeHWNone {
		releaseHWDevice()
	}
	// A typed source validates its tone-map revision through its own Validate.
	if err := validateToneMapSource(ctx, opts); err != nil {
		return fmt.Errorf("prepare-file: %w", err)
	}

	logs := newPrepareLogWriter(ctx, opts)
	err = runSourcePrepare(ctx, opts, partPath, logs)
	if err != nil && ctx.Err() == nil && opts.ToneMapMode != tonemap.ModeHardware {
		if retryAccel := StartupRetryHWAccel(opts); retryAccel != opts.HWAccel {
			slog.WarnContext(ctx, "prepared encode failed; retrying with software encoding",
				"hw_accel", opts.HWAccel, "output", outputPath, "error", err)
			logs.event("retrying with software encoding", "")
			retryOpts := opts
			retryOpts.HWAccel = retryAccel
			err = runSourcePrepare(ctx, retryOpts, partPath, logs)
		}
	}
	if err != nil {
		return err
	}
	if err := syncPreparedFile(partPath); err != nil {
		_ = os.Remove(partPath)
		return fmt.Errorf("prepare-file: sync artifact: %w", err)
	}
	if err := os.Rename(partPath, outputPath); err != nil {
		_ = os.Remove(partPath)
		return fmt.Errorf("prepare-file: finalize artifact: %w", err)
	}
	if err := syncPreparedDirectory(filepath.Dir(outputPath)); err != nil {
		return fmt.Errorf("prepare-file: sync artifact directory: %w", err)
	}
	return nil
}

// runSourcePrepare runs one ffmpeg encode from a freshly opened source. It
// returns only after the child and every source read have joined.
func runSourcePrepare(ctx context.Context, opts TranscodeOpts, partPath string, logs *prepareLogWriter) error {
	runCtx, executionOpts, input, err := prepareTranscodeInput(ctx, opts)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTranscodeFailed, err)
	}
	args := restrictSourceInputArgs(buildPrepareFileArgs(executionOpts, partPath), opts.InputFormat)
	bin := opts.FFmpegPath
	if bin == "" {
		bin = ffmpegBinary()
	}
	cmd := fspath.CommandContext(runCtx, bin, args...)
	configureTranscodeInputCommand(cmd, input)
	cmd.Stderr = io.Discard
	if opts.PrepareProgressSink != nil {
		// As PrepareFile's own run: copied video keeps the source's own
		// timing, so a remux reads out_time alone.
		frameRate := opts.SourceFrameRate
		if opts.AudioOnly || opts.TargetCodecVideo == codecCopyV3 {
			frameRate = 0
		}
		cmd.Stdout = newPrepareProgressWriter(opts.PrepareProgressSink, opts.TotalDuration, frameRate)
	}
	cmd.WaitDelay = 3 * time.Second
	if logs != nil {
		logs.setHWAccel(opts.HWAccel)
	}
	logs.event("ffmpeg process starting", "")
	if err := cmd.Start(); err != nil {
		_ = os.Remove(partPath)
		return fmt.Errorf("%w: %w", ErrTranscodeFailed, input.complete(err))
	}
	err = input.complete(waitTranscodeInputProcess(runCtx, cmd))
	logs.flush()
	if err != nil {
		_ = os.Remove(partPath)
		logs.event("ffmpeg process exit error", formatWaitError(err))
		return fmt.Errorf("%w: %w", ErrTranscodeFailed, err)
	}
	logs.event("ffmpeg process exited", "")
	return nil
}
