package bookrequests

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Title and author matching for the native audiobook lane. The storage
// plugin's acquisition route returns no audiobook id, and the bound library
// carries ASINs while requests often carry only an ISBN, so a request there may
// also complete from one new library item with the same title and author.

// bookFoldLetters and foldBookDiacritics mirror public.fold_search_diacritics
// (migration 20261023173138) and catalog's foldSearchDiacritics, so the SQL
// prefilter folds titles exactly as the Go matcher does.
var bookFoldLetters = strings.NewReplacer(
	"Ø", "O", "ø", "o", "Đ", "D", "đ", "d", "Ð", "D", "ð", "d", "Ł", "L", "ł", "l",
	"Ħ", "H", "ħ", "h", "ı", "i", "Ŧ", "T", "ŧ", "t", "Ŀ", "L", "ŀ", "l",
	"ß", "ss", "ẞ", "SS", "æ", "ae", "Æ", "AE", "œ", "oe", "Œ", "OE",
)

func foldBookDiacritics(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	stripped := strings.Map(func(r rune) rune {
		if r >= 0x300 && r <= 0x36f {
			return -1
		}
		return r
	}, norm.NFD.String(s))
	return bookFoldLetters.Replace(norm.NFC.String(stripped))
}

// bookApostrophes are dropped rather than split on, so "Ender's" and
// "Enders" normalize alike. The candidate query removes the same characters.
const bookApostrophes = "'’ʼ‘`"

func isBookTextLetterOrDigit(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// normalizeBookText folds diacritics and case, spells "&" as "and", drops
// apostrophes and turns every other run of non-letters into one space.
func normalizeBookText(s string) string {
	s = strings.ToLower(foldBookDiacritics(s))
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case strings.ContainsRune(bookApostrophes, r):
			continue
		case r == '&':
			b.WriteString(" and")
			space = true
		case isBookTextLetterOrDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// bookSubtitleSeparator ends a main title: a colon, or a dash between spaces.
var bookSubtitleSeparator = regexp.MustCompile(`:|\s[-–—]\s`)

// bookMainTitle returns the title before its first subtitle separator, or ""
// when it has none.
func bookMainTitle(title string) string {
	if loc := bookSubtitleSeparator.FindStringIndex(title); loc != nil {
		return title[:loc[0]]
	}
	return ""
}

// bookTitleForms returns the normalized title and, when it has a subtitle,
// the normalized title without it.
func bookTitleForms(title string) []string {
	full := normalizeBookText(title)
	if full == "" {
		return nil
	}
	forms := []string{full}
	if main := normalizeBookText(bookMainTitle(title)); main != "" && main != full {
		forms = append(forms, main)
	}
	return forms
}

var bookAuthorSeparator = regexp.MustCompile(`[,;&]|\s(?i:and)\s`)

// bookAuthorKeys returns the normalized author string and, when it names
// several people, each of them.
func bookAuthorKeys(author string) []string {
	full := normalizeBookText(author)
	if full == "" {
		return nil
	}
	keys := []string{full}
	for _, part := range bookAuthorSeparator.Split(author, -1) {
		if key := normalizeBookText(part); key != "" && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// audiobookTitleAuthorMatch reports whether a library item is the requested
// audiobook: its title equals the request's, with or without either one's
// subtitle, and one of its credited authors is a requested author. Case,
// accents and punctuation are ignored. An empty title or author never
// matches.
func audiobookTitleAuthorMatch(title, author, itemTitle string, itemAuthors []string) bool {
	forms, keys := bookTitleForms(title), bookAuthorKeys(author)
	if len(forms) == 0 || len(keys) == 0 {
		return false
	}
	titled := false
	for _, form := range bookTitleForms(itemTitle) {
		if slices.Contains(forms, form) {
			titled = true
			break
		}
	}
	if !titled {
		return false
	}
	for _, name := range itemAuthors {
		if n := normalizeBookText(name); n != "" && slices.Contains(keys, n) {
			return true
		}
	}
	return false
}

// bookPrefilterText is what the candidate query compares: the folded,
// lowercased title with apostrophes removed (audiobookTitleCandidatesSQL).
func bookPrefilterText(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(bookApostrophes, r) {
			return -1
		}
		return r
	}, strings.ToLower(foldBookDiacritics(s)))
}

// audiobookTitlePrefilterToken is the longest run of at least three letters
// or digits in the request's main title, as bookPrefilterText spells it.
// Every title the matcher accepts contains it, so the candidate query can
// narrow on it. "" disables the prefilter.
func audiobookTitlePrefilterToken(title string) string {
	main := bookMainTitle(title)
	if normalizeBookText(main) == "" {
		main = title
	}
	best := ""
	for _, run := range strings.FieldsFunc(bookPrefilterText(main), func(r rune) bool { return !isBookTextLetterOrDigit(r) }) {
		if n := utf8.RuneCountInString(run); n >= 3 && n > utf8.RuneCountInString(best) {
			best = run
		}
	}
	return best
}
