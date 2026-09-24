package access

import "strings"

var qualityRank = map[string]int{
	"":      0,
	"480P":  1,
	"720P":  2,
	"1080P": 3,
	"2160P": 4,
	"4320P": 5,
}

const (
	PlaybackQualityStandard = "1080p"
	PlaybackQuality4K       = "2160p"
)

// ParsePlaybackQualityPreset normalizes user-facing playback quality presets
// into canonical stored values.
func ParsePlaybackQualityPreset(value string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "", "ANY":
		return "", true
	case "STANDARD", "480P", "720P", "1080P":
		return PlaybackQualityStandard, true
	case "4K", "UHD", "2160P", "4320P":
		return PlaybackQuality4K, true
	default:
		return "", false
	}
}

// NormalizePlaybackQuality collapses legacy and preset values into the
// canonical policy values used throughout the app.
func NormalizePlaybackQuality(value string) string {
	if normalized, ok := ParsePlaybackQualityPreset(value); ok {
		return normalized
	}
	return ""
}

// CompareQuality compares normalized playback qualities.
// It returns -1 when a < b, 0 when equal, and 1 when a > b.
func CompareQuality(a, b string) int {
	aRank := qualityRank[strings.ToUpper(strings.TrimSpace(a))]
	bRank := qualityRank[strings.ToUpper(strings.TrimSpace(b))]
	switch {
	case aRank < bRank:
		return -1
	case aRank > bRank:
		return 1
	default:
		return 0
	}
}

// QualityAllowed reports whether the file quality fits within the ceiling.
func QualityAllowed(fileQuality, ceiling string) bool {
	ceiling = NormalizePlaybackQuality(ceiling)
	if strings.TrimSpace(ceiling) == "" {
		return true
	}
	return CompareQuality(fileQuality, ceiling) <= 0
}

// MinQuality returns the stricter non-empty quality ceiling.
func MinQuality(a, b string) string {
	a = NormalizePlaybackQuality(a)
	b = NormalizePlaybackQuality(b)
	switch {
	case strings.TrimSpace(a) == "":
		return b
	case strings.TrimSpace(b) == "":
		return a
	case CompareQuality(a, b) <= 0:
		return a
	default:
		return b
	}
}
