"""Pictures for docs/architecture/bloem-requests.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, footer, para

BOOK = {"fill": "#FFF7ED", "stroke": "#EA580C", "label": "#9A3412"}


def three_kinds():
    s = Svg(1200, 700, "Three kinds of request",
            "Three columns: movies and series, handled by Silo's request service; audiobooks, Bloem's own lane; and "
            "ebooks, Bloem's own lane that is off by default. Each lists who can ask, what they give, where the "
            "title comes from and when the request is done.")
    s.header("Three kinds of request",
             "Viewers ask for something the library does not have yet. Each kind has its own queue and its own count.")
    o, p, g = TIERS["org"], TIERS["plan"], TIERS["grey"]
    cols = [
        (p, "Movies and series", "Silo's request service, with Bloem's organization and profile checks added.", [
            ("Who can ask", "Any profile whose plan allows requests."),
            ("They give", "A title, found on TMDB. For a series, which seasons."),
            ("It comes from", "A connected service such as Radarr or Sonarr, chosen by routing rules."),
            ("Done when", "The title arrives in a library."),
        ]),
        (BOOK, "Audiobooks", "Bloem's own queue beside Silo's.", [
            ("Who can ask", "Profiles in any organization, when its plan allows requests."),
            ("They give", "An ISBN or ASIN, or a title and author."),
            ("It comes from", "An audiobook service, if one is connected. Otherwise an administrator adds it."),
            ("Done when", "A book with the same ISBN or ASIN appears in the organization's catalog."),
        ]),
        (BOOK, "Ebooks", "Bloem's own queue. Off until a platform administrator switches it on.", [
            ("Who can ask", "Profiles in the default organization only."),
            ("They give", "An ISBN. Nothing else is accepted."),
            ("It comes from", "Bookwarehouse, into a library linked to it."),
            ("Done when", "An EPUB or PDF with that ISBN appears in that library."),
        ]),
    ]
    x, w = 40, 360
    for color, title, sub, rows in cols:
        s.rect(x, 124, w, 500, color["fill"], color["stroke"], rx=16, sw=1.8)
        s.text(x + 20, 160, title, size=20, weight="700")
        para(s, x + 20, 186, sub, w - 40, size=14, gap=19)
        y = 238
        for label, val in rows:
            s.rect(x + 16, y, w - 32, 86, "#FFFFFF", "#E5E7EB", rx=10)
            s.text(x + 32, y + 26, label.upper(), size=11.5, weight="700", fill=color["label"], spacing="1")
            para(s, x + 32, y + 50, val, w - 64, size=14, fill=INK, gap=19)
            y += 94
        x += w + 20
    footer(s, "Each kind is counted separately against the request limits. Requests from all of an "
              "account's profiles share one count.", y=666)
    return s


def journey():
    s = Svg(1200, 640, "An audiobook request, start to finish",
            "Five steps: a profile asks, the request is approved or declined, the book is fetched, the library is "
            "scanned and Bloem matches the book by its identifier, then the requester is told. Notes explain "
            "counting, privacy and duplicates.")
    s.header("An audiobook request, start to finish",
             "Movie, series and ebook requests follow the same shape.")
    o, a, w, st, g = TIERS["org"], TIERS["account"], TIERS["warn"], TIERS["stop"], TIERS["grey"]
    steps = [
        (BOOK, "Ask", "Sam's profile asks for a book by its ISBN. It counts against the account's limit."),
        (w, "Approve", "Automatically, or by an administrator of Sam's organization. A decline gives the count "
         "back."),
        (o, "Fetch", "A connected audiobook service downloads it. Without one, an administrator adds it by hand."),
        (g, "Scan and match", "Every five minutes Bloem looks for a book with the same ISBN or ASIN in the "
         "catalog. A similar title is never enough."),
        (a, "Done", "Sam gets a notice, and the book is in the library."),
    ]
    x0, y0, cw, ch, gap = 40, 128, 204, 236, 25
    for i, (c, t, b) in enumerate(steps):
        x = x0 + i * (cw + gap)
        s.rect(x, y0, cw, ch, c["fill"], c["stroke"], rx=14)
        s.number(x + 26, y0 + 28, i + 1, c["stroke"])
        s.text(x + 16, y0 + 74, t, size=16.5, weight="700")
        para(s, x + 16, y0 + 102, b, cw - 30, size=13.5, gap=18)
        if i < len(steps) - 1:
            s.arrow(x + cw + 3, y0 + ch / 2, x + cw + gap - 3, y0 + ch / 2)
    s.text(40, 424, "Good to know", size=17, weight="700")
    notes = [
        "Withdrawing a request still counts against the limit; a decline does not.",
        "Profiles on one account do not see each other's requests. Organization administrators see their own "
        "organization's queue, never another's.",
        "Asking twice for the same book in one organization is refused as a duplicate, without showing who asked "
        "first.",
        "An administrator can mark a request fulfilled by hand. That records the decision; it does not claim the "
        "book is playable.",
    ]
    y = 444
    for n in notes:
        s.add(f'<circle cx="52" cy="{y + 14}" r="4" fill="{SOFT}"/>')
        para(s, 66, y + 19, n, 1080, size=14.5, fill=INK)
        y += 40
    return s


PICTURES = [("three-kinds", three_kinds), ("journey", journey)]
