package ai

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Silo-Server/silo-server/internal/ai/llm"
)

// Bloem: cue building for speech-to-text servers that time Chinese and
// Japanese per character or per word.

// joinedSeq times each character of text as its own word that follows the
// previous one without a space, as NeMo-Speech.cpp reports Japanese.
func joinedSeq(startAt, dur float64, text string) []llm.TranscriptionWord {
	var out []llm.TranscriptionWord
	at := startAt
	for i, r := range []rune(text) {
		out = append(out, llm.TranscriptionWord{Start: at, End: at + dur, Text: string(r), JoinsPrevious: i > 0})
		at += dur
	}
	return out
}

func cueText(cues []SubtitleCue) string {
	var b strings.Builder
	for _, c := range cues {
		b.WriteString(strings.Join(c.Lines, ""))
	}
	return b.String()
}

func TestCuesFromWordsJoinsUnspacedWords(t *testing.T) {
	cues := cuesFromWords(joinedSeq(0, 0.1, "なんかごめんね。"), 0)
	if len(cues) != 1 || fmt.Sprint(cues[0].Lines) != "[なんかごめんね。]" {
		t.Fatalf("cues = %+v, want one unspaced line", cues)
	}
}

func TestCuesFromWordsCountsWideCharactersTwice(t *testing.T) {
	// 60 wide characters fill 120 columns: more than one cue's two 42-column
	// lines, though only 60 runes.
	text := strings.Repeat("あいうえおかきくけこ", 6)
	cues := cuesFromWords(joinedSeq(0, 0.05, text), 0)
	if len(cues) < 2 {
		t.Fatalf("cues = %d, want the text split by display width", len(cues))
	}
	for i, c := range cues {
		for _, line := range c.Lines {
			if n := utf8.RuneCountInString(line); n > cueMaxLineLength/2 {
				t.Errorf("cue %d line %q has %d wide characters, over %d columns", i, line, n, cueMaxLineLength)
			}
		}
	}
	if got := cueText(cues); got != text {
		t.Errorf("cue text = %q, want %q", got, text)
	}
}

func TestCuesFromWordsClosesJapaneseSentences(t *testing.T) {
	// 。 ends a sentence: once a line's worth is shown, the cue closes there.
	first := strings.Repeat("あ", 24) + "。"
	cues := cuesFromWords(joinedSeq(0, 0.05, first+"いいいい"), 0)
	if len(cues) != 2 || strings.Join(cues[0].Lines, "") != first {
		t.Fatalf("cues = %+v, want the first to end at 。", cues)
	}
}

func TestCuesFromWordsJoinsWhisperChineseAndJapaneseWords(t *testing.T) {
	// Whisper servers send Japanese words without joins_previous; a boundary
	// between two Chinese or Japanese words still takes no space, while
	// Korean and a kanji word in English text keep theirs.
	cases := []struct {
		words []string
		want  string
	}{
		{[]string{" なんか", "ごめん", "ね。"}, "なんかごめんね。"},
		{[]string{" 我们", "走吧"}, "我们走吧"},
		{[]string{" 안녕하세요", "여러분"}, "안녕하세요 여러분"},
		{[]string{" I", "love", "東京"}, "I love 東京"},
	}
	for _, c := range cases {
		cues := cuesFromWords(wordSeq(0, 0.3, 0.05, c.words...), 0)
		if len(cues) != 1 || strings.Join(cues[0].Lines, " ") != c.want {
			t.Errorf("cues for %q = %+v, want %q", c.words, cues, c.want)
		}
	}
}

func TestCuesFromWordsShortensChineseAndJapaneseLines(t *testing.T) {
	// Subtitle lines of wide characters hold 16 of them, not 21.
	text := strings.Repeat("あいうえおかきくけこ", 3)
	cues := cuesFromWords(joinedSeq(0, 0.05, text), 0)
	if got := cueText(cues); got != text {
		t.Fatalf("cue text = %q, want %q", got, text)
	}
	for i, c := range cues {
		for _, line := range c.Lines {
			if n := utf8.RuneCountInString(line); n > 16 {
				t.Errorf("cue %d line %q has %d wide characters, over 16", i, line, n)
			}
		}
	}
}

func TestCuesFromWordsSplitsUnspacedTextAtClauses(t *testing.T) {
	// A cue too full for the next character closes after the last clause
	// mark instead of in the middle of a word.
	first := strings.Repeat("あ", 20) + "、"
	cues := cuesFromWords(joinedSeq(0, 0.05, first+strings.Repeat("い", 20)), 0)
	if len(cues) != 2 || strings.Join(cues[0].Lines, "") != first {
		t.Fatalf("cues = %+v, want the first to end at 、", cues)
	}
	if cues[1].Start != time.Duration(21*0.05*float64(time.Second)) {
		t.Errorf("second cue start = %v, want the first carried character's start", cues[1].Start)
	}

	// A clause mark that would leave less than a line before it is no place
	// to split.
	early := cuesFromWords(joinedSeq(0, 0.05, "あ、"+strings.Repeat("い", 40)), 0)
	if len(early) < 2 || strings.Join(early[0].Lines, "") == "あ、" {
		t.Errorf("cues = %+v, want no split right after an early 、", early)
	}
}

func TestWrapCueTextBreaksUnspacedText(t *testing.T) {
	// 30 wide characters need two lines, broken inside the unspaced text, and
	// a closing mark never starts a line.
	text := strings.Repeat("あ", 21) + "。" + strings.Repeat("い", 8)
	lines := wrapCueText(text, 42, 2)
	if len(lines) != 2 || strings.Join(lines, "") != text {
		t.Fatalf("lines = %q, want %q in two lines", lines, text)
	}
	for _, line := range lines {
		if n := utf8.RuneCountInString(line); n > 21 {
			t.Errorf("line %q has %d wide characters, over 42 columns", line, n)
		}
	}
	if strings.HasPrefix(lines[1], "。") {
		t.Errorf("second line %q starts with a closing mark", lines[1])
	}
}
