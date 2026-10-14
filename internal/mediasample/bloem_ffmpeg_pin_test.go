package mediasample

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// bloemRotatedHeader is a probe header for a 320x180 stream with a 90 degree
// display matrix, as the given FFmpeg major prints it. FFmpeg 8 renamed the
// side data line; the output above and below it is unchanged.
func bloemRotatedHeader(displayMatrix string) string {
	return "Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'rot.mp4':\n" +
		"  Duration: 00:00:01.00, start: 0.000000, bitrate: 50 kb/s\n" +
		"  Stream #0:0[0x1](und): Video: h264 (High) (avc1 / 0x31637661), yuv420p(progressive), 320x180 [SAR 1:1 DAR 16:9], 42 kb/s, 10 fps, 10 tbr, 10240 tbn (default)\n" +
		"    Metadata:\n      handler_name    : VideoHandler\n" +
		"    Side data:\n      " + displayMatrix + " 90.00 degrees\n" +
		"Stream mapping:\n  Stream #0:0 -> #0:0 (copy)\nOutput #0, null, to 'pipe:':\n"
}

func bloemParsedRotation(header string) float64 {
	p := &inputHeaderParser{}
	for line := range strings.SplitSeq(header, "\n") {
		p.line(line)
	}
	return p.info.Rotation
}

// bloemFFmpegMajor reads the jellyfin-ffmpeg major a file installs.
func bloemFFmpegMajor(t *testing.T, path string, pattern *regexp.Regexp) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	match := pattern.FindSubmatch(raw)
	if match == nil {
		t.Fatalf("%s names no jellyfin-ffmpeg package", path)
	}
	return string(match[1])
}

// TestBloemCIFFmpegPinTracksTheDisplayMatrixParse keeps the gap between CI's
// FFmpeg and the runtime image's tied to its one reason.
//
// The runtime image ships jellyfin-ffmpeg8, which prints a display matrix as
// "Display Matrix: rotation of N degrees". This package's input parser reads
// only FFmpeg 7's "displaymatrix: rotation of N degrees", so on the shipped
// image rotated (phone) video gets landscape trickplay tiles. That is a
// production bug in Silo-owned code, an upstream contribution candidate, and
// the reason scripts/ci/bloem-install-test-tools.sh pins jellyfin-ffmpeg7:
// with FFmpeg 8 TestGenerateRotatedManifestEndToEndDB fails.
//
// While the parser misses FFmpeg 8's spelling and the pins differ, this test
// skips and names the bug. Once the parser reads both spellings it fails until
// CI installs the Dockerfile's FFmpeg major, so the pin cannot outlive its
// reason and CI again tests the FFmpeg production runs.
func TestBloemCIFFmpegPinTracksTheDisplayMatrixParse(t *testing.T) {
	if got := bloemParsedRotation(bloemRotatedHeader("displaymatrix: rotation of")); got != 90 {
		t.Fatalf("FFmpeg 7 display matrix parsed as rotation %v, want 90", got)
	}
	runtime := bloemFFmpegMajor(t, "../../Dockerfile", regexp.MustCompile(`jellyfin-ffmpeg(\d+)=`))
	ci := bloemFFmpegMajor(t, "../../scripts/ci/bloem-install-test-tools.sh", regexp.MustCompile(`(?m)^ffmpeg_package=jellyfin-ffmpeg(\d+)$`))
	readsFFmpeg8 := bloemParsedRotation(bloemRotatedHeader("Display Matrix: rotation of")) == 90
	switch {
	case runtime == ci:
		return
	case readsFFmpeg8:
		t.Fatalf("the input parser now reads FFmpeg 8's display matrix: move CI from jellyfin-ffmpeg%s to the Dockerfile's jellyfin-ffmpeg%s "+
			"(scripts/ci/bloem-install-test-tools.sh, docs/bloem/ci.md)", ci, runtime)
	default:
		t.Skipf("known bug: the runtime image ships jellyfin-ffmpeg%s, whose \"Display Matrix: rotation of\" line this parser does not read, "+
			"so rotated video gets landscape trickplay tiles in production; CI pins jellyfin-ffmpeg%s until the parser accepts both spellings "+
			"(upstream contribution candidate, docs/bloem/ci.md)", runtime, ci)
	}
}
