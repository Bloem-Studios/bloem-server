package scanner

import (
	"context"
	"math"
	"path"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/mediasource"
)

// NativeAudioProbe retains exact native timing alongside Silo's ordinary
// projection. Catalog and delivery consumers must use DurationSeconds and
// Chapters when forming a native presentation's timeline.
type NativeAudioProbe struct {
	Probe           *ProbeData
	DurationSeconds float64
	Chapters        []ChapterInfo
	Tags            map[string]string
}

// NativeAudioDemuxer admits the finite audio containers exercised through the
// pinned probe bridge. The filename selects a reader; actual bytes are probed.
func NativeAudioDemuxer(name string) (NativeProbeDemuxer, bool) {
	switch strings.ToLower(path.Ext(name)) {
	case ".m4a", ".m4b":
		return NativeProbeMOV, true
	case ".mp3":
		return NativeProbeMP3, true
	case ".flac":
		return NativeProbeFLAC, true
	case ".ogg", ".opus":
		return NativeProbeOgg, true
	case ".wav":
		return NativeProbeWAV, true
	default:
		return 0, false
	}
}

// ProbeNativeAudio uses the existing bounded, authorized tool lifecycle and
// Silo's metadata and chapter parsers. No local path or provider URL is read.
func ProbeNativeAudio(ctx context.Context, ffprobePath string, file mediasource.File, cfg NativeProbeConfig) (*NativeAudioProbe, error) {
	switch cfg.Demuxer {
	case NativeProbeMOV, NativeProbeMP3, NativeProbeFLAC, NativeProbeOgg, NativeProbeWAV:
	default:
		return nil, ErrNativeProbeInvalid
	}
	probe, raw, err := probeNative(ctx, ffprobePath, file, cfg)
	if err != nil {
		return nil, err
	}
	if len(probe.VideoTracks) != 0 || len(probe.AudioTracks) != 1 || probe.CodecAudio == "" {
		return nil, ErrNativeProbeInvalid
	}
	duration, err := strconv.ParseFloat(raw.Format.Duration, 64)
	if err != nil || !durationIsPositiveFinite(duration) || duration > maxReasonableAudioDurationSeconds {
		return nil, ErrNativeProbeInvalid
	}
	for _, chapter := range raw.Chapters {
		start, startErr := strconv.ParseFloat(string(chapter.StartTime), 64)
		end, endErr := strconv.ParseFloat(string(chapter.EndTime), 64)
		if startErr != nil || endErr != nil || math.IsNaN(start) || math.IsInf(start, 0) ||
			math.IsNaN(end) || math.IsInf(end, 0) || start < 0 || end <= start || end > duration {
			return nil, ErrNativeProbeInvalid
		}
	}
	// A zero limit asks Silo's parser to normalize chapters without clamping
	// them to its truncated duration; exact endpoints were checked above.
	chapters := normalizeChapters(raw.Chapters, 0)
	// Ogg carries book tags on its audio stream. Use Silo's normalizer, with
	// format-level tags taking precedence where both carry a value.
	tags := make(map[string]string)
	for _, stream := range raw.Streams {
		if stream.CodecType == "audio" {
			for key, value := range normalizeFormatTags(stream.Tags) {
				tags[key] = value
			}
			break
		}
	}
	for key, value := range probe.FormatTags {
		tags[key] = value
	}
	return &NativeAudioProbe{Probe: probe, DurationSeconds: duration, Chapters: chapters, Tags: tags}, nil
}
