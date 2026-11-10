"""Pictures for docs/architecture/bloem-search-and-embeddings.md."""
from kit import INK, MUTED, TIERS, Svg, box, footer, para


def two_halves():
    s = Svg(1200, 500, "How one search is answered",
            "A made-up search, 'man finds a message in a bottle', goes two ways. The words half matches its "
            "words in titles, people, genres, keywords and plots, dropping the most common words first when no "
            "title has every word. The meaning half turns the search into a vector labelled as a search and "
            "compares it with each title's vector, labelled as catalog text. Bloem mixes the two lists, half "
            "from each by default. The meaning half waits until 85% of each kind of title has a vector from the "
            "current model, and a search Meilisearch has not answered in time goes to the built-in engine.")
    s.header("How one search is answered",
             "Two halves look for titles, and Bloem mixes what they find.")
    g, o, a, pr = TIERS["grey"], TIERS["org"], TIERS["account"], TIERS["profile"]

    box(s, 40, 186, 230, 110, g, "The search",
        "“man finds a message in a bottle”", size=15)
    box(s, 330, 124, 500, 124, o, "1. Words",
        "Finds titles whose title, people, genres, keywords or plot contain the search's words. "
        "When no title has every word, it drops the most common words first.")
    box(s, 330, 272, 500, 124, pr, "2. Meaning",
        "The search becomes a list of numbers, a vector, labelled as a search. Bloem looks for titles "
        "whose own vector, labelled as catalog text, lies close to it.")
    box(s, 890, 186, 270, 110, a, "Mixed results",
        "Half from each list by default. The share is a setting.")

    s.arrow(273, 226, 327, 190)
    s.arrow(273, 256, 327, 330)
    s.arrow(833, 190, 887, 226)
    s.arrow(833, 330, 887, 256)

    footer(s, "The meaning half waits until 85% of each kind of title has a vector from the current model. "
              "A search Meilisearch has not answered in time goes to the built-in engine.", y=466)
    return s


def chips(s, x, y, words, kept, width, kept_color):
    """Lay words out as pills from x, wrapping at width; kept words in colour, dropped ones grey."""
    grey = {"fill": "#F9FAFB", "stroke": "#D1D5DB", "label": "#9CA3AF"}
    cx, cy = x, y
    for i, word in enumerate(words):
        w = round(len(word) * 15 * 0.58 + 22)
        if cx + w > x + width:
            cx, cy = x, cy + 40
        colour = kept_color if i in kept else grey
        s.badge(cx, cy, word, colour, size=15)
        if i not in kept:
            mid = cy + 14
            s.add(f'<path d="M{cx + 8},{mid} L{cx + w - 8},{mid}" stroke="#9CA3AF" stroke-width="1.6"/>')
        cx += w + 10
    return cy + 40


def dropping_words():
    s = Svg(1200, 470, "What happens when no title has every word",
            "This example is made up. The search 'man finds a message in a bottle on the beach' matches no "
            "title with all ten words. Dropping trailing words keeps 'man finds a', which thousands of titles "
            "contain. Dropping the most common words first keeps 'message bottle beach', the words that pick "
            "out the story. Two-word searches still need both words. On 200 searches that describe a title "
            "without naming it, the right title was in the top 50 for 16.5% of searches when trailing words "
            "were dropped and 36.5% when the most common words were dropped first.")
    s.header("What happens when no title has every word",
             "This example is made up: one search, matched two ways.")
    words = ["man", "finds", "a", "message", "in", "a", "bottle", "on", "the", "beach"]
    w, st = TIERS["warn"], TIERS["plan"]

    for x, colour, title, kept, note in [
        (40, w, "Drop trailing words", {0, 1, 2},
         "Keeps “man finds a”. Thousands of titles have those words, so the story it describes "
         "is lost among them."),
        (620, st, "Drop the most common words first", {3, 6, 9},
         "Keeps “message bottle beach”, the rarer words that pick out the story."),
    ]:
        s.rect(x, 124, 540, 226, colour["fill"], colour["stroke"], rx=14)
        s.text(x + 22, 162, title, size=19, weight="700", fill=INK)
        y = chips(s, x + 22, 186, words, kept, 500, colour)
        para(s, x + 22, y + 24, note, 496, size=15, fill=MUTED)

    footer(s, "Two-word searches still need both words, so a typing mistake such as “spnge bob” never "
              "shrinks to one loose word. Measured on 200 searches that describe a title: in the top 50 "
              "16.5% of the time when trailing words are dropped, 36.5% when common words go first.", y=436)
    return s


PICTURES = [("two-halves", two_halves), ("dropping-words", dropping_words)]
