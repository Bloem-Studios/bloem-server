"""Pictures for docs/architecture/delivery-boundary-enforcement.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, para, yes_mark


def exits():
    s = Svg(1200, 680, "Access is checked again at every exit",
            "In the middle, one question: which libraries may this account reach right now? It is worked out "
            "fresh from the database: the organization's libraries, narrowed by the plan and the profile. Four "
            "exits ask it before anything leaves the server: video and subtitles, managed downloads, the "
            "standalone proxy, and notifications. A player using a stream token gets the same check.")
    s.header("Access is checked again at every exit",
             "Before a segment, subtitle, link or notification leaves, Bloem asks the same question again.")
    o, a, f, pl, p, g = (TIERS["org"], TIERS["account"], TIERS["profile"], TIERS["plan"], TIERS["platform"],
                         TIERS["grey"])

    # The question in the middle.
    cx, cy, cw, ch = 400, 236, 400, 250
    s.rect(cx, cy, cw, ch, "#FFFFFF", o["stroke"], rx=18, sw=2)
    s.text(cx + cw / 2, cy + 38, "Which libraries may this", size=18, weight="700", anchor="middle")
    s.text(cx + cw / 2, cy + 62, "account reach right now?", size=18, weight="700", anchor="middle")
    layers = [(o, "The organization's libraries"), (pl, "narrowed by the plan"), (f, "and by the profile")]
    for i, (c, t) in enumerate(layers):
        w = 320 - i * 40
        x = cx + (cw - w) / 2
        y = cy + 88 + i * 46
        s.rect(x, y, w, 36, c["fill"], c["stroke"], rx=10, sw=1.4)
        s.text(cx + cw / 2, y + 24, t, size=14.5, weight="600", fill=c["label"], anchor="middle")
    s.text(cx + cw / 2, cy + 236, "Fresh from the database, every time.", size=13.5, fill=MUTED, anchor="middle")

    exits = [
        (40, 128, "Video and subtitles", "File bytes, subtitles, fonts, HLS playlists and segments, restart "
         "recipes."),
        (840, 128, "Managed downloads", "The subtitles that go with a download."),
        (40, 470, "Standalone proxy", "Signed recipes, media grants and download links. Never stored files."),
        (840, 470, "Notifications", "Choosing who gets one, and sending one that waited in the queue."),
    ]
    for x, y, t, b in exits:
        box(s, x, y, 320, 140, a, t, b, size=14.5)
        yes_mark(s, x + 296, y + 26, r=11)
    s.path("M 364 198 L 396 260")
    s.path("M 836 198 L 804 260")
    s.path("M 364 540 L 396 470")
    s.path("M 836 540 L 804 470")

    s.rect(400, 128, 400, 80, p["fill"], p["stroke"], rx=14, sw=1.5)
    s.text(420, 158, "A player with a stream token", size=15, weight="700")
    s.text(420, 182, "Bloem reads the account and profile from it.", size=13.5, fill=MUTED)
    s.arrow(600, 210, 600, 232)

    footer(s, "No answer means no, never “no limit”. Not allowed answers 404; cannot check right now answers 503.",
           y=646)
    return s


PICTURES = [("exits", exits)]
