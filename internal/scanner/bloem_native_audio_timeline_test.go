package scanner

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestNormalizeNativeAudioTracksOrdersDiscsAndGlobalChapters(t *testing.T) {
	tracks := []NativeAudioTrack{
		{EntryID: "second", Disc: 2, Track: 1, Duration: 90, Chapters: []NativeAudioChapter{
			{ID: "end", Title: "End", Start: 80, End: 90},
			{ID: "opening", Title: "Opening", Start: 5, End: 80},
		}},
		{EntryID: "first", Disc: 1, Track: 2, Duration: 60, Chapters: []NativeAudioChapter{
			{ID: "opening", Title: "Opening", Start: 0, End: 60},
		}},
	}
	want := []NativeAudioSpan{
		{EntryID: "first", Start: 0, Duration: 60, Chapters: []NativeAudioChapter{
			{ID: "opening", Title: "Opening", Start: 0, End: 60},
		}},
		{EntryID: "second", Start: 60, Duration: 90, Chapters: []NativeAudioChapter{
			{ID: "opening", Title: "Opening", Start: 65, End: 140},
			{ID: "end", Title: "End", Start: 140, End: 150},
		}},
	}
	got, err := NormalizeNativeAudioTracks(tracks)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("timeline = %#v, %v; want %#v", got, err, want)
	}
	if tracks[0].EntryID != "second" || tracks[0].Chapters[0].Start != 80 || tracks[0].Chapters[1].Start != 5 {
		t.Fatalf("normalization mutated its input: %#v", tracks)
	}
	got[1].Chapters[0].Title = "Changed"
	if tracks[0].Chapters[1].Title != "Opening" {
		t.Fatal("output shares chapter storage with input")
	}
}

func TestNormalizeNativeAudioTracksOrdersWithinDiscAndKeepsBoundaries(t *testing.T) {
	got, err := NormalizeNativeAudioTracks([]NativeAudioTrack{
		{EntryID: "two", Disc: 1, Track: 2, Duration: 0.75, Chapters: []NativeAudioChapter{{ID: "chapter", Start: 0, End: 0.75}}},
		{EntryID: "one", Disc: 1, Track: 1, Duration: 0.25, Chapters: []NativeAudioChapter{{ID: "chapter", Start: 0, End: 0.25}}},
	})
	if err != nil || len(got) != 2 || got[0].EntryID != "one" || got[1].Start != 0.25 || got[1].Chapters[0].Start != 0.25 || got[1].Chapters[0].End != 1 {
		t.Fatalf("fractional/boundary timeline = %#v, %v", got, err)
	}
}

func TestNormalizeNativeAudioTracksRejectsChapterPrecisionLoss(t *testing.T) {
	cases := []struct {
		name       string
		start, end float64
	}{
		{"positive interval collapses at track start", 0.25, 0.75},
		{"positive interval collapses after track start", 2, 2.75},
		{"positive start lost while interval survives", 0.25, 2},
		{"zero length chapter position lost", 0.25, 0.25},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeNativeAudioTracks([]NativeAudioTrack{
				{EntryID: "one", Disc: 1, Track: 1, Duration: 1e16},
				{EntryID: "two", Disc: 1, Track: 2, Duration: 10, Chapters: []NativeAudioChapter{
					{ID: "chapter", Start: tc.start, End: tc.end},
				}},
			})
			if got != nil || !errors.Is(err, ErrNativeAudioTimeline) {
				t.Fatalf("chapter precision loss accepted: %#v, %v", got, err)
			}
		})
	}
}

func TestNormalizeNativeAudioTracksKeepsRepresentableZeroLengthChapters(t *testing.T) {
	got, err := NormalizeNativeAudioTracks([]NativeAudioTrack{
		{EntryID: "one", Disc: 1, Track: 1, Duration: 1e16},
		{EntryID: "two", Disc: 1, Track: 2, Duration: 10, Chapters: []NativeAudioChapter{
			{ID: "start", Start: 0, End: 0},
			{ID: "middle", Start: 2, End: 2},
			{ID: "end", Start: 10, End: 10},
		}},
	})
	want := []NativeAudioSpan{
		{EntryID: "one", Start: 0, Duration: 1e16},
		{EntryID: "two", Start: 1e16, Duration: 10, Chapters: []NativeAudioChapter{
			{ID: "start", Start: 10000000000000000, End: 10000000000000000},
			{ID: "middle", Start: 10000000000000002, End: 10000000000000002},
			{ID: "end", Start: 10000000000000010, End: 10000000000000010},
		}},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("zero length chapter timeline = %#v, %v; want %#v", got, err, want)
	}
}

func TestNormalizeNativeAudioTracksRejectsIncompleteOrAmbiguousTimeline(t *testing.T) {
	valid := func() []NativeAudioTrack {
		return []NativeAudioTrack{
			{EntryID: "one", Disc: 1, Track: 1, Duration: 60, Chapters: []NativeAudioChapter{{ID: "one", Start: 5, End: 60}}},
			{EntryID: "two", Disc: 1, Track: 2, Duration: 90},
		}
	}
	cases := []struct {
		name string
		edit func([]NativeAudioTrack) []NativeAudioTrack
	}{
		{"empty", func([]NativeAudioTrack) []NativeAudioTrack { return nil }},
		{"missing entry", func(v []NativeAudioTrack) []NativeAudioTrack { v[1].EntryID = ""; return v }},
		{"duplicate entry", func(v []NativeAudioTrack) []NativeAudioTrack { v[1].EntryID = v[0].EntryID; return v }},
		{"missing disc", func(v []NativeAudioTrack) []NativeAudioTrack { v[1].Disc = 0; return v }},
		{"missing track", func(v []NativeAudioTrack) []NativeAudioTrack { v[1].Track = 0; return v }},
		{"duplicate order", func(v []NativeAudioTrack) []NativeAudioTrack { v[1].Track = 1; return v }},
		{"duration absent", func(v []NativeAudioTrack) []NativeAudioTrack { v[1].Duration = 0; return v }},
		{"duration negative", func(v []NativeAudioTrack) []NativeAudioTrack { v[1].Duration = -1; return v }},
		{"duration nan", func(v []NativeAudioTrack) []NativeAudioTrack { v[1].Duration = math.NaN(); return v }},
		{"duration infinite", func(v []NativeAudioTrack) []NativeAudioTrack { v[1].Duration = math.Inf(1); return v }},
		{"duration sum overflow", func(v []NativeAudioTrack) []NativeAudioTrack {
			v[0].Duration = math.MaxFloat64
			v[1].Duration = math.MaxFloat64
			return v
		}},
		{"duration cannot advance", func(v []NativeAudioTrack) []NativeAudioTrack { v[0].Duration = 1e30; v[1].Duration = 1; return v }},
		{"chapter identity absent", func(v []NativeAudioTrack) []NativeAudioTrack { v[0].Chapters[0].ID = ""; return v }},
		{"chapter identity repeated", func(v []NativeAudioTrack) []NativeAudioTrack {
			v[0].Chapters = append(v[0].Chapters, v[0].Chapters[0])
			return v
		}},
		{"chapter start nan", func(v []NativeAudioTrack) []NativeAudioTrack { v[0].Chapters[0].Start = math.NaN(); return v }},
		{"chapter end infinite", func(v []NativeAudioTrack) []NativeAudioTrack { v[0].Chapters[0].End = math.Inf(1); return v }},
		{"chapter negative", func(v []NativeAudioTrack) []NativeAudioTrack { v[0].Chapters[0].Start = -1; return v }},
		{"chapter reversed", func(v []NativeAudioTrack) []NativeAudioTrack {
			v[0].Chapters[0].Start = 30
			v[0].Chapters[0].End = 20
			return v
		}},
		{"chapter beyond track", func(v []NativeAudioTrack) []NativeAudioTrack { v[0].Chapters[0].End = 61; return v }},
		{"chapter count unbounded", func(v []NativeAudioTrack) []NativeAudioTrack {
			v[0].Chapters = make([]NativeAudioChapter, 1025)
			return v
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeNativeAudioTracks(tc.edit(valid()))
			if got != nil || !errors.Is(err, ErrNativeAudioTimeline) {
				t.Fatalf("invalid timeline accepted: %#v, %v", got, err)
			}
		})
	}
}
