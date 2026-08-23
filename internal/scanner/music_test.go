package scanner

import "testing"

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
