package scanner

import (
	"errors"
	"fmt"
	"math"
	"slices"
)

// ErrNativeAudioTimeline prevents an incomplete or ambiguous native recording
// from replacing a published audiobook presentation.
var ErrNativeAudioTimeline = errors.New("invalid native audiobook timeline")

// NativeAudioTrack is one exact pinned track after host probing and ordering.
// Disc and Track must be resolved by the ingest adapter before normalization;
// absent duration or ordering metadata is not a playable timeline.
type NativeAudioTrack struct {
	EntryID     string
	Disc, Track uint32
	Duration    float64
	Chapters    []NativeAudioChapter
}

// NativeAudioChapter has track-local timing on input and book-global timing in
// a NativeAudioSpan. Its identity is scoped to the containing track.
type NativeAudioChapter struct {
	ID, Title  string
	Start, End float64
}

// NativeAudioSpan retains the track association for global chapter positions.
type NativeAudioSpan struct {
	EntryID         string
	Start, Duration float64
	Chapters        []NativeAudioChapter
}

// NormalizeNativeAudioTracks validates one complete recording and orders it by
// disc/track. It never mutates the caller's tracks or chapters and returns no
// partial result on failure. Alternative presentations are normalized separately.
func NormalizeNativeAudioTracks(tracks []NativeAudioTrack) ([]NativeAudioSpan, error) {
	if len(tracks) == 0 {
		return nil, fmt.Errorf("%w: no tracks", ErrNativeAudioTimeline)
	}
	entries := make(map[string]struct{}, len(tracks))
	positions := make(map[[2]uint32]struct{}, len(tracks))
	for _, track := range tracks {
		if track.EntryID == "" || track.Disc == 0 || track.Track == 0 || !nativeAudioTimelineTime(track.Duration) || track.Duration == 0 {
			return nil, fmt.Errorf("%w: incomplete track", ErrNativeAudioTimeline)
		}
		if _, exists := entries[track.EntryID]; exists {
			return nil, fmt.Errorf("%w: repeated track identity", ErrNativeAudioTimeline)
		}
		entries[track.EntryID] = struct{}{}
		position := [2]uint32{track.Disc, track.Track}
		if _, exists := positions[position]; exists {
			return nil, fmt.Errorf("%w: repeated disc/track position", ErrNativeAudioTimeline)
		}
		positions[position] = struct{}{}
		if len(track.Chapters) > 1024 {
			return nil, fmt.Errorf("%w: too many track chapters", ErrNativeAudioTimeline)
		}
		chapters := make(map[string]struct{}, len(track.Chapters))
		for _, chapter := range track.Chapters {
			if chapter.ID == "" || !nativeAudioTimelineTime(chapter.Start) || !nativeAudioTimelineTime(chapter.End) || chapter.End < chapter.Start || chapter.End > track.Duration {
				return nil, fmt.Errorf("%w: invalid track chapter", ErrNativeAudioTimeline)
			}
			if _, exists := chapters[chapter.ID]; exists {
				return nil, fmt.Errorf("%w: repeated track chapter identity", ErrNativeAudioTimeline)
			}
			chapters[chapter.ID] = struct{}{}
		}
	}
	ordered := slices.Clone(tracks)
	slices.SortFunc(ordered, func(a, b NativeAudioTrack) int {
		if a.Disc != b.Disc {
			if a.Disc < b.Disc {
				return -1
			}
			return 1
		}
		if a.Track < b.Track {
			return -1
		}
		if a.Track > b.Track {
			return 1
		}
		return 0
	})
	spans := make([]NativeAudioSpan, 0, len(ordered))
	var offset float64
	for _, track := range ordered {
		end := offset + track.Duration
		if !nativeAudioTimelineTime(end) || end <= offset {
			return nil, fmt.Errorf("%w: duration exceeds timeline precision", ErrNativeAudioTimeline)
		}
		chapters := slices.Clone(track.Chapters)
		slices.SortStableFunc(chapters, func(a, b NativeAudioChapter) int {
			if a.Start < b.Start {
				return -1
			}
			if a.Start > b.Start {
				return 1
			}
			return 0
		})
		for i := range chapters {
			chapter := chapters[i]
			start, end := offset+chapter.Start, offset+chapter.End
			// The track can advance while smaller chapter offsets or intervals
			// collapse. Preserve intentional zero-length chapters only when
			// their positions survive adding the track offset.
			if (chapter.Start > 0 && start <= offset) || (chapter.End > chapter.Start && end <= start) {
				return nil, fmt.Errorf("%w: chapter exceeds timeline precision", ErrNativeAudioTimeline)
			}
			chapters[i].Start = start
			chapters[i].End = end
		}
		spans = append(spans, NativeAudioSpan{EntryID: track.EntryID, Start: offset, Duration: track.Duration, Chapters: chapters})
		offset = end
	}
	return spans, nil
}

func nativeAudioTimelineTime(seconds float64) bool {
	return !math.IsNaN(seconds) && !math.IsInf(seconds, 0) && seconds >= 0
}
