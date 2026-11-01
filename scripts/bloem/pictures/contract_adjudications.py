"""Pictures for docs/architecture/bloem-contract-adjudications.md."""
from kit import INK, MUTED, TIERS, Svg, box, footer, para

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}


def layers():
    s = Svg(1200, 680, "How Bloem tests against Silo's contract",
            "Silo's frozen scenario files stay exactly as Silo wrote them. Bloem's own rows are added to the end of "
            "the matching catalog when it loads. Then 49 reviewed decisions replace the answers Bloem deliberately "
            "gives differently, and the scenario gate runs every case. If a pinned original changes, loading stops "
            "until someone reviews it again.")
    s.header("How Bloem tests against Silo's contract",
             "Silo's test files stay Silo's. Bloem adds its rows and its reviewed differences when they load.")
    g, a, st = TIERS["grey"], TIERS["account"], TIERS["stop"]

    box(s, 40, 130, 300, 150, SILO, "Silo's scenario files",
        "Requests and the answers Silo expects. Frozen, byte for byte Silo's.", size=14)
    box(s, 40, 300, 300, 150, BLOEM, "Bloem's own rows",
        "For routes Bloem adds to a Silo group: profile sign-in, remote control and household commands.", size=14)
    s.path("M344,205 L392,205 L392,280 L420,280")
    s.path("M344,375 L392,375 L392,300 L420,300")
    box(s, 424, 220, 220, 140, g, "One catalog", "Bloem's rows go at the end of the Silo catalog they extend.",
        size=14)
    s.arrow(648, 290, 696, 290)
    box(s, 700, 200, 220, 180, BLOEM, "49 reviewed decisions",
        "Each replaces one answer Bloem gives on purpose, with its reason. Six Silo files are pinned by checksum.",
        size=14)
    s.arrow(924, 290, 972, 290)
    box(s, 976, 220, 184, 140, a, "Scenario gate", "Runs every case, 86 transport leaves in the focused packet.",
        size=14)

    s.path("M810,384 L810,452")
    s.rect(640, 456, 520, 100, st["fill"], st["stroke"], rx=14, sw=1.8)
    s.text(660, 488, "A pinned original changed?", size=16, weight="700", fill=st["label"])
    para(s, 660, 514, "Loading stops. The decision is reviewed again before the gate can pass.", 480, size=14,
         gap=19, fill=INK)

    s.rect(40, 470, 560, 86, "#F9FAFB", "#E5E7EB", rx=14)
    para(s, 60, 500, "Only expected answers change. Requests, accounts, fixtures and the order of steps stay "
         "exactly as Silo wrote them.", 520, size=14, gap=20, fill=INK)
    footer(s, "A decision never loosens a security rule to make a test pass: no secret is shown again, no hidden "
              "profile is listed.", y=626, fill=INK)
    return s


PICTURES = [("layers", layers)]
