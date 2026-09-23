package playback

import (
	"strings"

	"github.com/Silo-Server/silo-server/internal/models"
)

// IsFragileLosslessAudioCodec reports codecs whose software decode path is
// unreliable under HLS remux/transcode, notably TrueHD/MLP.
func IsFragileLosslessAudioCodec(codec string) bool {
	c := strings.ToLower(strings.TrimSpace(codec))
	if c == "" {
		return false
	}
	return strings.Contains(c, "truehd") || strings.Contains(c, "mlp")
}

// AudioTrackChannelsAt returns the channel count for tracks[index], or 0 when
// the inventory is missing or the index is out of range.
func AudioTrackChannelsAt(tracks []models.AudioTrack, index int) int {
	if index < 0 || index >= len(tracks) {
		return 0
	}
	return tracks[index].Channels
}
