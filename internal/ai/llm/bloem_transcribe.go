package llm

import (
	"fmt"
	"strings"
)

// Bloem: NeMo-Speech.cpp (Bloem's speech model for Chinese and Japanese)
// answers verbose_json with a top-level word list and no segments, and marks
// each word's running text (display) and whether it follows the previous word
// without a space (joins_previous). Silo's parser needs segments, so a
// words-only reply is turned into segments here before Silo's check refuses
// it.

// bloemWordsOnlyTranscription builds the transcription for a reply that has
// words but no segments. Speech-free audio gives no segments; text without
// any timed word fails as Silo's parser fails it.
func bloemWordsOnlyTranscription(parsed transcriptionResponse, model string, result **Transcription) error {
	out := &Transcription{Language: parsed.Language}
	out.Segments = segmentsFromWords(wordsFromJSON(parsed.Words))
	if len(out.Segments) == 0 && strings.TrimSpace(parsed.Text) != "" {
		return &permanentError{err: fmt.Errorf("transcription endpoint returned text without timed segments (model %q): %w: %w", model, ErrTimedSegmentsUnsupported, ErrUnusableResponse)}
	}
	*result = out
	return nil
}

// bloemWordExtensions applies a word's NeMo-Speech.cpp fields: its running
// text (display), so punctuation kept out of a character's word survives, and
// whether it joins the previous word.
func bloemWordExtensions(word *TranscriptionWord, raw transcriptionWordJSON) {
	if raw.Display != "" {
		word.Text = raw.Display
	}
	word.JoinsPrevious = raw.JoinsPrevious
}

// segmentsFromWords wraps a segment-less word list in one segment spanning
// it; cue building splits the words at pauses, so no segment boundaries are
// lost. Speech-free audio (no non-empty words) yields no segments.
func segmentsFromWords(words []TranscriptionWord) []TranscriptionSegment {
	var text strings.Builder
	for _, w := range words {
		word := strings.TrimSpace(w.Text)
		if word == "" {
			continue
		}
		if text.Len() > 0 && !w.JoinsPrevious {
			text.WriteByte(' ')
		}
		text.WriteString(word)
	}
	if text.Len() == 0 {
		return nil
	}
	return []TranscriptionSegment{{
		Start: words[0].Start,
		End:   words[len(words)-1].End,
		Text:  text.String(),
		Words: words,
	}}
}
