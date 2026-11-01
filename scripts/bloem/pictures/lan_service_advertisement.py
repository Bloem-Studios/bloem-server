"""Pictures for docs/specs/lan-service-advertisement.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, para, yes_mark


def finding_a_server():
    s = Svg(1200, 680, "Finding a Bloem server on a home network",
            "Three steps. An administrator switches the announcement on and restarts the server. The server "
            "announces _bloem._tcp on the local network with a small card of facts: its ID, name, address "
            "scheme and API versions. A Bloem app on the same network sees the card, then asks the server's "
            "identity route, which alone decides whether the app connects. Silo's own _silo._tcp announcement "
            "is separate and has its own switch.")
    s.header("Finding a Bloem server on a home network",
             "An optional hint for Bloem's apps. The server's identity route still decides.")
    p, a, g, w = TIERS["platform"], TIERS["account"], TIERS["grey"], TIERS["warn"]

    box(s, 40, 132, 340, 134, g, "1. Switch it on", "An administrator sets lan.advertisement_enabled and "
        "restarts the server. It is off by default and has no switch in the web app.", size=14)
    s.arrow(384, 199, 426, 199)
    box(s, 430, 132, 340, 134, p, "2. The server announces", "On the local network, as _bloem._tcp on the "
        "port the API listens on, with a small card of facts.", size=14)
    s.arrow(774, 199, 816, 199)
    box(s, 820, 132, 340, 134, a, "3. A Bloem app finds it", "It reads the card, then asks the server who it "
        "is before offering to connect.", size=14)

    # The TXT card.
    s.rect(430, 304, 340, 250, "#FFFFFF", p["stroke"], rx=14, sw=1.6)
    s.text(450, 336, "The card (TXT record)", size=16, weight="700")
    rows = [("txtvers", "1"), ("id", "the server's ID"), ("name", "its display name"), ("scheme", "http"),
            ("api", "1"), ("bloem", "v1")]
    y = 368
    for key, value in rows:
        s.text(450, y, key, size=14, weight="700", fill=p["label"])
        s.text(560, y, value, size=14, fill=INK)
        y += 30

    # Identity check.
    s.rect(820, 304, 340, 250, a["fill"], a["stroke"], rx=14, sw=1.6)
    s.text(840, 336, "The identity check decides", size=16, weight="700")
    para(s, 840, 364, "The app asks /api/bloem/v1/server/identity at the address it found. Only that answer "
         "decides whether to connect.", 300, size=14, fill=INK, gap=20)
    para(s, 840, 454, "The card can only make an app skip a server, for example one that speaks no Bloem "
         "API the app knows. It never makes a server trusted.", 300, size=14, gap=20)
    s.arrow(774, 429, 816, 429)

    # Silo's own.
    s.rect(40, 304, 340, 250, "#F9FAFB", "#E5E7EB", rx=14)
    s.text(60, 336, "Not the same as Silo's", size=16, weight="700")
    para(s, 60, 364, "Silo announces _silo._tcp with its own switch, Show on the local network "
         "(server.lan_discovery). That switch does not publish _bloem._tcp, and this setting does not "
         "publish _silo._tcp.", 300, size=14, gap=20)

    footer(s, "Where the network cannot carry it (a Docker bridge network, most containers), the server logs "
              "one line and stays quiet. Apps then fall back to typing the address.", y=640, fill=INK)
    return s


PICTURES = [("finding-a-server", finding_a_server)]
