"""Pictures for docs/bloem/overlays/architecture/scenario-acceptance.md."""
from kit import INK, MUTED, TIERS, Svg, box, footer

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}


def how_bloem_runs():
    s = Svg(1200, 620, "How Bloem runs Silo's scenario catalogs",
            "Silo's catalogs, unchanged and pinned by SHA-256, and Bloem's own rows for routes it adds to Silo's "
            "route groups, feed 49 written decisions, each replacing one expected answer with a reason. They run "
            "against Bloem's test setup, which recreates the default organization. The scenario executor runs "
            "them in its own CI job against an empty database; a pass is the scenario gate for one commit, not "
            "device acceptance.")
    s.header("How Bloem runs Silo's scenario catalogs",
             "Silo's files stay as they are. Every difference is a written decision.")
    w, o, p = TIERS["warn"], TIERS["org"], TIERS["plan"]

    box(s, 40, 128, 250, 168, SILO, "Silo's catalogs",
        "Recorded requests and the answers they must get. Unchanged, and pinned by checksum.", size=14)
    box(s, 40, 320, 250, 168, BLOEM, "Bloem's own rows",
        "For routes Bloem adds to Silo's route groups, in files of its own.", size=14)
    s.arrow(294, 212, 336, 290)
    s.arrow(294, 404, 336, 330)

    box(s, 340, 214, 250, 192, w, "49 written decisions",
        "Each replaces one expected answer with Bloem's, and says why. A changed Silo catalog fails the run.",
        size=14)
    s.arrow(594, 310, 626, 310)
    box(s, 630, 214, 250, 192, o, "Bloem's test setup",
        "Recreates the default organization; the test admin is the Owner; real avatar storage where a case "
        "needs it.", size=14)
    s.arrow(884, 310, 916, 310)
    box(s, 920, 214, 240, 192, p, "Scenario executor",
        "Its own CI job: up to 45 minutes, against an empty database it owns.", size=14)

    s.arrow(1040, 410, 1040, 444)
    s.rect(630, 448, 530, 56, INK, rx=28)
    s.text(895, 482, "A pass is the scenario gate for one commit", size=16, weight="700", fill="#FFFFFF",
           anchor="middle")
    s.text(895, 530, "It is not proof that an app works on a device.", size=14, fill=MUTED, anchor="middle")

    footer(s, "Bloem's decisions never edit Silo's catalogs. When Silo changes one, a person reviews the "
              "decisions that depend on it.", y=588, fill=INK)
    return s


PICTURES = [("how-bloem-runs", how_bloem_runs)]
