package emit

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/Silo-Server/silo-server/cmd/clientdtogen/internal/graph"
)

// Words splits a Go identifier per docs/specs/client-dto-generator.md §4.3
// step 1: on lower→upper boundaries and on letter↔digit boundaries, with a
// run of capitals followed by a capital starting a 2+-letter lowercase word
// read as an initialism ending before the last capital. A single lowercase
// after a capital run is a plural marker and keeps the run whole, so
// BLCompatibilityIDs → [BL Compatibility IDs], not [… I Ds]. Examples:
// HDRDetails → [HDR Details]; HDR10MaxWidth → [HDR 10 Max Width]; DVProfile
// → [DV Profile]; MIMEType → [MIME Type]; ID → [ID].
//
// Inside a run of capitals, a V immediately followed by digits is a version
// marker and starts its own word (StreamHLSV3 → [Stream HLS V 3]), which is
// what lets the enum rule strip a shared V3 suffix. Underscores separate
// words and are dropped. The split is total: every non-empty identifier
// yields at least one word.
func Words(ident string) []string {
	runes := []rune(ident)
	var words []string
	start := 0
	flush := func(end int) {
		if end > start {
			words = append(words, string(runes[start:end]))
		}
		start = end
	}
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		switch {
		case cur == '_':
			flush(i)
			start = i + 1
		case prev == '_':
			continue
		case unicode.IsDigit(cur) != unicode.IsDigit(prev):
			flush(i)
		case unicode.IsUpper(cur) && unicode.IsLower(prev):
			flush(i)
		case unicode.IsUpper(prev) && unicode.IsUpper(cur) && lowerRunLen(runes[i+1:]) >= 2:
			// Initialism ends before the capital that starts the next word;
			// one lowercase alone pluralises the initialism instead (IDs).
			flush(i)
		case unicode.IsUpper(prev) && cur == 'V' && i+1 < len(runes) && unicode.IsDigit(runes[i+1]):
			flush(i)
		}
	}
	flush(len(runes))
	return words
}

// lowerRunLen counts the leading lowercase letters of runes.
func lowerRunLen(runes []rune) int {
	n := 0
	for _, r := range runes {
		if !unicode.IsLower(r) {
			break
		}
		n++
	}
	return n
}

// CamelCase applies §4.3 steps 2–3: lowercase every word, capitalise every
// word but the first, join, and append "Value" when the result is in
// keywords. Digit words join without a separator (HDR10MaxWidth →
// hdr10MaxWidth).
func CamelCase(ident string, keywords map[string]bool) string {
	var b strings.Builder
	for i, w := range Words(ident) {
		w = strings.ToLower(w)
		if i > 0 {
			w = strings.ToUpper(w[:1]) + w[1:]
		}
		b.WriteString(w)
	}
	out := b.String()
	if keywords[out] {
		out += "Value"
	}
	return out
}

// ScreamingCase upper-cases the words and joins them with underscores; a
// digit run attaches to the word before it (HDR10 → HDR10, V3 → V3), so the
// wire-constant identifiers read like their Go source.
func ScreamingCase(words []string) string {
	var b strings.Builder
	for i, w := range words {
		if i > 0 && (len(w) == 0 || !unicode.IsDigit([]rune(w)[0])) {
			b.WriteByte('_')
		}
		b.WriteString(strings.ToUpper(w))
	}
	return b.String()
}

// ConstantNames derives the target-side identifier for each constant of an
// enum type (§4.2): the Go constant name with the shared prefix and suffix
// removed, in SCREAMING_CASE. The prefix is the type name when every constant
// starts with it (Protocol + ProtocolHLS → HLS), otherwise the longest run of
// leading words all constants share (StreamProtocolV3's StreamHLSV3 /
// StreamHTTPProgressiveV3 → HLS / HTTP_PROGRESSIVE after the shared Stream
// prefix and V3 suffix go). Stripping never empties a constant, and a name
// that would start with a digit is prefixed with an underscore. The result is
// in Go source order, parallel to t.Constants; two constants collapsing to
// one identifier is an error naming the type.
func ConstantNames(t *graph.Type) ([]string, error) {
	if len(t.Constants) == 0 {
		return nil, nil
	}
	words := make([][]string, len(t.Constants))
	minLen := -1
	for i, c := range t.Constants {
		words[i] = Words(c.GoName)
		if minLen < 0 || len(words[i]) < minLen {
			minLen = len(words[i])
		}
	}
	// Every constant must keep at least one word.
	budget := minLen - 1

	typeWords := Words(t.Name)
	var prefix int
	if len(typeWords) <= budget && allHavePrefix(words, typeWords) {
		prefix = len(typeWords)
	} else {
		prefix = commonPrefix(words, budget)
	}
	suffix := commonSuffix(words, budget-prefix)

	names := make([]string, len(words))
	seen := make(map[string]int, len(words))
	for i, w := range words {
		name := ScreamingCase(w[prefix : len(w)-suffix])
		if unicode.IsDigit([]rune(name)[0]) {
			name = "_" + name
		}
		if j, dup := seen[name]; dup {
			return nil, fmt.Errorf("enum %s: constants %s and %s both map to %s", t.Key(), t.Constants[j].GoName, t.Constants[i].GoName, name)
		}
		seen[name] = i
		names[i] = name
	}
	return names, nil
}

func allHavePrefix(words [][]string, prefix []string) bool {
	for _, w := range words {
		if len(w) < len(prefix) {
			return false
		}
		for i, p := range prefix {
			if w[i] != p {
				return false
			}
		}
	}
	return true
}

func commonPrefix(words [][]string, budget int) int {
	n := 0
	for n < budget {
		w := words[0][n]
		for _, other := range words[1:] {
			if other[n] != w {
				return n
			}
		}
		n++
	}
	return n
}

func commonSuffix(words [][]string, budget int) int {
	n := 0
	for n < budget {
		w := words[0][len(words[0])-1-n]
		for _, other := range words[1:] {
			if other[len(other)-1-n] != w {
				return n
			}
		}
		n++
	}
	return n
}
