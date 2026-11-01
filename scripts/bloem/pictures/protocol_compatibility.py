"""Pictures for docs/architecture/bloem-compatibility-removal.md (protocol compatibility)."""
from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}


def removed_and_restored():
    s = Svg(1200, 590, "Removed, then restored as Silo ships it",
            "A timeline. Before 13 October 2026 Bloem ran Jellyfin and Audiobookshelf compatibility with additions "
            "of its own: a gateway on the public port, companion-app enrollment and Live TV through Jellyfin. On 13 "
            "October Bloem removed both protocols, which needed 852 seams. On 18 October it brought back Silo's own "
            "servers, unchanged, on their own ports 8096 and 13378, and the seam list fell to 388. Bloem's own "
            "additions stayed out.")
    s.header("Removed, then restored as Silo ships it",
             "Bloem now runs Silo's Jellyfin and Audiobookshelf servers unchanged, on their own ports.")
    g, st, a = TIERS["grey"], TIERS["stop"], TIERS["account"]

    # Timeline line.
    s.add(f'<line x1="80" y1="170" x2="1120" y2="170" stroke="{LINE}" stroke-width="3"/>')
    marks = [(200, "Before 13 October", g), (600, "13 October 2026", st), (1000, "18 October 2026", a)]
    for x, label, c in marks:
        s.add(f'<circle cx="{x}" cy="170" r="11" fill="{c["stroke"]}"/>')
        s.text(x, 146, label, size=16, weight="700", anchor="middle")

    box(s, 40, 204, 340, 180, g, "Bloem's own version",
        "Silo's compatibility code, plus three Bloem additions: a gateway that served both protocols on the public "
        "port, enrollment for companion apps, and Live TV through Jellyfin apps.", size=14.5)
    box(s, 430, 204, 340, 180, st, "Both protocols removed",
        "Bloem took out Jellyfin and Audiobookshelf compatibility. Silo's tables and settings stayed in the "
        "database. Deleting Silo's code needed 852 seams, and every merge fought them.", size=14.5)
    box(s, 820, 204, 340, 180, a, "Silo's servers back",
        "Silo's own Jellyfin and Audiobookshelf servers, exactly as Silo ships them, each on its own port. Bloem "
        "adds only the organization wiring. The seam list fell to 388.", size=14.5)
    s.arrow(384, 294, 426, 294)
    s.arrow(774, 294, 816, 294)

    s.text(40, 436, "Stayed out after 18 October", size=17, weight="700")
    x = 40
    for t in ["The gateway on the public port", "Companion-app enrollment", "Live TV through Jellyfin apps"]:
        no_mark(s, x + 12, 470, r=11)
        s.text(x + 32, 475, t, size=15, fill=INK)
        x += 370
    footer(s, "Jellyfin apps use port 8096 and Audiobookshelf apps port 13378 (JF_PORT and ABS_PORT move them). "
              "Neither is served on the public port.", y=546, fill=INK)
    return s


def organization_wiring():
    s = Svg(1200, 720, "How a Jellyfin app stays inside its organization",
            "A Jellyfin or Audiobookshelf app connects to Silo's compatibility server on its own port. Silo's "
            "server is unchanged; where the Bloem binary builds it, Bloem adds four things: only the "
            "organization's libraries, downloads through Bloem's download rules for the profile, no files from "
            "storage sources for Jellyfin, and sign-outs that also end compatibility sessions, including those of "
            "profiles an organization removes.")
    s.header("How a Jellyfin app stays inside its organization",
             "Silo's compatibility servers run unchanged. Bloem adds its rules where the server is built.")
    o, a, f, st = TIERS["org"], TIERS["account"], TIERS["profile"], TIERS["stop"]

    box(s, 40, 132, 300, 120, o, "Jellyfin app", "Infuse, Findroid, Swiftfin and others, on port 8096.", size=14.5)
    box(s, 40, 272, 300, 120, o, "Audiobookshelf app", "For listening to audiobooks, on port 13378.", size=14.5)
    s.arrow(344, 192, 396, 250)
    s.arrow(344, 332, 396, 280)
    box(s, 400, 196, 300, 132, SILO, "Silo's compatibility server",
        "Exactly as Silo ships it. Bloem differs from Silo in one of its test files only.", size=14.5)
    s.arrow(704, 262, 756, 262)

    s.rect(760, 124, 400, 512, BLOEM["fill"], BLOEM["stroke"], rx=16, sw=2)
    s.text(784, 160, "Bloem's wiring, where it is built", size=18, weight="700")
    rules = [
        ("Only the organization's libraries", "A session sees its own organization's libraries, nothing more."),
        ("Downloads by Bloem's rules", "A Jellyfin download follows the profile's download rules; a session "
         "with no organization is refused."),
        ("No storage-source files", "Jellyfin never sees files that live in a storage source."),
        ("Sign-outs reach it", "Signing an account out ends its compatibility sessions on every server, and "
         "removing a profile ends that profile's."),
    ]
    y = 184
    for t, b in rules:
        yes_mark(s, 790, y + 12, r=10)
        s.text(810, y + 17, t, size=15.5, weight="700")
        y = para(s, 810, y + 42, b, 330, size=14, gap=19) + 18

    s.rect(40, 440, 660, 196, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(64, 474, "Not here", size=17, weight="700")
    for i, t in enumerate(["No Live TV: Bloem's Live TV is only in the web app and Bloem's apps.",
                           "Not on the public port: each protocol has its own.",
                           "No gateway or companion-app enrollment."]):
        no_mark(s, 76, 508 + i * 40, r=10)
        s.text(96, 513 + i * 40, t, size=14.5, fill=INK)

    footer(s, "A test fails if any viewer resolver the server builds, including the compatibility servers', skips "
              "Bloem's organization check.", y=684, fill=INK)
    return s


PICTURES = [("removed-and-restored", removed_and_restored), ("organization-wiring", organization_wiring)]
