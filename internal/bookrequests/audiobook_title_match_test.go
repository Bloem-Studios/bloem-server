package bookrequests

import (
	"slices"
	"strings"
	"testing"
)

func TestNormalizeBookText(t *testing.T) {
	for in, want := range map[string]string{
		"Project Hail Mary":   "project hail mary",
		"  The   Martian ":    "the martian",
		"Shōgun":              "shogun",
		"Les Misérables":      "les miserables",
		"Ender's Game":        "enders game",
		"Ender’s Game":        "enders game",
		"Pride & Prejudice":   "pride and prejudice",
		"Øresund. Straße!":    "oresund strasse",
		"Catch-22":            "catch 22",
		"Dr. Jekyll/Mr. Hyde": "dr jekyll mr hyde",
		"ÆSIR SAGA":           "aesir saga",
		"!!!":                 "",
		"Jírí Kratochvíl":  "jiri kratochvil",
	} {
		if got := normalizeBookText(in); got != want {
			t.Errorf("normalizeBookText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBookTitleForms(t *testing.T) {
	for in, want := range map[string][]string{
		"Project Hail Mary":                {"project hail mary"},
		"Project Hail Mary: A Novel":       {"project hail mary a novel", "project hail mary"},
		"Dune - Deluxe Edition":            {"dune deluxe edition", "dune"},
		"Dune — Deluxe Edition":            {"dune deluxe edition", "dune"},
		"Catch-22":                         {"catch 22"},
		": Leading colon":                  {"leading colon"},
		"Title:":                           {"title"},
		"Sapiens: A Brief History: Part 1": {"sapiens a brief history part 1", "sapiens"},
		"":                                 nil,
	} {
		if got := bookTitleForms(in); !slices.Equal(got, want) {
			t.Errorf("bookTitleForms(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBookAuthorKeys(t *testing.T) {
	for in, want := range map[string][]string{
		"Andy Weir":                         {"andy weir"},
		"Terry Pratchett & Neil Gaiman":     {"terry pratchett and neil gaiman", "terry pratchett", "neil gaiman"},
		"Terry Pratchett, Neil Gaiman":      {"terry pratchett neil gaiman", "terry pratchett", "neil gaiman"},
		"Douglas Preston and Lincoln Child": {"douglas preston and lincoln child", "douglas preston", "lincoln child"},
		"Gabriel García Márquez":            {"gabriel garcia marquez"},
		"":                                  nil,
		" ; ":                               nil,
	} {
		if got := bookAuthorKeys(in); !slices.Equal(got, want) {
			t.Errorf("bookAuthorKeys(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAudiobookTitleAuthorMatch(t *testing.T) {
	for _, tc := range []struct {
		label                   string
		title, author, itemName string
		itemAuthors             []string
		want                    bool
	}{
		{"exact", "Project Hail Mary", "Andy Weir", "Project Hail Mary", []string{"Andy Weir"}, true},
		{"case and punctuation", "project hail mary!", "andy weir.", "PROJECT HAIL MARY", []string{"ANDY WEIR"}, true},
		{"accents", "Les Miserables", "Victor Hugo", "Les Misérables", []string{"Víctor Hugo"}, true},
		{"request subtitle", "Project Hail Mary: A Novel", "Andy Weir", "Project Hail Mary", []string{"Andy Weir"}, true},
		{"item subtitle", "Project Hail Mary", "Andy Weir", "Project Hail Mary: A Novel", []string{"Andy Weir"}, true},
		{"both subtitles", "Dune: Book One", "Frank Herbert", "Dune - Deluxe Edition", []string{"Frank Herbert"}, true},
		{"any credited author", "Good Omens", "Neil Gaiman", "Good Omens", []string{"Terry Pratchett", "Neil Gaiman"}, true},
		{"any requested author", "Good Omens", "Terry Pratchett & Neil Gaiman", "Good Omens", []string{"Neil Gaiman"}, true},
		{"wrong author", "Project Hail Mary", "Andy Weir", "Project Hail Mary", []string{"Someone Else"}, false},
		{"no item author", "Project Hail Mary", "Andy Weir", "Project Hail Mary", nil, false},
		{"no request author", "Project Hail Mary", "", "Project Hail Mary", []string{"Andy Weir"}, false},
		{"different title", "Artemis", "Andy Weir", "The Martian", []string{"Andy Weir"}, false},
		{"title prefix is not a subtitle", "Dune", "Frank Herbert", "Dune Messiah", []string{"Frank Herbert"}, false},
		{"partial author name", "Project Hail Mary", "Weir", "Project Hail Mary", []string{"Andy Weir"}, false},
		{"empty title", "", "Andy Weir", "", []string{"Andy Weir"}, false},
		{"punctuation-only title", "!!!", "Andy Weir", "???", []string{"Andy Weir"}, false},
	} {
		if got := audiobookTitleAuthorMatch(tc.title, tc.author, tc.itemName, tc.itemAuthors); got != tc.want {
			t.Errorf("%s: match = %v, want %v", tc.label, got, tc.want)
		}
	}
}

// The SQL prefilter keeps only titles whose folded, lowercased text without
// apostrophes contains the token, so the token must survive in every title
// the matcher accepts.
func TestAudiobookTitlePrefilterToken(t *testing.T) {
	for _, tc := range []struct{ request, item, want string }{
		{"Project Hail Mary: A Novel", "Project Hail Mary", "project"},
		{"Project Hail Mary: A Novel", "PROJECT HAIL MARY - Unabridged", "project"},
		{"Ender's Game", "Ender’s Game", "enders"},
		{"Enders Game", "Ender's Game", "enders"},
		{"A & B", "A and B", ""},
		{"Les Misérables", "Les Miserables", "miserables"},
		{"!!!", "", ""},
	} {
		token := audiobookTitlePrefilterToken(tc.request)
		if token != tc.want {
			t.Errorf("token(%q) = %q, want %q", tc.request, token, tc.want)
		}
		if audiobookTitleAuthorMatch(tc.request, "Author", tc.item, []string{"Author"}) && !strings.Contains(bookPrefilterText(tc.item), token) {
			t.Errorf("token %q filters out matching title %q", token, tc.item)
		}
	}
}
