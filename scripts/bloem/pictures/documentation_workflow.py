"""Pictures for docs/bloem/documentation-workflow.md."""
from xml.sax.saxutils import escape

from kit import INK, MUTED, SOFT, TIERS, Svg, footer, para

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}
MONO = "'SFMono-Regular', Menlo, Consolas, 'Liberation Mono', monospace"


def _mono(s, x, y, text, size=13.5, fill=SOFT, anchor="start"):
    s.add(f'<text x="{x}" y="{y}" font-family="{MONO}" font-size="{size}" fill="{fill}" '
          f'text-anchor="{anchor}">{escape(text)}</text>')


def _doc(s, x, y, w, h, color, title, path, body, dash=None, tag=None):
    """A tinted box with a title, a monospaced path and wrapped body text."""
    s.rect(x, y, w, h, color["fill"], color["stroke"], rx=14, sw=1.8, dash=dash)
    s.text(x + 20, y + 34, title, size=18, weight="700")
    _mono(s, x + 20, y + 60, path)
    para(s, x + 20, y + 92, body, w - 40, size=14.5, gap=20)
    if tag:
        s.badge(x + w - 18, y + 14, tag, color, size=12.5, anchor="end")


def where_it_goes():
    s = Svg(1200, 690, "Where each kind of documentation lives",
            "Top: Silo's own document stays exactly as Silo wrote it, and a Bloem overlay beside it lists what "
            "Bloem adds or replaces, section by section; a reader reads Silo's page first, then the overlay. "
            "Bottom: Bloem's own pages, dated records that keep the facts of their day, and temporary plans that "
            "never get committed.")
    s.header("Where each kind of documentation lives",
             "Silo's files stay Silo's. Bloem writes beside them, never inside them.")
    org, warn, grey = TIERS["org"], TIERS["warn"], TIERS["grey"]

    s.text(40, 142, "A Silo document and its overlay", size=17, weight="700")
    _doc(s, 40, 158, 500, 212, SILO, "Silo's document", "docs/auth-api.md",
         "Exactly as Silo wrote it, so taking in Silo's next change stays easy.", tag="Never edited")
    for i, row in enumerate(["## Account passwords", "## Sign-in attempts per name", "## Ordinary v2 authentication"]):
        _mono(s, 60, 304 + i * 22, row, size=13, fill=SILO["label"])
    s.arrow(546, 264, 650, 264)
    s.text(598, 252, "then", size=14, weight="700", fill=MUTED, anchor="middle")
    _doc(s, 656, 158, 504, 212, BLOEM, "Bloem's overlay", "docs/bloem/overlays/auth-api.md",
         "Lists, section by section, what Bloem adds to Silo's page or replaces in it.")
    for i, row in enumerate(["Location: “Account passwords”", "  Bloem replaces the first paragraph",
                             "Location: “Sign-in attempts per name”", "  Bloem adds a paragraph"]):
        _mono(s, 676, 302 + i * 19, row, size=12.5, fill=BLOEM["label"])

    s.text(40, 416, "Bloem's own writing", size=17, weight="700")
    _doc(s, 40, 432, 360, 150, org, "Bloem pages", "docs/architecture/bloem-*.md",
         "Explain what Bloem has and Silo does not. Each is listed in the documentation index.")
    _doc(s, 420, 432, 360, 150, warn, "Dated records", "docs/operations/",
         "Say what happened on one day. They keep their facts for good.")
    _doc(s, 800, 432, 360, 150, grey, "Plans", "docs/superpowers/ (ignored)",
         "Working notes. Their lasting facts move into a Bloem page; the plan never lands.", dash="7 5")

    footer(s, "The code is the source of truth: a page that disagrees with the code is wrong.", y=640, fill=INK)
    return s


def page_shape():
    s = Svg(1200, 884, "The shape of a Bloem page",
            "A page drawn as eight stacked parts: the title, the provenance note, one paragraph on what the page "
            "walks through, the plain sections with pictures, a made-up example, the who-can-change-what table, "
            "the words-used-in-the-code table, and the For developers section. Brackets on the right group them: "
            "the opening, the plain part for every reader, the bridge to the code, and everything technical.")
    s.header("The shape of a Bloem page",
             "The same eight parts, in the same order, on every page. Plain first, technical last.")
    g, a, p, o = TIERS["grey"], TIERS["account"], TIERS["plan"], TIERS["org"]

    # The page itself.
    s.rect(40, 120, 640, 724, "#FFFFFF", "#D1D5DB", rx=16, sw=1.5)
    bands = [
        (g, 50, "Title", "“How X works”"),
        (g, 58, "Provenance and status", "Date, commit, Bloem or Silo"),
        (g, 50, "What this page walks through", "One short paragraph"),
        (a, 150, "Plain sections, with pictures", "Short sentences, bold new terms"),
        (a, 56, "A made-up example", "“This example is made up.”"),
        (p, 56, "Who can change what", "When roles are involved"),
        (p, 56, "Words used in the code", "Plain words → the code's words"),
        (o, 110, "For developers", "Every rule, setting and test name"),
    ]
    y = 140
    spans = []
    for i, (c, h, title, note) in enumerate(bands):
        s.rect(60, y, 600, h, c["fill"], c["stroke"], rx=10, sw=1.4)
        cy = y + min(h, 58) / 2
        s.number(86, cy, i + 1, c["stroke"], r=12)
        s.text(110, cy + 6, title, size=16.5, weight="700")
        s.text(644, cy + 5, note, size=13.5, fill=MUTED, anchor="end")
        if i == 3:
            # A small picture inside the plain sections.
            s.rect(110, y + 64, 220, 70, "#FFFFFF", c["stroke"], rx=8, sw=1.2, dash="5 4")
            s.text(220, y + 104, "a picture", size=13.5, fill=c["label"], weight="600", anchor="middle")
            s.lines(352, y + 92, ["Where a picture explains", "better than words."], size=13.5, gap=19)
        if i == 7:
            s.lines(110, y + 74, ["API links, then the technical rules, then the", "related technical pages."],
                    size=13.5, gap=19)
        spans.append((y, y + h))
        y += h + 14

    # Brackets on the right.
    groups = [
        (0, 2, g, "The opening", "What this is, where it came from, what follows."),
        (3, 4, a, "For every reader", "No code needed. Pictures carry the hard parts."),
        (5, 6, p, "The bridge", "From the page's plain words to the code's words."),
        (7, 7, o, "For people who change the code", "Loses nothing the old page said that is still true."),
    ]
    for first, last, c, title, body in groups:
        top, bottom = spans[first][0] + 4, spans[last][1] - 4
        s.add(f'<path d="M700,{top} L716,{top} L716,{bottom} L700,{bottom}" fill="none" '
              f'stroke="{c["stroke"]}" stroke-width="2.5" stroke-linejoin="round"/>')
        mid = (top + bottom) / 2
        s.add(f'<line x1="716" y1="{mid}" x2="734" y2="{mid}" stroke="{c["stroke"]}" stroke-width="2.5"/>')
        s.text(748, mid - 4, title, size=17, weight="700", fill=c["label"])
        para(s, 748, mid + 20, body, 400, size=14, gap=19)
    return s


def pictures_flow():
    s = Svg(1200, 560, "How a picture is made",
            "Five steps from left to right: a drawing module in scripts/bloem/pictures uses the shared kit; "
            "draw-pictures.py writes its SVG files under docs/architecture/images; you look at a screenshot and "
            "go back to the module to fix anything that overlaps; the page links the SVG with alt text. Below: "
            "CI redraws every picture and fails when a committed file differs.")
    s.header("How a picture is made",
             "Pictures are drawn by code, so the next change can redraw them.")
    a, p, o, w, g = TIERS["account"], TIERS["plan"], TIERS["org"], TIERS["warn"], TIERS["grey"]
    steps = [
        (BLOEM, "Write a module", "scripts/bloem/pictures/", "foo_bar.py",
         "Named after the page. Lists its pictures in PICTURES and uses kit.py."),
        (g, "Draw", "draw-pictures.py", "foo-bar",
         "Runs your module and writes one SVG file per picture."),
        (o, "SVG files", "images/foo-bar/", "name.svg",
         "Each has a title and a description for screen readers."),
        (w, "Look at it", "headless browser", "screenshot",
         "Text that overlaps, touches a border or runs off: fix it and draw again."),
        (a, "Link it", "![alt text](", "images/…svg)",
         "The alt text says what the picture shows."),
    ]
    x, y, bw, bh, gap = 40, 132, 200, 200, 30
    for i, (c, title, path1, path2, body) in enumerate(steps):
        s.rect(x, y, bw, bh, c["fill"], c["stroke"], rx=14, sw=1.8)
        s.number(x + 26, y + 28, i + 1, c["stroke"], r=12)
        s.text(x + 46, y + 34, title, size=16.5, weight="700")
        _mono(s, x + 16, y + 66, path1, size=12.5)
        _mono(s, x + 16, y + 86, path2, size=12.5, fill=INK)
        para(s, x + 16, y + 120, body, bw - 32, size=14, gap=19)
        if i < len(steps) - 1:
            s.arrow(x + bw + 3, y + bh / 2, x + bw + gap - 3, y + bh / 2)
        x += bw + gap

    # The fix loop from step 4 back to step 1.
    x4 = 40 + 3 * (bw + gap) + bw / 2
    x1 = 40 + bw / 2
    s.path(f"M{x4},{y + bh + 2} L{x4},{y + bh + 46} L{x1},{y + bh + 46} L{x1},{y + bh + 6}",
           color=w["stroke"], dash="6 5")
    s.text((x1 + x4) / 2, y + bh + 38, "Fix it in the module, never in the SVG", size=14, weight="700",
           fill=w["label"], anchor="middle")

    # CI check.
    s.rect(40, 420, 1120, 96, p["fill"], p["stroke"], rx=14, sw=1.6)
    s.text(64, 454, "In CI", size=17, weight="700", fill=p["label"])
    para(s, 64, 482, "make verify-bloem-api-catalogue runs draw-pictures.py --check, which redraws every "
         "picture and fails when a committed SVG differs from what its module draws.", 1070, size=14.5, gap=20)
    return s


PICTURES = [("where-it-goes", where_it_goes), ("page-shape", page_shape), ("pictures-flow", pictures_flow)]
