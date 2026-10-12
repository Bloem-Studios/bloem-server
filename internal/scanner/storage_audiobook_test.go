package scanner

import (
	"testing"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

func audioTestTrack(id string, disc, number uint32) StorageAudioTrack {
	return StorageAudioTrack{Entry: &storagev1.Entry{Id: id, Name: id + ".mp3", LogicalPath: "Book/" + id + ".mp3", Revision: "r1", Size: 100, Kind: storagev1.EntryKind_ENTRY_KIND_FILE, AudioFile: &storagev1.AudioFileMetadata{DiscNumber: disc, TrackNumber: number}}, Status: StorageAudioTrackOK, Probe: &NativeAudioProbe{Probe: &ProbeData{Duration: 2, CodecAudio: "mp3", Container: "mp3"}, DurationSeconds: 2.75, Tags: map[string]string{}}}
}

func TestNativeAudioBookMetadataAuthority(t *testing.T) {
	track := audioTestTrack("1", 1, 1)
	track.Probe.Tags = map[string]string{"album": "Tagged book", "artist": "Tagged author", "asin": " ab-c "}
	book, authors, _, _, provider := nativeAudioBookMetadata([]StorageAudioTrack{track})
	if provider || book.Title != "Tagged book" || len(authors) != 1 || authors[0] != "Tagged author" {
		t.Fatalf("tags: book=%+v authors=%v provider=%v", book, authors, provider)
	}
	track.Entry.Audiobook = &storagev1.AudiobookMetadata{Title: "Provider book", Authors: []string{"Provider author"}, Isbn: " 978-0 123 "}
	book, authors, _, isbn, provider := nativeAudioBookMetadata([]StorageAudioTrack{track})
	if !provider || book.Title != "Provider book" || authors[0] != "Provider author" || isbn != "9780123" || normalizeNativeAudioASIN(book.ASIN) != "AB-C" {
		t.Fatalf("provider: book=%+v authors=%v isbn=%s", book, authors, isbn)
	}
	if got := normalizeNativeAudioASIN(" ab-c "); got != "AB-C" {
		t.Fatalf("ASIN normalized to %q", got)
	}
}

func TestOrderNativeAudioTracks(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tracks  []StorageAudioTrack
		want    []string
		invalid bool
	}{
		{"positions", []StorageAudioTrack{audioTestTrack("b", 2, 1), audioTestTrack("a", 1, 1)}, []string{"a", "b"}, false},
		{"numeric names", []StorageAudioTrack{audioTestTrack("10", 0, 0), audioTestTrack("2", 0, 0)}, []string{"2", "10"}, false},
		{"repeated positions", []StorageAudioTrack{audioTestTrack("a", 1, 1), audioTestTrack("b", 1, 1)}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := orderNativeAudioTracks(tc.tracks)
			if tc.invalid {
				if err == nil {
					t.Fatal("duplicate position admitted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for i, id := range tc.want {
				if got[i].Entry.Id != id {
					t.Fatalf("part%d=%s want%s", i, got[i].Entry.Id, id)
				}
			}
		})
	}
}

func TestNativeAudioTrackChapters(t *testing.T) {
	tracks := []StorageAudioTrack{audioTestTrack("1", 1, 1), audioTestTrack("2", 1, 2)}
	tracks[1].Probe.Chapters = []ChapterInfo{{Index: 0, StartSeconds: 0, EndSeconds: 2.75, Title: "Embedded", Source: "embedded"}}
	if err := validateNativeAudioBook(tracks); err != nil {
		t.Fatal(err)
	}
	first := nativeAudioTrackChapters(tracks[0], 1, 2)
	if len(first) != 1 || first[0].Title != "Part 1" || first[0].EndSeconds != 2.75 || first[0].Source != "" {
		t.Fatalf("chapters=%+v", first)
	}
	embedded := nativeAudioTrackChapters(tracks[1], 2, 2)
	if len(embedded) != 1 || embedded[0].EndSeconds != 2.75 || embedded[0].Source != "embedded" {
		t.Fatalf("embedded=%+v", embedded)
	}
	if got := nativeAudioTrackChapters(tracks[0], 1, 1); len(got) != 0 {
		t.Fatalf("single fallback=%+v", got)
	}
	tracks[0].Probe.DurationSeconds = .5
	if err := validateNativeAudioBook(tracks); err == nil {
		t.Fatal("subsecond track admitted")
	}
}
