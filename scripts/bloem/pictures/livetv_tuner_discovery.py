"""Pictures for docs/livetv-tuner-discovery.md."""
from xml.sax.saxutils import escape

from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

LIVE = {"fill": "#FDF2F8", "stroke": "#DB2777", "label": "#9D174D"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}
MONO = "'SFMono-Regular', Menlo, Consolas, 'Liberation Mono', monospace"


def _mono(s, x, y, txt, size=13.5, fill=INK):
    s.add(f'<text x="{x}" y="{y}" font-family="{MONO}" font-size="{size}" fill="{fill}">{escape(txt)}</text>')


def two_ways():
    s = Svg(1200, 760, "Two ways Bloem finds a tuner",
            "Left: Search my network. Bloem sends a broadcast on UDP port 65001, and HDHomeRun tuners and Dispatcharr "
            "answer; each answer is checked over HTTP and listed, with tuners already added marked Added. Right: "
            "adding a tuner by its address. Bloem tries discover.json at the usual places, including "
            "Dispatcharr's /hdhr/ path and port 9191, and stores the tuner's identity from the answer. Below: an "
            "Xtream provider is added with its own form instead.")
    s.header("Two ways Bloem finds a tuner",
             "Listen for tuners on the local network, or try the address you give. Either way, Bloem checks first.")
    a, g, o = TIERS["account"], TIERS["grey"], TIERS["org"]

    # Left: LAN discovery.
    s.rect(40, 124, 540, 476, "#FFFFFF", "#E5E7EB", rx=16)
    s.text(64, 160, "Search my network", size=20, weight="700")
    s.text(64, 184, "Needs Bloem on the local network itself.", size=14, fill=MUTED)
    box(s, 64, 208, 200, 92, BLOEM, "Bloem", "Broadcasts a question.", size=14)
    for i, (t, b) in enumerate([("HDHomeRun", "on an antenna or cable"), ("Dispatcharr", "pretending to be one")]):
        y = 208 + i * 106
        box(s, 356, y, 200, 92, LIVE, t, b, size=14)
    s.add(f'<path d="M268,246 C300,246 320,254 352,254" fill="none" stroke="{LINE}" stroke-width="2" '
          f'stroke-dasharray="3 5" marker-end="url(#arrow)"/>')
    s.add(f'<path d="M268,262 C300,262 320,354 352,354" fill="none" stroke="{LINE}" stroke-width="2" '
          f'stroke-dasharray="3 5" marker-end="url(#arrow)"/>')
    s.text(196, 340, "UDP port 65001", size=13.5, weight="600", fill=SOFT)
    steps = ["Each answer is checked over HTTP.", "Found tuners are listed; ones already added say Added.",
             "You press Add."]
    y = 440
    for i, t in enumerate(steps):
        s.number(80, y - 5, i + 1, a["stroke"], r=12)
        s.text(102, y, t, size=14.5, fill=INK)
        y += 40
    s.text(64, 574, "In Docker, a bridge network usually can't broadcast to the local network.", size=13.5, fill=SOFT)

    # Right: probe URL.
    s.rect(620, 124, 540, 476, "#FFFFFF", "#E5E7EB", rx=16)
    s.text(644, 160, "Tuner address", size=20, weight="700")
    s.text(644, 184, "You give a host or base address; Bloem tries, in order:", size=14, fill=MUTED)
    tries = ["{base}/hdhr/discover.json", "{base}/discover.json", "http://{host}:9191/hdhr/discover.json",
             "https://{host}:9191/hdhr/discover.json"]
    y = 214
    for t in tries:
        s.rect(644, y, 492, 40, g["fill"], g["stroke"], rx=8, sw=1)
        _mono(s, 660, y + 26, t, size=14)
        y += 50
    para(s, 644, y + 16, "An address ending in /discover.json is used as given; one ending in /hdhr gets "
         "/discover.json added. The port 9191 tries are skipped when you already gave 9191.", 492, size=14,
         gap=19)
    yes_mark(s, 656, 514, r=11)
    para(s, 676, 519, "The first answer gives the tuner's identity. You never type a device ID.", 460, size=14.5,
         fill=INK, gap=20)

    s.rect(40, 624, 1120, 96, o["fill"], o["stroke"], rx=14, sw=1.5)
    s.text(64, 658, "An Xtream provider is different", size=17, weight="700")
    para(s, 64, 686, "It is a service on the internet, added with its own form and a username and password. It needs "
         "outgoing HTTP(S), not discovery or host networking.", 1060, size=14.5, fill=INK, gap=20)
    return s


def docker_networks():
    s = Svg(1200, 450, "Discovery from Docker",
            "Three cases. In Docker's usual bridge network on Linux, the broadcast doesn't reach the local network, "
            "so Search my network finds nothing; the Live TV override puts Bloem on the host network, where it listens "
            "on port 8080. On Docker Desktop for Mac or Windows, host networking doesn't reach the real network, so "
            "type the tuner address instead. Run from source on the host, discovery just works.")
    s.header("Discovery from Docker",
             "A tuner address always works. Search my network needs Bloem on the real local network.")
    st, a, w = TIERS["stop"], TIERS["account"], TIERS["warn"]
    cards = [
        (st, "Docker, bridge network", "Bloem's usual Compose setup. The broadcast stays inside Docker, so "
         "Search my network finds nothing.", "Type the tuner address, or use the Live TV override."),
        (a, "Linux, with the Live TV override", "docker-compose.livetv.yml puts Bloem on the host network. It "
         "listens on port 8080, not 8090.", "Point apps and any proxy at port 8080."),
        (w, "Docker Desktop, Mac or Windows", "Host networking there doesn't reach your real network.",
         "Type the tuner address, or run Bloem from source."),
    ]
    for i, (c, t, b, do) in enumerate(cards):
        x = 40 + i * 380
        s.rect(x, 124, 360, 230, c["fill"], c["stroke"], rx=14, sw=1.6)
        s.text(x + 20, 158, t, size=17, weight="700")
        para(s, x + 20, 188, b, 320, size=14.5, gap=20)
        s.rect(x + 20, 266, 320, 68, "#FFFFFF", c["stroke"], rx=10, sw=1.2)
        para(s, x + 36, 294, do, 290, size=14.5, fill=INK, gap=20)
    footer(s, "From source on the host, or on bare metal, discovery uses the host's own network and needs nothing "
              "special. Never put a tuner's discover or stream address on the internet.", y=410, fill=INK)
    return s


PICTURES = [("two-ways", two_ways), ("docker-networks", docker_networks)]
