package ai

// Bloem-owned. AI subtitle cues for Chinese and Japanese, and a short hold
// after speech. Whisper-compatible servers time Chinese and Japanese per
// character or per word, so Silo's cue builder would join them with spaces and
// wrap them at 42 narrow characters. Bloem joins such words without a space,
// counts width in columns (a wide character takes two), wraps those lines at
// 16 wide characters, splits a full cue after a clause mark, and keeps every
// cue on screen briefly after its speech ends.
//
// transcriber.go reaches this file through hook lines: cuesFromWords and
// wrapCueText hand their work to bloemCuesFromWords and bloemWrapCueText, and
// both chunk loops call holdChunkCueEnds after transcribeChunk.

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Silo-Server/silo-server/internal/ai/llm"
)

const (
	// cueMaxLineLengthWide is the wrap width in columns of a line of Chinese or
	// Japanese text: 16 wide characters of two columns each.
	cueMaxLineLengthWide = 32
	// cueHold keeps a cue on screen briefly after its speech ends, as human
	// subtitles do, up to the next cue or the end of the audio chunk.
	cueHold = 600 * time.Millisecond
)

// holdChunkCueEnds applies holdCueEnds to one transcribed chunk's cues, which
// start at chunkStart seconds and may last chunkSeconds.
func holdChunkCueEnds(cues []SubtitleCue, chunkStart float64, chunkSeconds int) {
	holdCueEnds(cues, time.Duration((chunkStart+float64(chunkSeconds))*float64(time.Second)))
}

// cueWord is one word of the cue being built; spaced words follow the
// previous word after a space.
type cueWord struct {
	text       string
	start, end float64
	spaced     bool
}

// bloemCuesFromWords replaces Silo's cuesFromWords. It groups word timings
// into cues: a cue closes at a speech pause, when the text would exceed the
// wrap capacity, when it would exceed the maximum duration, or after a
// sentence ends once a full line is accumulated. Cue end times come from the
// last word, so cues disappear shortly after speech stops (holdCueEnds)
// instead of lingering through silence. Chinese and Japanese words follow
// each other without a space, and a cue of such text too full for the next
// word closes after its last clause mark rather than inside a word, when at
// least a line comes before the mark. It always handles the words.
func bloemCuesFromWords(words []llm.TranscriptionWord, offsetSeconds float64) ([]SubtitleCue, bool) {
	var out []SubtitleCue
	var cue []cueWord
	width := 0
	emit := func(ws []cueWord) {
		if len(ws) > 0 {
			out = append(out, newCue(offsetSeconds+ws[0].start, offsetSeconds+ws[len(ws)-1].end, joinCueWords(ws)))
		}
	}
	// restart begins the next cue with the words a clause split carried over.
	restart := func(carried []cueWord) {
		cue = append([]cueWord(nil), carried...)
		width = 0
		for i := range cue {
			cue[i].spaced = cue[i].spaced && i > 0
			width += textWidth(cue[i].text)
			if cue[i].spaced {
				width++
			}
		}
	}
	prev := ""
	for _, w := range words {
		word := strings.TrimSpace(w.Text)
		if word == "" {
			continue
		}
		spaced := !w.JoinsPrevious && !joinsWithoutSpace(prev, word)
		prev = word
		added := textWidth(word)
		if spaced {
			added++
		}
		if len(cue) > 0 {
			switch {
			case w.Start-cue[len(cue)-1].end >= cueSplitPauseSeconds || w.End-cue[0].start > maxCueSeconds:
				emit(cue)
				restart(nil)
			case width+added > cueLineLength(cue[0].text)*cueMaxLines:
				keep := len(cue)
				if i := lastClauseEnd(cue); !spaced && i > 0 &&
					textWidth(joinCueWords(cue[:i])) >= cueLineLength(cue[0].text) {
					keep = i
				}
				emit(cue[:keep])
				restart(cue[keep:])
			}
		}
		if len(cue) == 0 {
			spaced = false
			added = textWidth(word)
		}
		cue = append(cue, cueWord{text: word, start: w.Start, end: w.End, spaced: spaced})
		width += added
		if width >= cueLineLength(cue[0].text) && bloemEndsSentence(word) {
			emit(cue)
			restart(nil)
		}
	}
	emit(cue)
	return out, true
}

func joinCueWords(words []cueWord) string {
	var text strings.Builder
	for i, w := range words {
		if i > 0 && w.spaced {
			text.WriteByte(' ')
		}
		text.WriteString(w.text)
	}
	return text.String()
}

// joinsWithoutSpace reports whether next follows prev without a space: both
// sides of the boundary are Chinese or Japanese, which Whisper servers time
// as separate words.
func joinsWithoutSpace(prev, next string) bool {
	if prev == "" {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(prev)
	first, _ := utf8.DecodeRuneInString(next)
	return unspacedScript(last) && unspacedScript(first)
}

// unspacedScript reports whether r belongs to text written without spaces
// between words: Chinese characters, kana, and their punctuation and
// full-width forms.
func unspacedScript(r rune) bool {
	return unicode.Is(unicode.Han, r) || (r >= 0x3000 && r <= 0x30FF) || (r >= 0xFF01 && r <= 0xFF60)
}

// cueLineLength is the wrap width in columns for a cue that begins with text.
func cueLineLength(text string) int {
	if first, _ := utf8.DecodeRuneInString(text); unspacedScript(first) {
		return cueMaxLineLengthWide
	}
	return cueMaxLineLength
}

// clauseMarks close a clause in unspaced text; a full cue splits after the
// last one.
const clauseMarks = "、。，．！？…,.!?"

// lastClauseEnd is the number of leading words up to and including the last
// one that ends a clause, or 0 when none does.
func lastClauseEnd(words []cueWord) int {
	for i := len(words) - 1; i >= 0; i-- {
		r, _ := utf8.DecodeLastRuneInString(strings.TrimRight(words[i].text, `"')]”’」』）`))
		if strings.ContainsRune(clauseMarks, r) {
			return i + 1
		}
	}
	return 0
}

// bloemEndsSentence reports whether a word closes a sentence, tolerating a
// trailing quote or bracket after the punctuation, including Chinese and
// Japanese sentence marks and brackets.
func bloemEndsSentence(word string) bool {
	word = strings.TrimRight(word, `"')]”’」』）`)
	for _, mark := range []string{".", "?", "!", "…", "。", "？", "！"} {
		if strings.HasSuffix(word, mark) {
			return true
		}
	}
	return false
}

// holdCueEnds extends each cue by cueHold, never into the next cue and, for
// the last cue, never past limit: the end of the audio chunk, where the next
// chunk's cues can begin. A cue already ending later is left as it is. Cues
// are chronological within a chunk.
func holdCueEnds(cues []SubtitleCue, limit time.Duration) {
	for i := range cues {
		bound := limit
		if i+1 < len(cues) {
			bound = cues[i+1].Start
		}
		if end := min(cues[i].End+cueHold, bound); end > cues[i].End {
			cues[i].End = end
		}
	}
}

// bloemWrapCueText replaces Silo's wrapCueText. It greedily wraps text into at
// most maxLines lines of roughly maxLen columns of display width (textWidth),
// so multi-byte scripts like Arabic or Cyrillic wrap at the same visual width
// as Latin and a wide Chinese or Japanese character counts two. Lines break
// between words, and inside unspaced Chinese or Japanese text too wide for a
// line. The last line absorbs any overflow — text is never dropped.
//
// Silo's newCue passes cueMaxLineLength, its default width; for a cue that
// begins with Chinese or Japanese text that default narrows to
// cueMaxLineLengthWide (cueLineLength). It always handles the text.
func bloemWrapCueText(text string, maxLen, maxLines int) ([]string, bool) {
	if maxLen == cueMaxLineLength {
		maxLen = cueLineLength(text)
	}
	pieces := wrapPieces(text, maxLen)
	if len(pieces) == 0 {
		return nil, true
	}
	lines := []string{pieces[0].text}
	widths := []int{pieces[0].width}
	for _, p := range pieces[1:] {
		last := len(lines) - 1
		space := ""
		if p.spaced {
			space = " "
		}
		if widths[last]+len(space)+p.width <= maxLen || len(lines) >= maxLines {
			lines[last] += space + p.text
			widths[last] += len(space) + p.width
			continue
		}
		lines = append(lines, p.text)
		widths = append(widths, p.width)
	}
	return lines, true
}

// wrapPiece is a run of text a line may break before; spaced runs follow the
// previous one after a space.
type wrapPiece struct {
	text   string
	width  int
	spaced bool
}

// wrapPieces splits text at spaces, and a word with wide characters that is
// wider than a line into its characters, each mark in noLineStart kept with
// the character before it.
func wrapPieces(text string, maxLen int) []wrapPiece {
	var out []wrapPiece
	for _, word := range strings.Fields(text) {
		width := textWidth(word)
		if width <= maxLen || width == utf8.RuneCountInString(word) {
			out = append(out, wrapPiece{text: word, width: width, spaced: true})
			continue
		}
		for i, r := range []rune(word) {
			if i > 0 && strings.ContainsRune(noLineStart, r) {
				out[len(out)-1].text += string(r)
				out[len(out)-1].width += runeWidth(r)
				continue
			}
			out = append(out, wrapPiece{text: string(r), width: runeWidth(r), spaced: i == 0})
		}
	}
	return out
}

// noLineStart lists the marks Chinese and Japanese typesetting keeps off the
// start of a line: closing punctuation and brackets, small kana and the
// long-vowel mark.
const noLineStart = "、。，．・：；？！ー々」』）〕］｝〉》】ぁぃぅぇぉっゃゅょゎァィゥェォッャュョヮ…"

// textWidth is text's display width in columns (runeWidth).
func textWidth(text string) int {
	n := 0
	for _, r := range text {
		n += runeWidth(r)
	}
	return n
}

// runeWidth is a rune's display width in columns: two for the wide characters
// of Chinese, Japanese and Korean text, one otherwise.
func runeWidth(r rune) int {
	switch {
	case unicode.Is(unicode.Han, r), unicode.Is(unicode.Hangul, r):
		return 2
	case r >= 0x3000 && r <= 0x30FF: // CJK punctuation, hiragana, katakana
		return 2
	case r >= 0xFF01 && r <= 0xFF60, r >= 0xFFE0 && r <= 0xFFE6: // full-width forms
		return 2
	}
	return 1
}
