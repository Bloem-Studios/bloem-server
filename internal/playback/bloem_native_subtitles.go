package playback

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ExtractSourceSubtitle returns a complete, bounded extract only after the
// child and authorized source reads have joined. Partial output is discarded.
func ExtractSourceSubtitle(ctx context.Context, opts TranscodeOpts, track int, format string, seek, duration float64) ([]byte, error) {
	if track < 0 || math.IsNaN(seek) || math.IsInf(seek, 0) || seek < 0 || math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 0 {
		return nil, errors.New("invalid source subtitle request")
	}
	muxer, codec := format, format
	switch format {
	case "vtt":
		muxer, codec = "webvtt", "webvtt"
	case "srt", "ass":
	case "sup":
		codec = "copy"
	default:
		return nil, errors.New("unsupported source subtitle format")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	args := []string{"-v", "error", "-nostdin", "-i", opts.InputPath}
	if format == "sup" {
		args = append(args, "-copyts")
	}
	if seek > 0 {
		args = append(args, "-ss", strconv.FormatFloat(seek, 'f', -1, 64))
	}
	if duration > 0 {
		args = append(args, "-t", strconv.FormatFloat(duration, 'f', -1, 64))
	}
	args = append(args, "-map", fmt.Sprintf("0:s:%d", track), "-c:s", codec, "-f", muxer, "pipe:1")
	return sourceVideoTool(ctx, opts, ResolveFFmpegPath(opts.FFmpegPath), 32<<20, args...)
}

// prepareSourceSubtitleInput modifies only the execution copy. The durable
// recipe keeps the catalog's subtitle ordinal and opaque source identity.
func prepareSourceSubtitleInput(ctx context.Context, opts TranscodeOpts) (TranscodeOpts, error) {
	if !opts.SubtitleBurnIn || opts.SubtitleTrackIndex < 0 || NeedsBurnIn(opts.SubtitleCodec) {
		return opts, nil
	}
	data, err := ExtractSourceSubtitle(ctx, opts, opts.SubtitleTrackIndex, "ass", 0, 0)
	if err != nil {
		return opts, err
	}
	path := filepath.Join(opts.OutputDir, "native-subtitles.ass")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return opts, err
	}
	if IsASS(opts.SubtitleCodec) {
		fonts, err := ExtractSourceSubtitleFonts(ctx, opts)
		if err != nil {
			return opts, err
		}
		if len(fonts) > 0 {
			dir := filepath.Join(opts.OutputDir, "native-subtitle-fonts")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return opts, err
			}
			for i, font := range fonts {
				name := fmt.Sprintf("font-%d%s", i, filepath.Ext(font.Name))
				if err := os.WriteFile(filepath.Join(dir, name), font.Data, 0o600); err != nil {
					return opts, err
				}
			}
			opts.subtitleFilterFontsDir = dir
		}
	}
	opts.subtitleFilterInputPath, opts.SubtitleTrackIndex = path, 0
	return opts, nil
}

func appendSourceSubtitleFonts(filter string, opts TranscodeOpts) string {
	if opts.subtitleFilterFontsDir != "" {
		return filter + ":fontsdir='" + escapeFilterPath(opts.subtitleFilterFontsDir) + "'"
	}
	return filter
}
