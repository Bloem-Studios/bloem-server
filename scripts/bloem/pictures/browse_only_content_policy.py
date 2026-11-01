"""Pictures for docs/architecture/bloem-browse-only-content-policy.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, footer, no_mark, para, yes_mark


def every_door():
    s = Svg(1200, 672, "Browse-only: checked at every door",
            "A browse-only account can open the catalog: titles, descriptions, seasons and episodes, artwork and "
            "trailers hosted elsewhere. Six doors that deliver content (video and its stream links, the ebook reader, "
            "theme songs, thumbnails while scrubbing, Live TV and plugins) each check playback again on every "
            "request, and refuse.")
    s.header("Browse-only: checked at every door",
             "Hiding the play button is not enough. Every place content leaves the server checks again.")
    a, p, st, g = TIERS["account"], TIERS["plan"], TIERS["stop"], TIERS["grey"]

    # The account.
    s.rect(40, 260, 250, 150, p["fill"], p["stroke"], rx=16, sw=1.8)
    s.text(165, 310, "Browse-only", size=19, weight="700", anchor="middle")
    s.text(165, 336, "account", size=19, weight="700", anchor="middle")
    s.text(165, 368, "Playback is off", size=14, fill=MUTED, anchor="middle")
    s.path("M294,310 C330,310 330,180 376,180")
    s.path("M294,360 C330,360 330,430 376,430")

    # The catalog.
    s.rect(380, 124, 780, 112, a["fill"], a["stroke"], rx=14, sw=1.6)
    yes_mark(s, 412, 162, r=14)
    s.text(438, 168, "The catalog is open", size=18, weight="700")
    para(s, 438, 198, "Titles, descriptions, seasons and episodes, artwork, and trailers hosted elsewhere. "
         "Nothing is taken out of the catalog.", 690, size=14.5, gap=20)

    # The doors.
    s.rect(380, 262, 780, 332, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(404, 296, "Doors that deliver content", size=18, weight="700")
    doors = [
        ("Video", "Starting a stream, and its stream links"),
        ("Ebook reader", "Opening a book, whether stored here or elsewhere"),
        ("Theme songs", "Playing a title's theme music"),
        ("Thumbnails", "The pictures shown while scrubbing"),
        ("Live TV", "Starting a channel and every piece of it"),
        ("Plugins", "Anything a plugin serves, apart from public sign-in pictures"),
    ]
    for i, (t, b) in enumerate(doors):
        x, y = 400 + (i % 2) * 380, 316 + (i // 2) * 90
        s.rect(x, y, 360, 80, st["fill"], st["stroke"], rx=12, sw=1.3)
        no_mark(s, x + 28, y + 40, r=12)
        s.text(x + 52, y + 30, t, size=16, weight="700")
        para(s, x + 52, y + 52, b, 290, size=13, gap=16)
    footer(s, "Each door reads the account's settings as they are now, on every request. Switching playback off "
              "takes effect on the next request, even in an app that is already open.", y=642, size=14.5)
    return s


def free_play():
    s = Svg(1200, 660, "Free play: how the seconds are counted",
            "Three timelines. While watching, each piece of video charges the seconds since the piece before. After "
            "a ten-minute pause, the next piece charges at most 20 seconds. With two tabs on one allowance, pieces "
            "from both tabs are charged once by the clock. A bar shows an allowance running down to zero, when the "
            "video stops.")
    s.header("Free play: how the seconds are counted",
             "Each piece of video charges the time since the piece before, never more than 20 seconds.")
    a, w, st, o, g = TIERS["account"], TIERS["warn"], TIERS["stop"], TIERS["org"], TIERS["grey"]

    panels = [
        ("Watching", "Pieces arrive every 6 seconds; each charges 6.", "real time"),
        ("Paused for ten minutes", "The first piece after the pause charges 20, not 600.", "at most 20 seconds"),
        ("Two tabs, one allowance", "Pieces from both tabs are charged by the clock, once.", "paid once"),
    ]
    pw, gap, y0 = 360, 20, 124
    for i, (title, body, tag) in enumerate(panels):
        x = 40 + i * (pw + gap)
        s.rect(x, y0, pw, 250, "#FFFFFF", "#E5E7EB", rx=14)
        s.text(x + 20, y0 + 34, title, size=17, weight="700")
        para(s, x + 20, y0 + 60, body, pw - 40, size=14, gap=19)
        # Timeline.
        ty = y0 + 160
        s.add(f'<line x1="{x + 20}" y1="{ty}" x2="{x + pw - 20}" y2="{ty}" stroke="#D1D5DB" stroke-width="2"/>')
        s.text(x + pw - 20, ty + 26, "time", size=12, fill=SOFT, anchor="end")
        if i == 0:
            for k in range(7):
                px = x + 36 + k * 44
                s.rect(px, ty - 22, 26, 22, a["fill"], a["stroke"], rx=4, sw=1.2)
                if k:
                    s.text(px + 13, ty - 30, "+6", size=12, weight="600", fill=a["label"], anchor="middle")
        elif i == 1:
            for k, px in enumerate([x + 36, x + 80]):
                s.rect(px, ty - 22, 26, 22, a["fill"], a["stroke"], rx=4, sw=1.2)
            s.text(x + 106, ty - 30, "+6", size=12, weight="600", fill=a["label"], anchor="middle")
            s.rect(x + 120, ty - 20, 140, 18, "#F9FAFB", "#D1D5DB", rx=4, sw=1, dash="4 4")
            s.text(x + 190, ty - 7, "paused", size=12, fill=SOFT, anchor="middle")
            for px in [x + 274, x + 306]:
                s.rect(px, ty - 22, 26, 22, a["fill"], a["stroke"], rx=4, sw=1.2)
            s.text(x + 287, ty - 30, "+20", size=12, weight="700", fill=w["label"], anchor="middle")
            s.text(x + 319, ty - 30, "+6", size=12, weight="600", fill=a["label"], anchor="middle")
        else:
            for k in range(7):
                px = x + 36 + k * 44
                s.rect(px, ty - 22, 26, 22, a["fill"] if k % 2 == 0 else o["fill"],
                       a["stroke"] if k % 2 == 0 else o["stroke"], rx=4, sw=1.2)
                if k:
                    s.text(px + 13, ty - 30, "+3", size=12, weight="600", fill=a["label"], anchor="middle")
            s.text(x + 36, ty + 26, "tab 1 and tab 2", size=12, fill=SOFT)
        s.badge(x + 20, y0 + 204, tag, w if i == 1 else a, size=12.5)

    # The allowance.
    s.text(40, 418, "One allowance, running down", size=18, weight="700")
    bx, bw = 40, 1120
    s.rect(bx, 440, bw, 40, "#F3F4F6", "#D1D5DB", rx=10, sw=1.2)
    s.rect(bx, 440, bw * 0.6, 40, a["fill"], a["stroke"], rx=10, sw=1.2)
    s.text(bx + 18, 466, "Used: 18 minutes", size=14.5, weight="700", fill=a["label"])
    s.text(bx + bw - 18, 466, "Left: 12 minutes of 30", size=14.5, weight="700", fill=INK, anchor="end")
    steps = [
        (a, "Set once", "The storefront signs an allowance of one minute to four hours. A later link cannot "
         "add time."),
        (w, "Shown to the visitor", "The web app shows what is left and checks every 30 seconds."),
        (st, "At zero", "The video stops and the dialog says the free time has ended."),
    ]
    for i, (c, t, b) in enumerate(steps):
        x = 40 + i * 380
        s.rect(x, 504, 360, 104, "#FFFFFF", "#E5E7EB", rx=12)
        s.rect(x, 504, 6, 104, c["stroke"], rx=3)
        s.text(x + 22, 534, t, size=16, weight="700")
        para(s, x + 22, 560, b, 318, size=13.5, gap=18)
    footer(s, "Free play is off until a platform administrator switches it on. By default 20 allowances can stream "
              "at once.", y=640, size=13.5, fill=MUTED)
    return s


PICTURES = [("every-door", every_door), ("free-play", free_play)]
