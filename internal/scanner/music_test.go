package scanner

import (
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

func TestMusicTrackFromProbeUsesTagsAndPreservesUnknownOrder(t *testing.T) {
	got := musicTrackFromProbe("/Music/Artist/Album/07 - Song.flac", ProbeData{
		Duration: 241,
		FormatTags: map[string]string{
			"title":        "Song",
			"album":        "Album",
			"album_artist": "Artist",
			"artist":       "Guest",
			"date":         "2024-04-01",
			"disc":         "2/3",
			"track":        "7/12",
		},
	})
	if got.Title != "Song" || got.Album != "Album" || got.AlbumArtist != "Artist" || got.Artist != "Guest" {
		t.Fatalf("music tags = %+v", got)
	}
	if got.Year != 2024 || got.DiscNumber != 2 || got.TrackNumber != 7 || got.DurationMS != 241000 {
		t.Fatalf("music numeric tags = %+v", got)
	}

	fallback := musicTrackFromProbe("/Music/Artist/Album/03 - Untagged.mp3", ProbeData{})
	if fallback.Title != "03 - Untagged" || fallback.Album != "Album" || fallback.AlbumArtist != "Artist" {
		t.Fatalf("filesystem fallback = %+v", fallback)
	}
	if fallback.DiscNumber != 0 || fallback.TrackNumber != 0 {
		t.Fatalf("unknown ordering must stay unknown: %+v", fallback)
	}
}

func TestMusicMediaFilePreservesCompletePlaybackProbe(t *testing.T) {
	modified := time.Date(2026, 8, 26, 7, 0, 0, 0, time.UTC)
	track := parsedMusicTrack{
		Path:  "/Music/Bloem Artist/Bloem Album/01 - First Bloom.m4a",
		Title: "First Bloom",
		Probe: ProbeData{
			CodecAudio:    "aac",
			AudioChannels: 2,
			Container:     "mp4",
			Duration:      12,
			Bitrate:       130,
			AudioTracks:   []AudioTrackInfo{{Codec: "aac", Channels: 2, Default: true}},
			Chapters:      []ChapterInfo{},
		},
	}
	got := musicMediaFile(
		&models.MediaFolder{ID: 3}, "album-1", "/Music/Bloem Artist/Bloem Album",
		track, 195735, modified,
	)
	if got.ProbeUpdatedAt == nil || got.ProbeSource != "local" {
		t.Fatalf("probe provenance = source %q updated %v", got.ProbeSource, got.ProbeUpdatedAt)
	}
	if got.CodecAudio != "aac" || got.Duration != 12 || len(got.AudioTracks) != 1 {
		t.Fatalf("playback probe = codec %q duration %d tracks %+v", got.CodecAudio, got.Duration, got.AudioTracks)
	}
	if got.Chapters == nil {
		t.Fatal("chapters must preserve a known-empty probe inventory")
	}
	if NeedsCriticalProbeRepair(&got) {
		t.Fatalf("fresh music scan produced playback-incomplete file: %+v", got)
	}
}
