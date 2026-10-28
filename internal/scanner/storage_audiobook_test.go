package scanner

import (
	"encoding/json"
	"strconv"
	"strings"
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

func TestStorageAudioTrackFromEntryUsesListedMetadata(t *testing.T) {
	entry := func(name string, meta *storagev1.AudioFileMetadata) *storagev1.Entry {
		return &storagev1.Entry{Id: "book/1", Name: name, Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Revision: "r1", AudioFile: meta}
	}
	listed := &storagev1.AudioFileMetadata{DurationSeconds: 3600.5, Codec: "AAC", Container: "m4b", Bitrate: 64000, SampleRate: 44100, Channels: 2,
		Chapters: []*storagev1.AudioChapter{{Id: "a", Title: "One", StartSeconds: 0, EndSeconds: 1800}, {Id: "b", Title: "Two", StartSeconds: 1800, EndSeconds: 3600.5}}}
	track, ok := StorageAudioTrackFromEntry(entry("book.m4b", listed))
	if !ok || track.Status != StorageAudioTrackOK || track.Probe == nil || track.Probe.Probe == nil {
		t.Fatalf("listed metadata not used: %+v %v", track, ok)
	}
	p := track.Probe.Probe
	if p.Container != "mp4" || p.CodecAudio != "aac" || p.Bitrate != 64 || p.Duration != 3600 || len(p.AudioTracks) != 1 || p.AudioTracks[0].Channels != 2 || p.AudioTracks[0].SampleRate != 44100 {
		t.Fatalf("probe facts: %+v", p)
	}
	if track.Probe.DurationSeconds != 3600.5 || len(track.Probe.Chapters) != 2 || track.Probe.Chapters[1].Title != "Two" || track.Probe.Chapters[1].StartSeconds != 1800 {
		t.Fatalf("timeline: %+v", track.Probe)
	}
	if mp3, ok := StorageAudioTrackFromEntry(entry("part.mp3", &storagev1.AudioFileMetadata{DurationSeconds: 10, Codec: "mp3"})); !ok || mp3.Probe.Probe.Container != "mp3" {
		t.Fatalf("mp3: %+v %v", mp3, ok)
	}
	for name, e := range map[string]*storagev1.Entry{
		"no metadata":    entry("book.m4b", nil),
		"no codec":       entry("book.m4b", &storagev1.AudioFileMetadata{DurationSeconds: 10}),
		"under a second": entry("book.m4b", &storagev1.AudioFileMetadata{DurationSeconds: 0.5, Codec: "aac"}),
		"no demuxer":     entry("book.aiff", &storagev1.AudioFileMetadata{DurationSeconds: 10, Codec: "pcm_s16be"}),
	} {
		if _, ok := StorageAudioTrackFromEntry(e); ok {
			t.Errorf("%s: built a track", name)
		}
	}
}

func TestStagedAudioProbeKeepsATrackWhoseChaptersOutgrowIt(t *testing.T) {
	probe := &NativeAudioProbe{Probe: &ProbeData{Container: "mp3", CodecAudio: "mp3"}, DurationSeconds: 270000}
	for i := range 1194 {
		probe.Chapters = append(probe.Chapters, ChapterInfo{Index: i, Title: "Chapter " + strconv.Itoa(i), StartSeconds: float64(i), EndSeconds: float64(i + 1)})
	}
	var staged nativeAudioStagedProbe
	if err := json.Unmarshal(stagedAudioProbe(probe), &staged); err != nil || len(staged.Chapters) != 1194 {
		t.Fatalf("a Bible's chapters were not staged: %d %v", len(staged.Chapters), err)
	}
	for i := range probe.Chapters {
		probe.Chapters[i].Title = strings.Repeat("t", 1024)
	}
	encoded := stagedAudioProbe(probe)
	if encoded == nil {
		t.Fatal("track rejected for its chapters")
	}
	staged = nativeAudioStagedProbe{}
	if err := json.Unmarshal(encoded, &staged); err != nil || len(staged.Chapters) != 0 || staged.DurationSeconds != 270000 {
		t.Fatalf("oversized chapters kept or timeline lost: %d %v", len(staged.Chapters), err)
	}
}
