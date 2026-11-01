"""Pictures for docs/architecture/bloem-silo-switching-explained.md."""
from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}
DB = {"fill": "#F3F4F6", "stroke": "#6B7280", "label": "#374151"}


def _cylinder(s, cx, top, w, h, label, sub=None):
    rx, ry = w / 2, 14
    s.add(f'<path d="M{cx - rx},{top} L{cx - rx},{top + h} A{rx},{ry} 0 0 0 {cx + rx},{top + h} '
          f'L{cx + rx},{top} Z" fill="{DB["fill"]}" stroke="{DB["stroke"]}" stroke-width="1.8"/>')
    s.add(f'<ellipse cx="{cx}" cy="{top}" rx="{rx}" ry="{ry}" fill="#FFFFFF" stroke="{DB["stroke"]}" stroke-width="1.8"/>')
    s.text(cx, top + h / 2 + 8, label, size=17, weight="700", anchor="middle")
    if sub:
        s.text(cx, top + h / 2 + 30, sub, size=13.5, fill=MUTED, anchor="middle")


def taking_turns():
    s = Svg(1200, 700, "Silo and Bloem taking turns",
            "One database in the middle, Silo on the left and Bloem on the right, each with its own cache. Below, "
            "a timeline: Silo serves, Bloem takes over and switching is enabled, Silo serves again, then Bloem. "
            "Only one runs at a time.")
    s.header("Silo and Bloem taking turns",
             "One database, two servers, never both at once. Each reads what the other wrote.")
    _cylinder(s, 600, 150, 240, 150, "The database", "accounts, profiles, libraries, history")
    for x, color, name, sub in [(40, SILO, "Silo", "the original server"), (860, BLOEM, "Bloem", "Silo plus Bloem's additions")]:
        s.rect(x, 140, 300, 170, color["fill"], color["stroke"], rx=16, sw=2)
        s.text(x + 150, 186, name, size=24, weight="700", fill=color["label"], anchor="middle")
        s.text(x + 150, 212, sub, size=14, fill=MUTED, anchor="middle")
        s.rect(x + 70, 236, 160, 48, "#FFFFFF", color["stroke"], rx=10, sw=1.2)
        s.text(x + 150, 266, "its own cache", size=14, weight="600", fill=color["label"], anchor="middle")
    s.add(f'<line x1="344" y1="225" x2="476" y2="225" stroke="{SILO["stroke"]}" stroke-width="2.5" stroke-dasharray="7 6"/>')
    s.add(f'<line x1="724" y1="225" x2="856" y2="225" stroke="{BLOEM["stroke"]}" stroke-width="2.5"/>')
    s.text(410, 212, "stopped", size=13, fill=SOFT, anchor="middle")
    s.text(790, 212, "serving", size=13, weight="700", fill=BLOEM["label"], anchor="middle")

    s.text(40, 376, "A switch, step by step", size=18, weight="700")
    turns = [
        (SILO, "Silo serves", "An ordinary Silo install, with its accounts and libraries."),
        (BLOEM, "Bloem takes over", "Stop Silo. Start Bloem; it adds its tables. Then switch on Silo switching."),
        (BLOEM, "Bloem serves", "Everything still there, plus Bloem's features."),
        (SILO, "Silo again", "Stop Bloem, empty the cache, start Silo. It reads what Bloem wrote."),
        (BLOEM, "And back", "Either way, as often as needed."),
    ]
    x0, y0, cw, ch, gap = 40, 396, 204, 168, 25
    for i, (c, t, b) in enumerate(turns):
        x = x0 + i * (cw + gap)
        s.rect(x, y0, cw, ch, c["fill"], c["stroke"], rx=14)
        s.number(x + 26, y0 + 28, i + 1, c["stroke"])
        s.text(x + 16, y0 + 74, t, size=16.5, weight="700")
        para(s, x + 16, y0 + 102, b, cw - 30, size=13.5, gap=18)
        if i < len(turns) - 1:
            s.arrow(x + cw + 3, y0 + ch / 2, x + cw + gap - 3, y0 + ch / 2)
    footer(s, "Before the first switch, take a snapshot of the database. Run the Silo version Bloem has merged: "
              "a newer or much older Silo can misread the database.", y=626, fill=INK)
    return s


def what_each_sees():
    s = Svg(1200, 720, "What each server sees",
            "Two columns. Shared by both: accounts and passwords, profiles, libraries and the catalog, watch "
            "history, access groups, movie and series requests, and Jellyfin and Audiobookshelf app data. "
            "Bloem's only: organizations beyond the default one, plans, Live TV, storage-source libraries, "
            "promotions and seasonal packs, and organization domains and branding. Below, the three rules while switching is on.")
    s.header("What each server sees",
             "Bloem's extras stay in the database while Silo serves. They wake up again with Bloem.")
    a, g, st = TIERS["account"], TIERS["grey"], TIERS["stop"]
    s.rect(40, 124, 540, 356, a["fill"], a["stroke"], rx=16, sw=1.8)
    yes_mark(s, 70, 156)
    s.text(92, 162, "Both use, and both change", size=19, weight="700")
    shared = ["Accounts, passwords and sessions", "Profiles and their PINs", "Libraries and the catalog",
              "What everyone watched, and how far", "Access groups and account settings",
              "Movie and series requests", "Jellyfin and Audiobookshelf app data"]
    for i, t in enumerate(shared):
        y = 200 + i * 42
        s.add(f'<circle cx="72" cy="{y - 5}" r="4" fill="{a["stroke"]}"/>')
        s.text(88, y, t, size=15.5, fill=INK)
    s.rect(620, 124, 540, 356, BLOEM["fill"], BLOEM["stroke"], rx=16, sw=1.8)
    s.add(f'<circle cx="650" cy="156" r="12" fill="{BLOEM["stroke"]}"/>')
    s.text(650, 161, "B", size=13, weight="700", fill="#FFFFFF", anchor="middle")
    s.text(672, 162, "Only Bloem uses; kept while Silo serves", size=19, weight="700")
    only = ["Plans (entitlement templates)", "Live TV, guides and recordings", "Libraries on storage sources",
            "Audiobook and ebook requests", "Promotions, playback cards and seasonal packs",
            "Organization domains and branding"]
    for i, t in enumerate(only):
        y = 200 + i * 42
        s.add(f'<circle cx="652" cy="{y - 5}" r="4" fill="{BLOEM["stroke"]}"/>')
        s.text(668, y, t, size=15.5, fill=INK)

    s.text(40, 524, "Three rules while switching is on", size=18, weight="700")
    rules = [
        ("One organization", "Silo has no organizations, so a second one is refused."),
        ("No profile sign-ins", "Silo would treat a profile's own sign-in as the whole account, so none is allowed."),
        ("Silo's writes fit in", "A new account or profile Silo creates lands in the default organization."),
    ]
    for i, (t, b) in enumerate(rules):
        x = 40 + i * 380
        s.rect(x, 544, 360, 120, "#FFFFFF", "#E5E7EB", rx=12)
        s.rect(x, 544, 6, 120, SILO["stroke"], rx=3)
        s.text(x + 24, 576, t, size=16, weight="700")
        para(s, x + 24, 602, b, 316, size=14, gap=19)
    return s


def two_layouts():
    s = Svg(1200, 700, "Two ways Silo is installed",
            "Left: Silo with its own PostgreSQL and Redis containers; switching swaps the Silo container for a Bloem "
            "container on the same database. Right: Silo's all-in-one container with everything in one /config "
            "folder; switching swaps the image for Bloem built on the same all-in-one layer.")
    s.header("Two ways Silo is installed",
             "Both can switch to Bloem and back. Checked on 31 October 2026 with Silo 32a2f63e1 and Bloem 4edbee99d.")
    for x, title, sub in [(40, "Separate containers", "Silo's earlier Compose layout"),
                          (620, "Silo's all-in-one container", "What Silo's image is now")]:
        s.rect(x, 124, 540, 448, "#FFFFFF", "#E5E7EB", rx=16)
        s.text(x + 24, 160, title, size=20, weight="700")
        s.text(x + 24, 184, sub, size=14, fill=MUTED)

    # Left layout.
    s.rect(64, 212, 220, 92, SILO["fill"], SILO["stroke"], rx=12, sw=1.8)
    s.text(174, 252, "Silo container", size=16, weight="700", anchor="middle")
    s.text(174, 276, "swap this →", size=13.5, fill=SILO["label"], anchor="middle")
    s.rect(336, 212, 220, 92, BLOEM["fill"], BLOEM["stroke"], rx=12, sw=1.8, dash="6 5")
    s.text(446, 252, "Bloem container", size=16, weight="700", anchor="middle")
    s.text(446, 276, "Bloem's usual image", size=13.5, fill=BLOEM["label"], anchor="middle")
    _cylinder(s, 200, 350, 170, 80, "PostgreSQL")
    s.rect(330, 350, 170, 94, "#FFFFFF", DB["stroke"], rx=12, sw=1.5)
    s.text(415, 392, "Redis", size=16, weight="700", anchor="middle")
    s.text(415, 414, "emptied at each switch", size=12.5, fill=MUTED, anchor="middle")
    s.arrow(174, 306, 196, 334)
    s.arrow(446, 306, 240, 334, dash="6 5")
    para(s, 64, 494, "Stop Silo, start Bloem with the same database address and secret key.", 490, size=14,
         fill=INK)
    s.text(64, 548, "make silo-switch-check", size=13.5, weight="600", fill=SOFT)

    # Right layout.
    s.rect(644, 212, 492, 236, "#F9FAFB", DB["stroke"], rx=14, sw=1.6)
    s.text(664, 240, "/config folder", size=14, weight="700", fill=DB["label"])
    for i, (name, note) in enumerate([("silo", "the server's files"), ("postgres", "the database"),
                                      ("redis", "the cache: empty it at each switch")]):
        y = 256 + i * 58
        s.rect(664, y, 452, 46, "#FFFFFF", "#D1D5DB", rx=10)
        s.text(684, y + 29, name, size=15, weight="700")
        s.text(790, y + 29, note, size=14, fill=MUTED)
    s.rect(644, 462, 230, 64, SILO["fill"], SILO["stroke"], rx=12, sw=1.8)
    s.text(759, 500, "Silo's all-in-one", size=15, weight="700", anchor="middle")
    s.arrow(878, 494, 902, 494)
    s.rect(906, 462, 230, 64, BLOEM["fill"], BLOEM["stroke"], rx=12, sw=1.8, dash="6 5")
    s.text(1021, 488, "Bloem in the same", size=15, weight="700", anchor="middle")
    s.text(1021, 508, "all-in-one layer", size=15, weight="700", anchor="middle")
    s.text(644, 548, "make silo-switch-check-all-in-one", size=13.5, weight="600", fill=SOFT)

    footer(s, "Bloem does not publish an all-in-one image. Build one from the Bloem image you run, with Silo's "
              "own all-in-one recipe, which Bloem carries unchanged.", y=630, fill=INK)
    return s


PICTURES = [("taking-turns", taking_turns), ("what-each-sees", what_each_sees), ("two-layouts", two_layouts)]
