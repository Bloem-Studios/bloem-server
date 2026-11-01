"""Pictures for docs/architecture/bloem-leaving-silo-v1.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}
PART = {"fill": "#FFFBEB", "stroke": "#F59E0B", "label": "#B45309"}


def _part_mark(s, cx, cy, r=12):
    s.add(f'<circle cx="{cx}" cy="{cy}" r="{r}" fill="#FFFFFF" stroke="{PART["stroke"]}" stroke-width="2.5"/>')
    s.add(f'<path d="M{cx - r * 0.45},{cy} L{cx + r * 0.45},{cy}" stroke="{PART["stroke"]}" '
          f'stroke-width="2.5" stroke-linecap="round"/>')


def callers():
    s = Svg(1200, 640, "Who still calls Silo's /api/v1",
            "A grid. Rows are the apps that talk to a Bloem server: the storefront, the web app, Bloem's phone and "
            "TV apps, and Silo's own apps. Columns are Silo's /api/v1, Silo's /api/v2 and Bloem's /api/bloem/v1. "
            "The storefront and the web app left /api/v1 on 31 October and 1 November 2026; Bloem's phone and TV "
            "apps still call part of it.")
    s.header("Who still calls Silo's /api/v1",
             "Silo will retire /api/v1. Each caller moves to Silo's /api/v2 or to Bloem's own API.")
    st = TIERS["stop"]
    cols = [(st, "Silo's /api/v1", "Frozen; to be retired"), (SILO, "Silo's /api/v2", "Silo's stable API"),
            (BLOEM, "Bloem's /api/bloem/v1", "Bloem's own features")]
    col_x, col_w = 330, 276
    for i, (c, title, sub) in enumerate(cols):
        x = col_x + i * col_w
        s.rect(x + 4, 118, col_w - 8, 64, c["fill"], c["stroke"], rx=12, sw=1.6)
        s.text(x + col_w / 2, 146, title, size=16, weight="700", anchor="middle")
        s.text(x + col_w / 2, 168, sub, size=13, fill=MUTED, anchor="middle")

    rows = [
        ("The storefront", "Bloem Garden",
         [("no", "Left on 31 October 2026"), ("yes", "Accounts, libraries, sessions"),
          ("yes", "Organizations, plans, messages")]),
        ("The web app", "In the browser",
         [("no", "Left on 1 November 2026"), ("yes", "Through Silo's own web code"),
          ("yes", "Live TV, campaigns, seasons")]),
        ("Bloem's phone and TV apps", "Android and Apple",
         [("part", "Campaigns, remote control, profile sign-in"), ("yes", "Most screens"),
          ("yes", "Watch, people, server identity")]),
        ("Silo's own apps", "Built for Silo",
         [("part", "At most a health check"), ("yes", "Everything they use"),
          ("no", "They know nothing of it")]),
    ]
    y, h = 198, 88
    for title, sub, cells in rows:
        s.rect(40, y, 280, h - 10, "#FFFFFF", "#E5E7EB", rx=12)
        s.text(60, y + 34, title, size=16.5, weight="700")
        s.text(60, y + 56, sub, size=13.5, fill=MUTED)
        for i, (kind, note) in enumerate(cells):
            x = col_x + i * col_w
            s.rect(x + 4, y, col_w - 8, h - 10, "#FFFFFF", "#E5E7EB", rx=10)
            cx, cy = x + 30, y + (h - 10) / 2
            {"yes": yes_mark, "no": no_mark}.get(kind, _part_mark)(s, cx, cy)
            rows_n = 1 if len(note) * 6.75 < col_w - 70 else 2
            para(s, x + 52, cy + 5 - (rows_n - 1) * 9, note, col_w - 70, size=13.5, gap=18, fill=INK)
        y += h
    footer(s, "When Silo answers /api/v1 with 410 Gone, the routes Bloem still keeps there go with it. Each one "
              "already has a replacement, or gets one when the phone apps move.", y=600, fill=INK)
    return s


def new_feature():
    s = Svg(1200, 560, "Where a new route goes",
            "A decision. If Silo's /api/v2 already has what Bloem needs, Bloem's apps call it. If not, Bloem adds "
            "a typed route under /api/bloem/v1, declared in OpenAPI and detected through a feature token. Nothing "
            "new is ever added to Silo's /api/v1, and no Silo file is changed.")
    s.header("Where a new route goes",
             "Bloem sits on top of Silo: it uses Silo's API where it can and adds its own beside it.")
    a, st = TIERS["account"], TIERS["stop"]

    box(s, 40, 150, 300, 120, TIERS["grey"], "Bloem needs a route",
        "For the storefront, the web app or Bloem's apps.", size=14)
    s.arrow(344, 210, 396, 210)
    s.add(f'<path d="M500,140 L604,210 L500,280 L396,210 Z" fill="{PART["fill"]}" stroke="{PART["stroke"]}" '
          'stroke-width="1.8"/>')
    s.text(500, 204, "Does Silo's", size=14.5, weight="700", anchor="middle")
    s.text(500, 223, "/api/v2 have it?", size=14.5, weight="700", anchor="middle")

    s.arrow(608, 210, 676, 210)
    s.text(642, 200, "Yes", size=13, weight="700", fill=a["label"], anchor="middle")
    box(s, 680, 150, 480, 120, SILO, "Call Silo's /api/v2",
        "With Silo's own rules, such as If-Match on account writes.", size=14)

    s.path("M500,284 L500,330")
    s.text(512, 314, "No", size=13, weight="700", fill=st["label"])
    box(s, 300, 334, 860, 120, BLOEM, "Add a route under /api/bloem/v1",
        "Typed and declared in OpenAPI, so it appears in the API by topic. Clients find it through a feature "
        "token in /api/bloem/v1/capabilities. It reuses only what Silo's own /api/v2 uses.", size=14)

    s.rect(40, 334, 236, 120, st["fill"], st["stroke"], rx=14, sw=1.8)
    no_mark(s, 70, 368)
    s.text(92, 374, "Never", size=17, weight="700", fill=st["label"])
    para(s, 60, 404, "A new route on Silo's /api/v1, or an edit to a Silo file.", 200, size=14, gap=19, fill=INK)
    footer(s, "Bloem's own API stays at v1. Its number is Bloem's, not Silo's: it grows by adding fields and routes.",
           y=510, fill=INK)
    return s


PICTURES = [("callers", callers), ("new-route", new_feature)]
