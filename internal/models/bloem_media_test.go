package models

import "testing"

// Bloem's music base type, kept out of Silo's media_test.go
// TestMediaFileIsAudioOnly table.
func TestBloemMusicFileIsAudioOnly(t *testing.T) {
	file := &MediaFile{BaseType: "music", CodecAudio: "aac", AudioTracks: []AudioTrack{{Codec: "aac"}}}
	if !file.IsAudioOnly() {
		t.Fatal("IsAudioOnly() = false for music with no video stream, want true")
	}
}
