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
    s = Svg(1200, 580, "How a Bloem commit becomes an image",
            "A commit on main passes Bloem CI. Bloem's deploy tool builds the image for the commit it deploys, "
            "and only after that deploy passes verification does it publish the image: release-<sha9>, the full "
            "commit SHA, latest and a dated tag such as 2026.11.26 all name the same image. Commits that are never "
            "deployed get no image. Below: edge no longer moves and stays at 9fa2dad01; Silo's own tags, build-N, "
            "latest and the full SHA, belong to Silo's all-in-one image and are not published for Bloem.")
    s.header("How a Bloem commit becomes an image",
             "Images are built at deploy time. Only a deployed commit gets one.")
    a, p, g, w = TIERS["account"], TIERS["plan"], TIERS["grey"], TIERS["warn"]

    box(s, 40, 130, 200, 160, g, "A commit on main", "The source an image is built from, with the Silo "
        "revision it includes.", size=14)

    s.arrow(244, 210, 276, 210)
    box(s, 280, 130, 200, 160, p, "Bloem CI passes",
        "It tests the commit. It builds no image.", size=14, title_size=16)

    s.arrow(484, 210, 516, 210)
    box(s, 520, 130, 260, 160, BLOEM, "A deploy, verified",
        "Bloem's deploy tool builds the image, runs it in production, and checks it.", size=14,
        title_size=16)

    s.arrow(784, 210, 816, 210)
    s.rect(820, 130, 340, 160, INK, rx=16)
    s.text(990, 162, "Then published, one image", size=16, weight="700", fill="#FFFFFF", anchor="middle")
    for i, tag in enumerate(["bloem-server:release-<sha9>", "bloem-server:<full SHA>",
                             "bloem-server:latest", "bloem-server:2026.11.26"]):
        _mono(s, 990, 194 + i * 22, tag, size=13, fill="#E5E7EB", anchor="middle")

    s.rect(40, 340, 548, 120, w["fill"], w["stroke"], rx=14, sw=1.6)
    s.text(64, 376, "edge no longer moves", size=16, weight="700", fill=w["label"])
    para(s, 64, 406, "It stays at 9fa2dad01, the last image Bloem Image published before that workflow was "
         "disabled. Use latest or a dated tag instead.", 500, size=14, gap=19)

    s.rect(612, 340, 548, 120, SILO["fill"], SILO["stroke"], rx=14, sw=1.6)
    no_mark(s, 642, 370)
    s.text(666, 376, "Not published for Bloem", size=16, weight="700", fill=SILO["label"])
    para(s, 636, 406, "Silo's tags build-N, latest and the full SHA name Silo's all-in-one image, with "
         "PostgreSQL, Redis and Meilisearch inside. Bloem's image is the server alone.", 500, size=14, gap=19)

    footer(s, "Bloem has one prerelease, v0.1.0-bloem.20261011.1. An installation is identified by its "
              "commit or dated tag, not by a release.", y=520, fill=INK)
    return s


PICTURES = [("images", images)]
