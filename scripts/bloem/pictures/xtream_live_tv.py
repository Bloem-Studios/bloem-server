"""Pictures for docs/architecture/xtream-live-tv.md."""
from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

LIVE = {"fill": "#FDF2F8", "stroke": "#DB2777", "label": "#9D174D"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}
MONO = "'SFMono-Regular', Menlo, Consolas, 'Liberation Mono', monospace"


def boundary():
    s = Svg(1200, 760, "What Bloem asks an Xtream provider for",
            "Bloem keeps the provider's username and password encrypted and makes only four fixed requests: sign in "
            "with player_api.php, list live channels with get_live_streams, fetch the guide with xmltv.php, and open "
            "one live .ts stream. It refuses redirects, films and series, the provider's own stream and artwork "
            "addresses, and local or cloud-metadata addresses. Inside Bloem, the video encoder reads a pipe, never "
            "an address with the password in it.")
    s.header("What Bloem asks an Xtream provider for",
             "Four fixed requests, live channels only. The password never leaves the server's own requests.")
    a, st, g, p = TIERS["account"], TIERS["stop"], TIERS["grey"], TIERS["platform"]

    # Bloem.
    s.rect(40, 124, 360, 420, p["fill"], p["stroke"], rx=16, sw=2)
    s.text(220, 160, "Bloem server", size=20, weight="700", anchor="middle")
    inner = [
        ("Encrypted account", "Username and password, encrypted together with the server's secret key."),
        ("Channels", "Each keeps a reference only the server can use."),
        ("Video encoder", "Reads a pipe that Bloem feeds, never the provider's address."),
    ]
    y = 180
    for t, b in inner:
        s.rect(60, y, 320, 108, "#FFFFFF", "#FDBA74", rx=12, sw=1.4)
        s.text(78, y + 30, t, size=16, weight="700")
        para(s, 78, y + 56, b, 286, size=14, gap=19)
        y += 118

    # Requests.
    s.text(440, 146, "The only requests", size=17, weight="700")
    reqs = [("player_api.php", "sign in"), ("get_live_streams", "the live channel list"),
            ("xmltv.php", "the programme guide"), ("a live .ts stream", "one channel, as it airs")]
    y = 162
    for code, label in reqs:
        s.rect(440, y, 320, 70, a["fill"], a["stroke"], rx=12, sw=1.5)
        s.add(f'<text x="460" y="{y + 30}" font-family="{MONO}" font-size="15" font-weight="600" fill="{INK}">'
              f'{code}</text>')
        s.text(460, y + 54, label, size=14, fill=MUTED)
        s.arrow(404, y + 35, 436, y + 35)
        s.arrow(764, y + 35, 796, y + 35)
        y += 82

    # Provider.
    s.rect(800, 124, 360, 420, LIVE["fill"], LIVE["stroke"], rx=16, sw=2)
    s.text(980, 160, "Xtream provider", size=20, weight="700", anchor="middle")
    para(s, 822, 196, "A paid IPTV service on the internet. It sees the username and password in Bloem's "
         "requests, as it must. Prefer HTTPS: over plain HTTP they travel unencrypted.", 316, size=14.5,
         fill=INK, gap=20)
    s.text(822, 330, "Bloem takes from it:", size=15, weight="700")
    yes_mark(s, 834, 360, r=10)
    s.text(852, 365, "live channels", size=14.5, fill=INK)
    yes_mark(s, 834, 394, r=10)
    s.text(852, 399, "its guide", size=14.5, fill=INK)
    no_mark(s, 834, 428, r=10)
    s.text(852, 433, "films and series", size=14.5, fill=INK)
    no_mark(s, 834, 462, r=10)
    s.text(852, 467, "its stream and artwork addresses", size=14.5, fill=INK)

    s.text(40, 590, "Always refused", size=17, weight="700")
    refused = ["Any redirect, even on the same host", "Local and cloud-metadata addresses",
               "Playlists, text or JSON instead of video"]
    x = 40
    for t in refused:
        s.rect(x, 606, 360, 56, st["fill"], st["stroke"], rx=12, sw=1.4)
        no_mark(s, x + 26, 634, r=10)
        s.text(x + 46, 639, t, size=14.5, fill=INK)
        x += 380
    footer(s, "The password appears in no public answer, stored channel address, log or encoder command.", y=716,
           fill=INK)
    return s


def connections():
    s = Svg(1200, 650, "Sharing a provider's connections",
            "A made-up provider allows two connections at once. Two Bloem servers share them through the database: "
            "one viewer watches on server A, a recording runs on server B. Each physical stream holds a lease that "
            "lasts 60 seconds and is renewed every 15. A third viewer is refused until one ends. A lease that "
            "can't be renewed stops its stream.")
    s.header("Sharing a provider's connections",
             "Every stream from the provider holds a lease in the database, on whichever server opened it.")
    a, st, g = TIERS["account"], TIERS["stop"], TIERS["grey"]

    s.text(40, 140, "This example is made up: a provider that allows two connections at once.", size=14.5,
           fill=MUTED)
    for i, (name, use) in enumerate([("Bloem server A", "A viewer watching a channel"),
                                     ("Bloem server B", "A recording of a programme")]):
        y = 166 + i * 110
        box(s, 40, y, 330, 90, BLOEM, name, use, size=14.5)
        s.arrow(374, y + 45, 446, y + 45)
    s.rect(40, 386, 330, 90, st["fill"], st["stroke"], rx=14, sw=1.8)
    s.text(58, 418, "A third viewer", size=17, weight="700")
    s.text(58, 444, "Refused: both connections are in use.", size=14.5, fill=INK)
    s.add(f'<line x1="374" y1="431" x2="446" y2="431" stroke="{st["stroke"]}" stroke-width="2" '
          f'stroke-dasharray="6 5"/>')
    no_mark(s, 410, 431, r=11)

    s.rect(450, 156, 320, 324, g["fill"], g["stroke"], rx=16, sw=1.6)
    s.text(610, 190, "The database", size=18, weight="700", anchor="middle")
    for i in range(2):
        y = 208 + i * 112
        s.rect(470, y, 280, 96, a["fill"], a["stroke"], rx=12, sw=1.4)
        s.text(490, y + 32, f"Lease {i + 1} of 2", size=16, weight="700")
        s.text(490, y + 58, "Lasts 60 s, renewed every 15 s,", size=14, fill=MUTED)
        s.text(490, y + 78, "with an unguessable ID", size=14, fill=MUTED)
    s.text(610, 456, "No lease free for a third", size=14, weight="600", fill=st["label"], anchor="middle")
    s.arrow(774, 300, 846, 300)
    box(s, 850, 236, 310, 128, LIVE, "The provider", "Two streams open: one for each lease.", size=14.5)

    notes = [
        "Watching, the browser-friendly stream and recordings all share the same connections.",
        "A lease that can't be renewed stops its stream. An expired lease can't come back or end its "
        "successor's.",
        "Bloem counts only its own streams, not ones other apps opened on the same provider account.",
    ]
    y = 516
    for n in notes:
        s.add(f'<circle cx="52" cy="{y - 5}" r="4" fill="{SOFT}"/>')
        y = para(s, 66, y, n, 1080, size=14.5, fill=INK, gap=20) + 10
    footer(s, "If the server holding a stream dies, the viewer opens a new one. Leases limit how many streams "
              "start; they don't move a stream to another server.", y=618, fill=INK)
    return s


PICTURES = [("boundary", boundary), ("connections", connections)]
