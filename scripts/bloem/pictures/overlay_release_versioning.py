"""Pictures for docs/bloem/overlays/release-versioning.md."""
from xml.sax.saxutils import escape

from kit import INK, MUTED, TIERS, Svg, box, footer, no_mark, para

BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}
SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
MONO = "'SFMono-Regular', Menlo, Consolas, 'Liberation Mono', monospace"


def _mono(s, x, y, text, size=13, fill=INK, anchor="start"):
    s.add(f'<text x="{x}" y="{y}" font-family="{MONO}" font-size="{size}" fill="{fill}" '
          f'text-anchor="{anchor}">{escape(text)}</text>')


def images():
    s = Svg(1200, 600, "How a Bloem commit becomes an image",
            "A commit on main passes Bloem CI. Bloem Image then publishes an image tagged with the full commit SHA "
            "and moves edge while the commit is still main's newest. After a deploy of that commit passes "
            "verification, Bloem Release points latest and a dated tag such as 2026.11.17 at the same image. "
            "Below: Silo's own tags, build-N, latest and the full SHA, belong to Silo's all-in-one image and are "
            "not published for Bloem.")
    s.header("How a Bloem commit becomes an image",
             "One image per commit, named by the commit. Release tags point at it.")
    a, p, g, w, o = TIERS["account"], TIERS["plan"], TIERS["grey"], TIERS["warn"], TIERS["org"]

    box(s, 40, 130, 200, 160, g, "A commit on main", "The source an image is built from, with the Silo "
        "revision it includes.", size=14)

    s.arrow(244, 210, 296, 210)
    box(s, 300, 130, 260, 160, p, "Bloem CI, then Bloem Image",
        "Only after CI passes on a push to main.", size=14, title_size=16)

    s.arrow(564, 210, 600, 210)
    s.rect(604, 130, 280, 160, a["fill"], a["stroke"], rx=14, sw=1.8)
    s.text(624, 162, "Published by Bloem Image", size=16, weight="700")
    _mono(s, 624, 190, "bloem-server:<full SHA>", size=13)
    _mono(s, 624, 212, "bloem-server:edge", size=13)
    s.text(624, 244, "edge: only while the commit", size=13, fill=MUTED)
    s.text(624, 262, "is still main's newest", size=13, fill=MUTED)

    s.arrow(888, 210, 924, 210)
    s.rect(928, 130, 232, 160, INK, rx=16)
    s.text(1044, 162, "After a verified deploy", size=16, weight="700", fill="#FFFFFF", anchor="middle")
    s.text(1044, 186, "Bloem Release points", size=13, fill="#E5E7EB", anchor="middle")
    _mono(s, 1044, 218, "bloem-server:latest", size=13, fill="#E5E7EB", anchor="middle")
    _mono(s, 1044, 240, "bloem-server:2026.11.17", size=13, fill="#E5E7EB", anchor="middle")
    s.text(1044, 268, "at that image", size=13, fill="#E5E7EB", anchor="middle")

    s.rect(40, 350, 1120, 104, SILO["fill"], SILO["stroke"], rx=14, sw=1.6)
    no_mark(s, 70, 386)
    s.text(94, 392, "Not published for Bloem", size=16, weight="700", fill=SILO["label"])
    para(s, 64, 422, "Silo's tags build-N, latest and the full SHA name Silo's all-in-one image, with "
         "PostgreSQL, Redis and Meilisearch inside. Bloem's image is the server alone.", 1070, size=14, gap=19)

    footer(s, "Bloem has one prerelease, v0.1.0-bloem.20261011.1. An installation is identified by its "
              "commit or dated tag, not by a release.", y=520, fill=INK)
    return s


PICTURES = [("images", images)]
