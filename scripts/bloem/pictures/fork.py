"""Pictures for FORK.md (Bloem's fork of Silo Server)."""
from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}


def _commit(s, cx, cy, color, ring=False):
    if ring:
        s.add(f'<circle cx="{cx}" cy="{cy}" r="14" fill="#FFFFFF" stroke="{color}" stroke-width="2"/>')
    s.add(f'<circle cx="{cx}" cy="{cy}" r="9" fill="{color}"/>')


def _line(s, x1, y1, x2, y2, color, width=3, dash=None):
    d = f' stroke-dasharray="{dash}"' if dash else ""
    s.add(f'<line x1="{x1}" y1="{y1}" x2="{x2}" y2="{y2}" stroke="{color}" stroke-width="{width}"{d}/>')


def tracking_fork():
    s = Svg(1200, 700, "A tracking fork, not a copy",
            "Left: the hard fork Bloem tried first, a new first commit with no link to Silo's history and the "
            "module renamed in 1,178 files, so every Silo change had to be copied by hand. Right: the tracking "
            "fork Bloem is, sharing Silo's history, with Silo's work merged in again and again beside Bloem's own "
            "commits.")
    s.header("A tracking fork, not a copy",
             "Bloem shares Silo's history, so each Silo change is merged in rather than copied by hand.")

    # Left: the hard fork.
    s.rect(40, 124, 540, 470, "#FFFFFF", "#E5E7EB", rx=16)
    s.text(64, 162, "Tried first, and dropped", size=19, weight="700")
    s.text(64, 186, "A hard fork", size=14, fill=MUTED)
    s.text(64, 226, "Silo's history", size=13.5, weight="600", fill=SILO["label"])
    _line(s, 90, 250, 540, 250, SILO["stroke"])
    for x in range(100, 541, 70):
        _commit(s, x, 250, SILO["stroke"])
    s.text(64, 316, "Bloem, as a copy", size=13.5, weight="600", fill=BLOEM["label"])
    _line(s, 100, 340, 540, 340, BLOEM["stroke"])
    _commit(s, 100, 340, BLOEM["stroke"], ring=True)
    for x in range(170, 541, 70):
        _commit(s, x, 340, BLOEM["stroke"])
    _line(s, 380, 262, 380, 326, LINE, width=2, dash="5 5")
    no_mark(s, 380, 295)
    rows = [
        "A new first commit, with no link to Silo's history.",
        "The module path renamed: 1,178 files changed.",
        "Every Silo change copied by hand, and in conflict.",
    ]
    y = 420
    for r in rows:
        no_mark(s, 76, y - 5, r=10)
        para(s, 96, y, r, 460, size=14.5, fill=INK)
        y += 46
    s.text(64, 566, "Replaced on 13 August 2026", size=13.5, fill=SOFT)

    # Right: the tracking fork.
    s.rect(620, 124, 540, 470, "#FFFFFF", "#E5E7EB", rx=16)
    s.text(644, 162, "What Bloem is", size=19, weight="700")
    s.text(644, 186, "A tracking fork", size=14, fill=MUTED)
    s.text(644, 226, "Silo's history", size=13.5, weight="600", fill=SILO["label"])
    xs = [670, 745, 820, 895, 970, 1045, 1120]
    _line(s, 660, 250, 1130, 250, SILO["stroke"])
    for x in xs:
        _commit(s, x, 250, SILO["stroke"])
    s.text(644, 378, "Bloem's main", size=13.5, weight="600", fill=BLOEM["label"])
    _line(s, 670, 250, 745, 340, BLOEM["stroke"])
    _line(s, 745, 340, 1130, 340, BLOEM["stroke"])
    for x in [745, 820, 970, 1120]:
        _commit(s, x, 340, BLOEM["stroke"])
    for x in [895, 1045]:
        s.arrow(x, 262, x, 322, color=SILO["stroke"])
        _commit(s, x, 340, BLOEM["stroke"], ring=True)
    s.text(902, 298, "merge", size=12.5, fill=SILO["label"])
    s.text(1052, 298, "merge", size=12.5, fill=SILO["label"])
    rows = [
        "Bloem's main shares Silo's history.",
        "Silo's work comes in as ordinary merges, daily.",
        "The module path stays Silo's own.",
    ]
    y = 420
    for r in rows:
        yes_mark(s, 656, y - 5, r=10)
        para(s, 676, y, r, 460, size=14.5, fill=INK)
        y += 46
    # Legend.
    _commit(s, 652, 562, SILO["stroke"])
    s.text(668, 567, "Silo's commit", size=13, fill=MUTED)
    _commit(s, 790, 562, BLOEM["stroke"])
    s.text(806, 567, "Bloem's own commit", size=13, fill=MUTED)
    _commit(s, 966, 562, BLOEM["stroke"], ring=True)
    s.text(986, 567, "Silo merged in", size=13, fill=MUTED)

    footer(s, "Renaming the module path could still happen any time. Losing the ability to merge could not be "
              "undone, so the module path stays github.com/Silo-Server/silo-server.", y=650, fill=INK)
    return s


def what_bloem_adds():
    s = Svg(1200, 700, "What Bloem adds to Silo",
            "On the left, Silo Server, whose features all stay. On the right, what Bloem adds beside it: "
            "organizations, accounts and plans; Live TV and recordings; libraries on storage sources; an API for "
            "Bloem's own apps; organization domains and branding; switching between Silo and Bloem. Below, what "
            "Bloem added once and took out again.")
    s.header("What Bloem adds to Silo",
             "Bloem is Silo plus these additions. Each one sits beside Silo's code, not inside it.")

    s.rect(40, 124, 330, 372, SILO["fill"], SILO["stroke"], rx=16, sw=2)
    s.text(205, 164, "Silo Server", size=22, weight="700", fill=SILO["label"], anchor="middle")
    para(s, 64, 198, "A self-hosted media server for films, series, audiobooks, ebooks, podcasts and manga.",
         282, size=14.5, fill=INK)
    keeps = ["Its libraries and catalog", "Its apps and its API", "Jellyfin and Audiobookshelf apps",
             "Its migrations and settings"]
    y = 290
    for k in keeps:
        yes_mark(s, 74, y - 5, r=10)
        s.text(94, y, k, size=14.5, fill=INK)
        y += 38
    para(s, 64, 452, "All of it stays: Bloem merges Silo's work every day.", 282, size=14, fill=MUTED)
    s.arrow(374, 310, 404, 310)
    s.text(389, 296, "+", size=22, weight="700", fill=BLOEM["label"], anchor="middle")

    adds = [
        ("Organizations and plans", "Customers kept apart, with accounts, seats and plans."),
        ("Live TV and recordings", "Tuners, Xtream providers, a guide and a recorder."),
        ("Storage sources", "Libraries read from S3 or Bookwarehouse. Experimental."),
        ("An API for Bloem's apps", "Richer title pages, profile sign-ins, server-side search."),
        ("Domains and branding", "Each organization on its own address, name and colours."),
        ("Switching with Silo", "One database, served by Silo or by Bloem, one at a time."),
    ]
    for i, (t, b) in enumerate(adds):
        col, row = i % 2, i // 2
        x, y = 410 + col * 380, 124 + row * 128
        box(s, x, y, 360, 116, BLOEM, t, b, size=14.5, title_size=17)

    s.text(40, 552, "Added once, then taken out again", size=17, weight="700")
    gone = ["A gateway for companion apps", "Pastime tracking", "A music catalog", "Bundled worker processes"]
    x = 40
    for g in gone:
        w = s.badge(x, 570, g, TIERS["grey"], size=14)
        x += w + 14
    footer(s, "Bloem's name and look are applied when the web app is built, so Silo's source stays as Silo "
              "wrote it.", y=656, fill=INK)
    return s


PICTURES = [("tracking-fork", tracking_fork), ("what-bloem-adds", what_bloem_adds)]
