package playback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/httpstream"
	"github.com/Silo-Server/silo-server/internal/processmetrics"
)

func sourceInputOptions(format string) []string {
	args := []string{"-protocol_whitelist", "http,tcp", "-http_proxy", "", "-f", format, "-format_whitelist", format}
	if format == "mov" {
		args = append(args, "-enable_drefs", "0", "-use_absolute_path", "0")
	}
	return args
}

func restrictSourceInputArgs(args []string, format string) []string {
	for index, arg := range args {
		if arg == "-i" {
			out := make([]string, 0, len(args)+12)
			out = append(out, args[:index]...)
			out = append(out, sourceInputOptions(format)...)
			return append(out, args[index:]...)
		}
	}
	return args
}

type sourceToolOutput struct {
	bytes.Buffer
	limit  int
	cancel context.CancelFunc
	err    error
}

func (w *sourceToolOutput) Write(data []byte) (int, error) {
	if len(data) > w.limit-w.Len() {
		w.err = errors.New("playback source tool output exceeded budget")
		w.cancel()
		return 0, w.err
	}
	return w.Buffer.Write(data)
}

func sourceCapabilityOutput(ctx context.Context, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output := &sourceToolOutput{limit: 128 << 10, cancel: cancel}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env, cmd.Stdout, cmd.Stderr, cmd.WaitDelay = transcodeInputEnvironment(), output, io.Discard, 3*time.Second
	configureTranscodeInputProcess(cmd)
	if err := cmd.Start(); err != nil {
		return nil, &transcodeInputFailure{err}
	}
	if err := errors.Join(waitTranscodeInputProcess(ctx, cmd), output.err); err != nil {
		return nil, &transcodeInputFailure{err}
	}
	return output.Bytes(), nil
}

// SourceDVRPUStrippable performs the existing bounded head-of-video strip
// preflight through fresh source authority. A zero child exit cannot hide a
// per-packet filter rejection. It certifies only that short head, not the title.
func SourceDVRPUStrippable(ctx context.Context, ffmpegPath, inputPath string, source *TranscodeInputSource, inputFormat string) (bool, error) {
	if source == nil {
		return false, fmt.Errorf("source input is required")
	}
	opts := TranscodeOpts{InputPath: inputPath, InputSource: source, InputFormat: inputFormat}
	if err := validateTranscodeInput(opts); err != nil {
		return false, err
	}
	bin := ResolveFFmpegPath(ffmpegPath)
	capabilities, err := sourceCapabilityOutput(ctx, bin, "-hide_banner", "-bsfs")
	if err != nil {
		return false, err
	}
	if !bytes.Contains(capabilities, []byte("dovi_rpu")) || !bytes.Contains(capabilities, []byte("filter_units")) {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, dvRPUProbeTimeout)
	defer cancel()
	runCtx, executionOpts, input, err := prepareTranscodeInput(ctx, opts)
	if err != nil {
		return false, err
	}
	args := []string{"-hide_banner", "-nostats", "-v", "error", "-i", executionOpts.InputPath, "-t", strconv.Itoa(dvRPUProbeSeconds), "-map", "0:v:0", "-c:v", "copy", "-bsf:v", DV7ToHDR10BitstreamFilter, "-an", "-sn", "-dn", "-f", "null", "-"}
	cmd := exec.CommandContext(runCtx, bin, restrictSourceInputArgs(args, inputFormat)...)
	configureTranscodeInputCommand(cmd, input)
	output := &sourceToolOutput{limit: maxDVRPUProbeOutput, cancel: cancel}
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = io.Discard, output, 3*time.Second
	if err := cmd.Start(); err != nil {
		return false, input.complete(err)
	}
	err = input.complete(errors.Join(waitTranscodeInputProcess(runCtx, cmd), output.err))
	if err != nil {
		return false, err
	}
	return !dvRPUOutputFailed(output.String()), nil
}

// ResolveSourceCopySeekAnchor observes the same first copied packet as the
// transport using a fresh authorized source. It does not cache ephemeral tool
// arguments or share viewer authority between requests.
func ResolveSourceCopySeekAnchor(ctx context.Context, ffmpegPath, inputPath string, source *TranscodeInputSource, inputFormat string, requested float64, segmentDuration int) (float64, int, error) {
	if math.IsNaN(requested) || math.IsInf(requested, 0) {
		return 0, 0, fmt.Errorf("invalid source seek")
	}
	if requested <= 0 {
		return 0, 0, nil
	}
	opts := TranscodeOpts{InputPath: inputPath, InputSource: source, InputFormat: inputFormat}
	if source == nil {
		return 0, 0, fmt.Errorf("source input is required")
	}
	if err := validateTranscodeInput(opts); err != nil {
		return 0, 0, err
	}
	if segmentDuration <= 0 {
		segmentDuration = DefaultSegmentDuration
	}
	ctx, cancel := context.WithTimeout(ctx, copySeekProbeTimeout)
	defer cancel()
	select {
	case copySeekProbeSlots <- struct{}{}:
		defer func() { <-copySeekProbeSlots }()
	case <-ctx.Done():
		return 0, 0, &transcodeInputFailure{ctx.Err()}
	}
	runCtx, executionOpts, input, err := prepareTranscodeInput(ctx, opts)
	if err != nil {
		return 0, 0, err
	}
	args := []string{"-v", "error", "-fflags", "+genpts+fastseek", "-analyzeduration", "3000000", "-probesize", "5000000", "-ss", fmt.Sprintf("%.3f", requested), "-i", executionOpts.InputPath,
		"-map", "0:V:0", "-c:v", "copy", "-copyts", "-start_at_zero", "-avoid_negative_ts", "disabled", "-frames:v", "1", "-f", "framecrc", "-"}
	cmd := exec.CommandContext(runCtx, ResolveFFmpegPath(ffmpegPath), restrictSourceInputArgs(args, inputFormat)...)
	configureTranscodeInputCommand(cmd, input)
	stdout := &sourceToolOutput{limit: 64 << 10, cancel: cancel}
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = stdout, io.Discard, 3*time.Second
	if err := cmd.Start(); err != nil {
		return 0, 0, input.complete(err)
	}
	if err := input.complete(errors.Join(waitTranscodeInputProcess(runCtx, cmd), stdout.err)); err != nil {
		return 0, 0, err
	}
	return parseSourceCopyAnchor(stdout.Bytes(), segmentDuration)
}

func parseSourceCopyAnchor(output []byte, segmentDuration int) (float64, int, error) {
	var numerator, denominator int64
	for line := range bytes.SplitSeq(output, []byte("\n")) {
		text := strings.TrimSpace(string(line))
		if strings.HasPrefix(text, "#tb 0:") {
			first, second, found := strings.Cut(strings.TrimSpace(strings.TrimPrefix(text, "#tb 0:")), "/")
			if found {
				numerator, _ = strconv.ParseInt(first, 10, 64)
				denominator, _ = strconv.ParseInt(second, 10, 64)
			}
			continue
		}
		if numerator <= 0 || denominator <= 0 || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Split(text, ",")
		if len(fields) < 3 || strings.TrimSpace(fields[0]) != "0" {
			continue
		}
		timestamp := strings.TrimSpace(fields[2])
		if timestamp == "" || strings.EqualFold(timestamp, "N/A") {
			timestamp = strings.TrimSpace(fields[1])
		}
		ticks, err := strconv.ParseInt(timestamp, 10, 64)
		seconds := float64(ticks) * float64(numerator) / float64(denominator)
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			continue
		}
		seconds = math.Max(0, seconds)
		return seconds, int(seconds / float64(segmentDuration)), nil
	}
	return 0, 0, fmt.Errorf("source seek returned no video packet timestamp")
}

type sourceRemuxWriter struct {
	writer      *httpstream.RollingDeadlineWriter
	contentType string
	started     bool
	cancel      context.CancelFunc
}

func (w *sourceRemuxWriter) Write(data []byte) (int, error) {
	if !w.started {
		w.writer.Header().Set("Content-Type", w.contentType)
		w.writer.WriteHeader(http.StatusOK)
		w.started = true
	}
	n, err := w.writer.Write(data)
	if err == nil {
		w.writer.Flush()
	} else {
		w.cancel()
	}
	return n, err
}

// ServeSourceRemux streams a request-owned typed source using the existing
// remux recipe builders. Return/abort occurs only after the child and all input
// IO have joined. An input failure after binary output aborts the HTTP response.
func ServeSourceRemux(w http.ResponseWriter, r *http.Request, opts TranscodeOpts, outputFormat string, transcodeAudio bool, dvProfile int, serve RemuxServeOptions) error {
	if opts.InputSource == nil {
		return fmt.Errorf("source input is required")
	}
	if err := validateTranscodeInput(opts); err != nil {
		return err
	}
	switch outputFormat {
	case "mp4", "webm", "matroska", "mpegts":
	default:
		return fmt.Errorf("unsupported source remux output format")
	}
	if math.IsNaN(opts.SeekSeconds) || math.IsInf(opts.SeekSeconds, 0) || opts.SeekSeconds < 0 {
		return fmt.Errorf("invalid source remux seek")
	}
	if serve.DVMode != "" && serve.DVMode != RemuxDVLegacyAutoV3 && serve.DVMode != RemuxDVPreserveV3 && serve.DVMode != RemuxDVRejectP7V3 && serve.DVMode != RemuxDVStripToHDR10V3 {
		return fmt.Errorf("unsupported source remux Dolby Vision mode")
	}
	bin := serve.FFmpegPath
	if bin == "" {
		bin = opts.FFmpegPath
	}
	effectiveProfile := dvProfile
	tagSampleEntry := serve.DVMode == RemuxDVPreserveV3
	if serve.DVMode == RemuxDVStripToHDR10V3 || dvProfile == 7 && (serve.DVMode == "" || serve.DVMode == RemuxDVLegacyAutoV3) {
		if dvProfile != 7 && dvProfile != 8 {
			return fmt.Errorf("source HDR10 strip requires Dolby Vision profile 7 or 8")
		}
		strippable, err := SourceDVRPUStrippable(r.Context(), bin, opts.InputPath, opts.InputSource, opts.InputFormat)
		if err != nil {
			return err
		}
		if !strippable {
			return fmt.Errorf("source Dolby Vision RPU strip unavailable")
		}
		effectiveProfile, tagSampleEntry = 7, serve.DVMode == RemuxDVStripToHDR10V3
	} else if dvProfile == 7 {
		return fmt.Errorf("source Dolby Vision profile 7 cannot be preserved")
	}
	dropLeading := serve.DropResumeLeadingPictures && opts.SeekSeconds > 0 && !serve.AudioOnly
	if dropLeading {
		capabilities, err := sourceCapabilityOutput(r.Context(), ResolveFFmpegPath(bin), "-hide_banner", "-h", "bsf=noise")
		dropLeading = err == nil && strings.Contains(string(capabilities), "-drop")
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	runCtx, executionOpts, input, err := prepareTranscodeInput(ctx, opts)
	if err != nil {
		return err
	}
	args := buildRemuxArgsWithLeadingPictureDropV3(executionOpts.InputPath, outputFormat, opts.SeekSeconds, transcodeAudio, opts.AudioTrackIndex, effectiveProfile,
		tagSampleEntry, serve.AudioOnly, serve.SourceAudioChannels, serve.TargetAudioChannels, serve.TargetAudioBitrateKbps, dropLeading)
	cmd := exec.CommandContext(runCtx, ResolveFFmpegPath(bin), restrictSourceInputArgs(args, opts.InputFormat)...)
	configureTranscodeInputCommand(cmd, input)
	streamWriter := httpstream.NewRollingDeadlineWriter(w)
	contentType := serve.ContentType
	if contentType == "" {
		contentType = containerMIME(outputFormat)
	}
	output := &sourceRemuxWriter{writer: streamWriter, contentType: contentType, cancel: cancel}
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = output, io.Discard, 3*time.Second
	if err := cmd.Start(); err != nil {
		return input.complete(err)
	}
	served, watcherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-serve.Abort:
			cancel()
			_ = streamWriter.Abort()
		case <-served:
		}
	}()
	err = waitTranscodeInputProcess(runCtx, cmd)
	processmetrics.Record(processmetrics.Remux, cmd.ProcessState, err, runCtx.Err())
	err = input.complete(err)
	close(served)
	<-watcherDone
	if err != nil && output.started {
		panic(http.ErrAbortHandler)
	}
	return err
}
