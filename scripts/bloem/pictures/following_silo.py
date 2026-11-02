"""Pictures for docs/architecture/bloem-following-silo.md."""
from xml.sax.saxutils import escape

from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, footer, para

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}
MONO = "'SFMono-Regular', Menlo, Consolas, 'Liberation Mono', monospace"


def _code(s, x, y, rows, size=14, gap=22):
    for i, (txt, color) in enumerate(rows):
        s.add(f'<text x="{x}" y="{y + i * gap}" font-family="{MONO}" font-size="{size}" fill="{color}" '
              f'xml:space="preserve">{escape(txt)}</text>')


def daily_flow():
    s = Svg(1200, 760, "How Bloem keeps up with Silo",
            "A maintainer merges Silo's latest work into a branch, taking Silo's side of every conflict. Every "
            "check runs; where Silo's change would break Bloem, Bloem's behavior goes back on its own lines, with "
            "a test, and the checks run again. The merge is pushed to Bloem's main, where Bloem CI checks it. "
            "From main, an image is built and deployed when the owner asks.")
    s.header("How Bloem keeps up with Silo",
             "Bloem is a tracking fork: it takes in Silo's work as ordinary merges instead of copying it by hand.")
    g, a, w, p = TIERS["grey"], TIERS["account"], TIERS["warn"], TIERS["plan"]

    s.text(40, 140, "Each Silo merge", size=17, weight="700")
    box(s, 40, 156, 200, 120, SILO, "Silo's main", "The Silo project's latest work.", size=14)
    s.arrow(244, 216, 276, 216)
    box(s, 280, 156, 220, 120, g, "The merge", "A maintainer merges Silo into a new branch. Silo wins every conflict.",
        size=14)
    s.arrow(504, 216, 540, 216)
    # Decision diamond.
    s.add(f'<path d="M620,150 L700,216 L620,282 L540,216 Z" fill="{w["fill"]}" stroke="{w["stroke"]}" stroke-width="1.8"/>')
    s.text(620, 212, "Does Bloem", size=14, weight="700", anchor="middle")
    s.text(620, 230, "still work?", size=14, weight="700", anchor="middle")
    s.arrow(704, 216, 756, 216)
    s.text(728, 206, "Yes", size=13, weight="700", fill=a["label"], anchor="middle")
    box(s, 760, 156, 180, 120, a, "Bloem's main", "Pushed once every check passes.", size=14)
    s.arrow(944, 216, 976, 216)
    box(s, 980, 156, 180, 120, p, "Bloem CI", "Checks the push like any other.", size=14)

    s.path("M620,286 L620,346")
    s.text(632, 322, "No", size=13, weight="700", fill=TIERS["stop"]["label"])
    box(s, 470, 350, 300, 150, BLOEM, "Bloem adapts",
        "Where Silo's change would break Bloem, Bloem's behavior goes back on its own lines, with a test. "
        "Then every check again.", size=14)
    s.path("M774,425 L850,425 L850,280")

    s.text(40, 560, "When the owner says deploy", size=17, weight="700")
    steps = [
        (a, "Bloem's main", "Everything merged and checked."),
        (g, "An image", "The server, the web app and its tools, labelled with the exact commit."),
        (w, "Safety first", "A snapshot of the server and a restore point in the database."),
        (p, "Restart", "Usually about a minute offline. Migrations run, then the new version serves."),
    ]
    x = 40
    for i, (c, t, b) in enumerate(steps):
        box(s, x, 576, 262, 140, c, t, b, size=14, title_size=16)
        if i < len(steps) - 1:
            s.arrow(x + 266, 646, x + 282, 646)
        x += 286
    return s


def silo_wins():
    s = Svg(1200, 760, "When Silo and Bloem change the same lines",
            "A real fix from 31 October 2026 in a household check. Left: Bloem's check folded into Silo's line. "
            "Right: Silo's line as Silo wrote it, and Bloem's check on its own lines above it. Below: where Bloem "
            "keeps its own work, and the seam list, which may only shrink.")
    s.header("When Silo and Bloem change the same lines",
             "Silo always wins. A real example from 31 October 2026, in the household check.")
    st, a = TIERS["stop"], TIERS["account"]

    # Before.
    s.text(40, 140, "Before: Bloem's check inside Silo's line", size=17, weight="700")
    s.rect(40, 156, 540, 250, "#0F172A", rx=14)
    _code(s, 64, 194, [
        ("// A remote-playback handoff never", "#94A3B8"),
        ("// manages the household.", "#94A3B8"),
        ("if bloemDirectProfileClaims(ctx) ||", "#FCA5A5"),
        ("    apimw.GetClaims(ctx).IsRemotePlayback() {", "#FCA5A5"),
        ("    return false, nil, nil", "#E2E8F0"),
        ("}", "#E2E8F0"),
    ], size=15, gap=27)
    s.arrow(586, 280, 616, 280)

    # After.
    s.text(620, 140, "After: Silo's line as Silo wrote it", size=17, weight="700")
    s.rect(620, 156, 540, 250, "#0F172A", rx=14)
    _code(s, 644, 194, [
        ("if bloemDirectProfileClaims(ctx) {", "#FDBA74"),
        ("    return false, nil, nil", "#FDBA74"),
        ("}", "#FDBA74"),
        ("// A remote-playback handoff never", "#7DD3FC"),
        ("// manages the household.", "#7DD3FC"),
        ("if apimw.GetClaims(ctx).IsRemotePlayback() {", "#7DD3FC"),
        ("    return false, nil, nil", "#7DD3FC"),
        ("}", "#7DD3FC"),
    ], size=15, gap=26)
    s.rect(644, 420, 14, 14, "#FDBA74", rx=3)
    s.text(666, 432, "Bloem's check, on its own lines", size=13.5, fill=MUTED)
    s.rect(910, 420, 14, 14, "#7DD3FC", rx=3)
    s.text(932, 432, "Silo's lines, exactly", size=13.5, fill=MUTED)
    para(s, 40, 432, "Same behavior. But a Silo line with Bloem inside it conflicts again whenever Silo changes "
         "it, and Silo changes every day.", 520, size=14, fill=INK)

    s.text(40, 510, "Where Bloem keeps its own work", size=17, weight="700")
    homes = [
        (BLOEM, "Its own files", "Files named bloem_*, and whole packages Silo does not have, such as Live TV."),
        (BLOEM, "Overlays", "Where a Silo document needs a Bloem note, a separate Bloem page carries it."),
        (st, "The seam list", "The Silo files Bloem still edits: 304 on 1 November 2026. The number may only go "
         "down."),
    ]
    for i, (c, t, b) in enumerate(homes):
        x = 40 + i * 380
        s.rect(x, 530, 360, 150, c["fill"], c["stroke"], rx=14, sw=1.6)
        s.text(x + 20, 564, t, size=17, weight="700")
        para(s, x + 20, 592, b, 320, size=14.5, gap=20)
    footer(s, "The aim: Bloem's additions sit beside Silo's code, never inside it, so taking Silo's work stays "
              "cheap.", y=724, fill=INK)
    return s


PICTURES = [("daily-flow", daily_flow), ("silo-wins", silo_wins)]
