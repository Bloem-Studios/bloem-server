package playback

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediaprobe"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tonemap"
)

// sourceVideoTool accepts only host-generated commands. Source arguments stay
// private, protocols/demuxers are restricted, and every command opens its own
// authorized immutable input and joins all reads before returning output.
func sourceVideoTool(ctx context.Context, opts TranscodeOpts, bin string, limit int, args ...string) ([]byte, error) {
	if opts.InputSource == nil || opts.InputSource.Open == nil || opts.InputFormat == "" {
		return nil, ErrToneMapSourceValidationUnavailable
	}
	// Tools do not invoke the burn-in adapter recursively. Their source authority
	// and session identity remain the same as the caller's execution recipe.
	opts.SubtitleBurnIn = false
	if err := validateTranscodeInput(opts); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	runCtx, executionOpts, input, err := prepareTranscodeInput(ctx, opts)
	if err != nil {
		return nil, err
	}
	commandArgs := make([]string, 0, len(args)+12)
	found := false
	for _, arg := range args {
		if arg == opts.InputPath {
			found = true
			if len(commandArgs) > 0 && commandArgs[len(commandArgs)-1] == "-i" {
				commandArgs = commandArgs[:len(commandArgs)-1]
				commandArgs = append(commandArgs, sourceInputOptions(opts.InputFormat)...)
				commandArgs = append(commandArgs, "-i")
			} else {
				commandArgs = append(commandArgs, sourceInputOptions(opts.InputFormat)...)
			}
			arg = executionOpts.InputPath
		}
		commandArgs = append(commandArgs, arg)
	}
	if !found {
		return nil, input.complete(ErrToneMapSourceValidationUnavailable)
	}
	output := &sourceToolOutput{limit: limit, cancel: cancel}
	cmd := exec.CommandContext(runCtx, bin, commandArgs...)
	configureTranscodeInputCommand(cmd, input)
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = output, io.Discard, 3*time.Second
	if err := cmd.Start(); err != nil {
		return nil, input.complete(err)
	}
	if err := input.complete(errors.Join(waitTranscodeInputProcess(runCtx, cmd), output.err)); err != nil {
		return nil, err
	}
	return append([]byte(nil), output.Bytes()...), nil
}

// ProbeSourcePrimaryVideoTrack uses the same normalization as catalog probing.
func ProbeSourcePrimaryVideoTrack(ctx context.Context, opts TranscodeOpts) (models.VideoTrack, error) {
	ctx, cancel := context.WithTimeout(ctx, ManifestStartupTimeout)
	defer cancel()
	output, err := sourceVideoTool(ctx, opts, mediaprobe.FFprobePathFromFFmpeg(ResolveFFmpegPath(opts.FFmpegPath)), 2<<20,
		"-v", "error", "-print_format", "json", "-show_streams", "-select_streams", "v", opts.InputPath)
	if err != nil {
		return models.VideoTrack{}, err
	}
	var raw struct {
		Streams []mediaprobe.Stream `json:"streams"`
	}
	if err := json.Unmarshal(output, &raw); err != nil {
		return models.VideoTrack{}, ErrToneMapSourceValidationUnavailable
	}
	for _, stream := range raw.Streams {
		if mediaprobe.IsPrimaryVideoStream(stream) {
			return mediaprobe.NormalizeVideoTrack(stream), nil
		}
	}
	return models.VideoTrack{}, tonemap.ErrSourceRevisionChanged
}

// ValidateSourceToneMap runs after the host validates the real catalog revision
// and immutable binding. It never manufactures filesystem identity. Ambiguous
// sources use the existing representative-frame/output validator without its
// filesystem cache; the runner replaces each source argument with a fresh typed
// input. Local preflight output remains owned by the existing validator.
func ValidateSourceToneMap(ctx context.Context, opts TranscodeOpts) error {
	if opts.InputSource == nil || opts.ToneMapSourceRevision.MediaFileID <= 0 || opts.ToneMapSourceRevision.StreamSignature == "" {
		return ErrToneMapSourceValidationUnavailable
	}
	live, err := ProbeSourcePrimaryVideoTrack(ctx, opts)
	if err != nil {
		return err
	}
	if err := tonemap.ValidateLivePrimaryVideoTrack(opts.ToneMapSourceRevision, live); err != nil {
		return err
	}
	if !opts.ToneMapPreflightRequired {
		return nil
	}
	backend := opts.HWAccel
	if opts.ToneMapMode == tonemap.ModeSoftware {
		backend = tonemap.BackendSoftware
	}
	request := tonemap.SourcePreflightRequest{FFmpegPath: ResolveFFmpegPath(opts.FFmpegPath), FFprobePath: mediaprobe.FFprobePathFromFFmpeg(ResolveFFmpegPath(opts.FFmpegPath)),
		InputPath: opts.InputPath, DurationSeconds: opts.TotalDuration, SourceBitDepth: opts.SourceVideoBitDepth, SoftwareVideoDecode: opts.SoftwareVideoDecode, Mode: opts.ToneMapMode,
		Backend: backend, Filter: opts.ToneMapFilter, Kind: opts.ToneMapSourceKind, RecipeVersion: opts.ToneMapRecipeVersion, HardwareDevice: opts.HWDevice}
	// A zero local revision disables only filesystem validation and caching in
	// this internal call. The host and fresh typed input establish source identity.
	err = tonemap.ValidateSourceWithRunner(ctx, request, func(ctx context.Context, bin string, args ...string) ([]byte, error) {
		if bin != request.FFmpegPath && bin != request.FFprobePath {
			return nil, ErrToneMapSourceValidationUnavailable
		}
		for _, arg := range args {
			if arg == opts.InputPath {
				return sourceVideoTool(ctx, opts, bin, 2<<20, args...)
			}
		}
		return sourceCapabilityOutput(ctx, bin, args...)
	})
	return err
}
