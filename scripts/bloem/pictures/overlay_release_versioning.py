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
    s = Svg(1200, 660, "How a Bloem commit reaches production",
            "A commit on main either passes Bloem CI, after which Bloem Image publishes an image tagged with the "
            "full commit SHA and moves latest while the commit is still main's newest, or, when the owner asks, "
            "is built locally from a git archive of the exact commit with the same build arguments. Either way "
            "production runs the image as bloem-server:release-short-sha, and a dated record names the commit, the "
            "Silo revision and the image digest. Below: Silo's own tags, build-N, latest and the full SHA, belong "
            "to Silo's all-in-one image and are not published for Bloem.")
    s.header("How a Bloem commit reaches production",
             "One image per commit, named by the commit. A dated record says what ran.")
    a, p, g, w, o = TIERS["account"], TIERS["plan"], TIERS["grey"], TIERS["warn"], TIERS["org"]

    # The commit.
    box(s, 40, 170, 200, 190, g, "A commit on main", "The source an image is built from, with the Silo "
        "revision it includes.", size=14)

    # Upper path: Bloem Image.
    s.arrow(244, 230, 296, 200)
    box(s, 300, 124, 260, 140, p, "Bloem CI, then Bloem Image",
        "Only after CI passes on a push to main.", size=14, title_size=16)
    s.arrow(564, 194, 600, 194)
    s.rect(604, 124, 300, 140, a["fill"], a["stroke"], rx=14, sw=1.8)
    s.text(624, 156, "Published", size=16, weight="700")
    _mono(s, 624, 184, "bloem-server:<full SHA>", size=13)
    _mono(s, 624, 206, "bloem-server:latest", size=13)
    s.text(624, 236, "latest: only while the commit", size=13, fill=MUTED)
    s.text(624, 254, "is still main's newest", size=13, fill=MUTED)

    # Lower path: local build.
    s.arrow(244, 310, 296, 340)
    box(s, 300, 290, 604, 112, w, "Built locally, when the owner asks",
        "From a git archive of the exact commit, with Bloem Image's build arguments, without waiting for CI. "
        "Most deployments since October 28 were built this way.", size=14, title_size=16)

    # Production.
    s.path("M908,194 L924,194 L924,256 L940,256")
    s.path("M908,346 L924,346 L924,300 L940,300")
    s.rect(944, 214, 216, 136, INK, rx=16)
    s.text(1052, 248, "Production", size=18, weight="700", fill="#FFFFFF", anchor="middle")
    _mono(s, 1052, 280, "bloem-server:", size=13, fill="#E5E7EB", anchor="middle")
    _mono(s, 1052, 300, "release-<short SHA>", size=13, fill="#E5E7EB", anchor="middle")
    s.arrow(1052, 354, 1052, 446)
    s.rect(904, 450, 256, 92, BLOEM["fill"], BLOEM["stroke"], rx=14, sw=1.6)
    s.text(924, 480, "A dated record", size=16, weight="700")
    s.lines(924, 504, ["Commit, Silo revision,", "image digest, checks."], size=13.5, gap=18)

    # Silo's tags.
    s.rect(40, 470, 820, 104, SILO["fill"], SILO["stroke"], rx=14, sw=1.6)
    no_mark(s, 70, 506)
    s.text(94, 512, "Not published for Bloem", size=16, weight="700", fill=SILO["label"])
    para(s, 64, 542, "Silo's tags build-N, latest and the full SHA name Silo's all-in-one image, with "
         "PostgreSQL, Redis and Meilisearch inside. Bloem's image is the server alone.", 772, size=14, gap=19)

    footer(s, "Bloem has one prerelease, v0.1.0-bloem.20261011.1. Releases are not how a deployment is "
              "identified: its commit is.", y=622, fill=INK)
    return s


PICTURES = [("images", images)]
