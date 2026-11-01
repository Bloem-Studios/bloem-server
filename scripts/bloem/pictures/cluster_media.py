"""Pictures for docs/architecture/bloem-cluster-media.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, para


def forwarding():
    s = Svg(1200, 576, "A Live TV request on the wrong server",
            "The app's request goes through the load balancer to API server B. Server B checks the sign-in, the "
            "Live TV permission, the account and the profile, then passes the request on once to API server A, "
            "which opened the channel and runs its encoder. A checks again and answers through B. If A is gone, "
            "B answers 503 with Retry-After 2 and the app opens a new Live TV session.")
    s.header("A Live TV request on the wrong server",
             "The server that opened the channel runs its encoder. Any other server passes requests on to it, once.")
    o, a, p, st, g = TIERS["org"], TIERS["account"], TIERS["platform"], TIERS["stop"], TIERS["grey"]

    box(s, 40, 160, 200, 112, a, "The app", "Asks for the next part of the channel.", size=14.5)
    s.arrow(244, 216, 296, 216)
    box(s, 300, 160, 200, 112, g, "Load balancer", "Sends it to any API server.", size=14.5)
    s.arrow(504, 216, 556, 216)

    s.rect(560, 128, 260, 176, p["fill"], p["stroke"], rx=16, sw=1.8)
    s.text(580, 162, "API server B", size=17, weight="700")
    para(s, 580, 190, "Checks the sign-in, the Live TV permission, the account and the profile.", 220, size=14,
         gap=19)
    s.text(580, 284, "Not the owner: passes it on.", size=14, weight="600", fill=p["label"])

    s.arrow(824, 216, 876, 216)
    s.text(850, 204, "once", size=13, fill=SOFT, anchor="middle")
    s.rect(880, 128, 280, 176, p["fill"], p["stroke"], rx=16, sw=1.8)
    s.text(900, 162, "API server A", size=17, weight="700")
    para(s, 900, 190, "Opened the channel and runs its encoder. Checks again, then answers through B.", 240,
         size=14, gap=19)
    s.text(900, 284, "Its address: its own NODE_URL", size=14, weight="600", fill=p["label"])

    # What happens if A is gone.
    s.rect(560, 344, 600, 128, st["fill"], st["stroke"], rx=14, sw=1.5)
    s.text(582, 378, "If server A is gone", size=16, weight="700", fill=st["label"])
    para(s, 582, 406, "B answers 503, “try again in 2 seconds”. Bloem never starts a second encoder: the app "
         "opens a new Live TV session, and the clean-up releases the dead server's tuner connection.", 556,
         size=14.5, fill=INK, gap=20)
    s.path("M 690 308 L 690 340")

    s.rect(40, 344, 460, 128, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(62, 378, "Good to know", size=16, weight="700")
    para(s, 62, 406, "A request is passed on at most once. Passing on is routing, not a backup encoder.", 420,
         size=14.5, fill=INK, gap=20)

    footer(s, "Each API server sets NODE_URL to its own address that the other servers can reach, never the "
              "shared load-balanced one.", y=530)
    return s


def stop():
    s = Svg(1200, 440, "Stopping playback frees a screen at once",
            "Three steps: the viewer presses stop, and only the profile that started the video may stop it; the stop "
            "is written to the database and the screen it held is freed in the same step, on every server; within "
            "about fifteen seconds the server that was streaming notices the stop and tidies up.")
    s.header("Stopping playback frees a screen at once",
             "The screen is free the moment the stop is saved. The tidying up follows within seconds.")
    a, o, pl, p = TIERS["account"], TIERS["org"], TIERS["plan"], TIERS["platform"]
    steps = [
        (a, "The viewer presses stop", "Only the profile that started the video may stop it. Another profile on "
         "the account is refused."),
        (o, "The stop is saved", "Written to the database. The screen it held is freed in the same step, on "
         "every server."),
        (p, "The server tidies up", "Within about 15 seconds the server that was streaming notices, stops the "
         "stream and removes its files."),
    ]
    x0, y0, cw, ch, gap = 40, 128, 352, 176, 32
    for i, (c, t, b) in enumerate(steps):
        x = x0 + i * (cw + gap)
        s.rect(x, y0, cw, ch, c["fill"], c["stroke"], rx=14)
        s.number(x + 28, y0 + 30, i + 1, c["stroke"])
        s.text(x + 18, y0 + 78, t, size=18, weight="700")
        para(s, x + 18, y0 + 108, b, cw - 36, size=14.5, gap=20)
        if i < len(steps) - 1:
            s.arrow(x + cw + 4, y0 + ch / 2, x + cw + gap - 4, y0 + ch / 2)
    s.text(x0 + cw + gap / 2 + cw / 2, y0 + ch + 34, "Screen free here", size=14, weight="600",
           fill=o["label"], anchor="middle")
    footer(s, "If the database cannot be reached, Bloem waits rather than guessing that a session has stopped.",
           y=400)
    return s


PICTURES = [("forwarding", forwarding), ("stop", stop)]
