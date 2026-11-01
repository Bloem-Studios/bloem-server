"""Pictures for docs/bloem/overlays/architecture/v1-scope.md."""
from xml.sax.saxutils import escape

from kit import INK, MUTED, TIERS, Svg, footer, para

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}
MONO = "'SFMono-Regular', Menlo, Consolas, 'Liberation Mono', monospace"


def _mono(s, x, y, text, size=17, fill=INK, anchor="start", weight="700"):
    s.add(f'<text x="{x}" y="{y}" font-family="{MONO}" font-size="{size}" font-weight="{weight}" '
          f'fill="{fill}" text-anchor="{anchor}">{escape(text)}</text>')


def three_apis():
    s = Svg(1200, 650, "Bloem's three APIs, and who calls them",
            "Three columns. Silo's /api/v1 is frozen and Silo will retire it; Bloem keeps a few reviewed additions "
            "there, such as a profile's own sign-in, and adds nothing new. Silo's /api/v2 is Silo's stable API. "
            "/api/bloem/v1 is Bloem's own API for organizations, plans, Live TV and more, with its own version. "
            "Below, who calls what: since November 1, 2026 the web app and the storefront call only /api/v2 and "
            "/api/bloem/v1; the phone apps still call some /api/v1 routes; Silo's own apps use Silo's APIs.")
    s.header("Bloem's three APIs, and who calls them",
             "Silo's two, and Bloem's own. New Bloem features go only in Bloem's own.")
    g, st, a = TIERS["grey"], TIERS["stop"], TIERS["account"]

    cols = [
        (g, "/api/v1", "Silo's frozen API",
         "Silo will retire it. Bloem keeps a few routes it added earlier, such as a profile's own sign-in, "
         "and never drops one of Silo's. Nothing new goes here.", "Being retired"),
        (SILO, "/api/v2", "Silo's stable API",
         "Everything Silo has: libraries, playback, accounts, settings. Bloem's apps use it for all of that.",
         "Silo's"),
        (BLOEM, "/api/bloem/v1", "Bloem's own API",
         "Organizations, plans, the storefront, Live TV, messages and more. Its version is Bloem's; it grows by "
         "adding fields and routes.", "Bloem's"),
    ]
    x = 40
    for c, path, title, body, tag in cols:
        s.rect(x, 120, 360, 200, c["fill"], c["stroke"], rx=16, sw=1.8)
        _mono(s, x + 24, 160, path, size=19)
        s.text(x + 24, 188, title, size=16, weight="700", fill=c["label"])
        para(s, x + 24, 220, body, 312, size=14.5, gap=20)
        s.badge(x + 336, 136, tag, c, size=12.5, anchor="end")
        x += 380

    s.text(40, 364, "Who calls what", size=17, weight="700")
    callers = [
        ("The web app and the storefront", "Only /api/v2 and /api/bloem/v1, since November 1, 2026.", a),
        ("Bloem's phone apps", "/api/v2 and /api/bloem/v1, and still some /api/v1 routes.", TIERS["warn"]),
        ("Silo's own apps", "Silo's APIs, as they do with Silo. None of Bloem's own API.", g),
    ]
    y = 382
    for name, what, c in callers:
        s.rect(40, y, 1120, 58, "#FFFFFF", "#E5E7EB", rx=12)
        s.rect(40, y, 8, 58, c["stroke"], rx=4)
        s.text(66, y + 35, name, size=16, weight="700")
        s.text(400, y + 35, what, size=14.5, fill=INK)
        y += 70

    footer(s, "Jellyfin and Audiobookshelf apps use their own entrances on their own ports, not these APIs.",
           y=616, fill=MUTED)
    return s


PICTURES = [("three-apis", three_apis)]
