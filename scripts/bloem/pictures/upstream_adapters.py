"""Pictures for docs/architecture/bloem-upstream-adapters.md."""
from xml.sax.saxutils import escape

from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, footer, para, yes_mark

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}
MONO = "'SFMono-Regular', Menlo, Consolas, 'Liberation Mono', monospace"


def _code(s, x, y, rows, size=13.5, gap=21):
    for i, (txt, color) in enumerate(rows):
        s.add(f'<text x="{x}" y="{y + i * gap}" font-family="{MONO}" font-size="{size}" fill="{color}" '
              f'xml:space="preserve">{escape(txt)}</text>')


def seam_and_adapter():
    s = Svg(1200, 720, "Two ways Bloem changes what Silo does",
            "Left, the preferred way: an adapter, a Bloem-owned wrapper with the same shape as Silo's code, sits "
            "between Silo's caller and Silo's unchanged code, and no Silo file changes. Right, when there is no "
            "other way: a seam, one hook line in a Silo file that calls Bloem's code in a file of its own. Every "
            "seam is listed in contracts/seams.txt, and the list may only get shorter.")
    s.header("Two ways Bloem changes what Silo does",
             "Bloem prefers a wrapper around Silo's code. When it must touch a Silo file, it adds one hook line.")

    # Left: adapter.
    s.rect(40, 124, 540, 500, "#FFFFFF", "#E5E7EB", rx=16)
    yes_mark(s, 70, 160)
    s.text(92, 166, "An adapter", size=20, weight="700")
    s.text(64, 194, "Preferred. No Silo file changes.", size=14, fill=MUTED)
    box(s, 64, 220, 492, 82, SILO, "Silo's caller", "Calls the function it always called.", size=14)
    s.arrow(310, 304, 310, 336)
    box(s, 64, 340, 492, 104, BLOEM, "Bloem's wrapper, in a bloem_* file",
        "Same name and shape as Silo's. Adds Bloem's check, such as the viewer's organization, then calls "
        "Silo.", size=14)
    s.arrow(310, 446, 310, 478)
    box(s, 64, 482, 492, 82, SILO, "Silo's code", "Exactly as Silo wrote it.", size=14)
    s.text(64, 598, "Example: migrations.BloemFS wraps Silo's migration files.", size=13.5, fill=SOFT)

    # Right: seam.
    s.rect(620, 124, 540, 500, "#FFFFFF", "#E5E7EB", rx=16)
    s.add(f'<circle cx="650" cy="160" r="12" fill="{TIERS["warn"]["stroke"]}"/>')
    s.text(650, 165, "!", size=15, weight="700", fill="#FFFFFF", anchor="middle")
    s.text(672, 166, "A seam", size=20, weight="700")
    s.text(644, 194, "Only when no wrapper can do it.", size=14, fill=MUTED)
    s.text(644, 230, "internal/access/group_store.go, a Silo file", size=14, weight="700", fill=SILO["label"])
    s.rect(644, 242, 492, 170, "#0F172A", rx=12)
    _code(s, 664, 272, [
        ("func groupPolicyForUser(ctx, db, userID) {", "#7DD3FC"),
        ("    // Bloem: groups are organization-owned", "#FDBA74"),
        ("    return bloemAccountGroupPolicy(", "#FDBA74"),
        ("        ctx, db, userID)", "#FDBA74"),
        ("}", "#7DD3FC"),
    ], size=13.5, gap=26)
    s.arrow(890, 416, 890, 448)
    box(s, 644, 452, 492, 82, BLOEM, "Bloem's code, in a bloem_* file",
        "Everything the hook needs lives here.", size=14)
    s.rect(644, 552, 14, 14, "#FDBA74", rx=3)
    s.text(666, 564, "Bloem's hook", size=13, fill=MUTED)
    s.rect(790, 552, 14, 14, "#7DD3FC", rx=3)
    s.text(812, 564, "Silo's lines, exactly", size=13, fill=MUTED)
    s.text(644, 598, "Listed in contracts/seams.txt: 304 on 1 November 2026.", size=13.5, fill=SOFT)

    footer(s, "Each seam conflicts again whenever Silo changes that file. The seam list may only get shorter, "
              "and adding a seam is a reviewed decision.", y=676, fill=INK)
    return s


def where_adapters_sit():
    s = Svg(1200, 700, "Where Bloem's adapters sit",
            "Ten places where Bloem wraps Silo: migrations, playback limits, downloads, playback admission, "
            "access groups, sign-in and invitations, live sockets, recommendations, the web build, and the "
            "SQLite user store. Each keeps Silo's own call shape.")
    s.header("Where Bloem's adapters sit",
             "Each one keeps Silo's call shape, so Silo's callers and tests run through it unchanged.")
    o, a, p, f = TIERS["org"], TIERS["account"], TIERS["platform"], TIERS["profile"]
    cards = [
        (p, "Migrations", "BloemFS adapts a few old Silo migrations as they are read."),
        (f, "Playback limits", "A profile-aware limit provider beside Silo's account-only one."),
        (f, "Downloads", "Silo's download code reads the request's profile from the context."),
        (o, "Playback admission", "A wrapper puts the request's trusted organization facts in first."),
        (o, "Access groups", "Groups belong to an organization, and resolve per profile."),
        (a, "Sign-in and invitations", "Creating an account also links it to its one organization."),
        (o, "Live sockets", "Silo's handshake, with Bloem's organization check in Silo's hook."),
        (o, "Recommendations", "Wrapped in the same organization-aware viewer resolver as the rest."),
        (BLOEM, "The web build", "Bloem's name, icons and preview mode are applied at build time."),
        (TIERS["grey"], "SQLite user store", "A repair step inside Silo's migration, with no version of its own."),
    ]
    for i, (c, t, b) in enumerate(cards):
        col, row = i % 2, i // 2
        x, y = 40 + col * 570, 124 + row * 102
        s.rect(x, y, 550, 90, "#FFFFFF", "#E5E7EB", rx=12)
        s.rect(x, y, 8, 90, c["stroke"], rx=4)
        s.text(x + 26, y + 34, t, size=17, weight="700")
        para(s, x + 26, y + 62, b, 500, size=14.5, gap=20)
    footer(s, "None of them grants access. They add Bloem's organization and profile checks; the host still "
              "decides identity, PINs, libraries and limits.", y=664, fill=INK)
    return s


PICTURES = [("seam-and-adapter", seam_and_adapter), ("where-adapters-sit", where_adapters_sit)]
