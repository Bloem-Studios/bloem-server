"""Pictures for docs/architecture/playback-overlays.md and docs/playback-overlays-api.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

PROMO = {"fill": "#FDF4FF", "stroke": "#C026D3", "label": "#86198F"}


def when_a_card_appears():
    s = Svg(1200, 770, "When a playback card may appear",
            "A playback timeline with chapter marks. Starting playback, seeking across a chapter and resuming "
            "after a pause never show a card. The first time playback runs naturally into a chapter, the card "
            "appears; a second chapter in the same app session shows nothing. Below, the card's display time: "
            "a 650 millisecond fade in, the visible part, and a one second fade out, 5 to 60 seconds in all.")
    s.header("When a playback card may appear",
             "Only when playback runs on its own into a chapter. At most once per title, per profile, per app session.")

    # Timeline.
    x0, x1, y = 60, 1140, 236
    s.rect(x0, y, x1 - x0, 12, "#E5E7EB", rx=6)
    chapters = [(x0, "start"), (300, "chapter 2"), (560, "chapter 3"), (820, "chapter 4"), (1060, "chapter 5")]
    for cx, label in chapters:
        s.add(f'<line x1="{cx}" y1="{y - 8}" x2="{cx}" y2="{y + 20}" stroke="{INK}" stroke-width="2"/>')
        s.text(cx if cx > x0 else cx + 2, y + 44, label, size=13, fill=SOFT, anchor="middle" if cx > x0 else "start")

    events = [
        (x0 + 4, False, "Playback starts", ["Never at the start,", "even on a chapter."]),
        (300, False, "A seek lands here", ["Jumping across a", "chapter does not count."]),
        (560, False, "Resumed after pause", ["Crossing a chapter", "while paused does not."]),
        (820, True, "Runs into chapter 4", ["The first natural", "crossing: the card shows."]),
        (1060, False, "Runs into chapter 5", ["Already shown for this", "title in this session."]),
    ]
    for cx, ok, title, rows in events:
        mx = min(max(cx, 130), 1070)
        if ok:
            yes_mark(s, cx, y - 34, r=14)
        else:
            no_mark(s, cx, y - 34, r=14)
        s.text(mx, 156, title, size=14.5, weight="700", anchor="middle")
        s.lines(mx, 300, rows, size=13, fill=MUTED, anchor="middle", gap=18)
    s.add(f'<path d="M 230 {y - 34} C 250 {y - 70}, 280 {y - 70}, 300 {y - 50}" fill="none" stroke="{SOFT}" '
          f'stroke-width="1.6" stroke-dasharray="4 4" marker-end="url(#arrow)"/>')
    s.text(214, y - 30, "seek", size=12.5, fill=SOFT, anchor="end")

    # Envelope.
    ey = 420
    s.text(60, ey, "How long it stays", size=18, weight="700")
    left, right, top, base = 120, 1040, ey + 50, ey + 170
    s.add(f'<path d="M {left} {base} L {left + 60} {top} L {right - 90} {top} L {right} {base} Z" '
          f'fill="{PROMO["fill"]}" stroke="{PROMO["stroke"]}" stroke-width="2"/>')
    s.add(f'<line x1="{left - 20}" y1="{base}" x2="{right + 40}" y2="{base}" stroke="{INK}" stroke-width="1.5"/>')
    s.text(left + 30, base + 26, "fade in, 650 ms", size=13, fill=PROMO["label"], anchor="middle")
    s.text((left + right) / 2, top + 60, "visible: artwork and text float over the picture", size=15,
           weight="600", fill=PROMO["label"], anchor="middle")
    s.text(right - 45, base + 26, "fade out, 1 s", size=13, fill=PROMO["label"], anchor="middle")
    s.add(f'<line x1="{left}" y1="{base + 46}" x2="{right}" y2="{base + 46}" stroke="{SOFT}" stroke-width="1.5" '
          f'marker-end="url(#arrow)" marker-start="url(#arrow)"/>')
    s.text((left + right) / 2, base + 70, "5 to 60 seconds in all, fades included; 10 seconds unless the campaign "
           "sets another time", size=13.5, fill=INK, anchor="middle")

    footer(s, "It also leaves early when the player's controls open, playback pauses or seeks, the campaign "
              "expires or the server stops listing it. It never pauses the video or takes the sound.",
           y=726, fill=INK)
    return s


def who_calls_what():
    s = Svg(1200, 676, "Who calls what for playback cards",
            "Left: the storefront and the web admin console write campaigns into one registry on the Bloem "
            "server through the native platform routes. Right: the web app reads the cards from Bloem's native "
            "routes; the Android apps' old routes on Silo's v1 API are retired; Bloem's Apple apps and Jellyfin "
            "apps show no playback cards.")
    s.header("Who calls what for playback cards",
             "Two ways to write a campaign, one registry, and the routes a viewer's app reads.")
    p, o, a, g = TIERS["platform"], TIERS["org"], TIERS["account"], TIERS["grey"]

    box(s, 40, 132, 320, 110, g, "The storefront", "Bloem Garden, a separate application, with a platform "
        "administrator's API key.", size=14)
    box(s, 40, 266, 320, 110, g, "The web admin console", "Campaigns & seasonal packs, in the Platform "
        "administrative context.", size=14)
    s.arrow(364, 187, 436, 236)
    s.arrow(364, 321, 436, 268)

    s.rect(440, 132, 320, 400, p["fill"], p["stroke"], rx=18, sw=2)
    s.text(600, 166, "Bloem server", size=19, weight="700", anchor="middle")
    s.text(600, 196, "writes: /api/bloem/v2/admin/platform/promotions", size=11.5, fill=SOFT, anchor="middle")
    s.rect(466, 210, 268, 112, "#FFFFFF", PROMO["stroke"], rx=12, sw=1.6)
    s.text(600, 240, "Campaign registry", size=16, weight="700", anchor="middle")
    para(s, 600, 266, "One store for every surface. A later save overwrites an earlier one.", 236, size=13.5,
         anchor="middle")
    s.rect(466, 338, 268, 140, "#FFFFFF", "#E5E7EB", rx=12)
    s.text(600, 366, "Checks per request", size=15, weight="700", anchor="middle")
    s.lines(600, 392, ["signed in, profile chosen,", "PIN checked if it has one,", "adult profile only,",
                       "audience and dates match"], size=13.5, anchor="middle", gap=19)
    s.text(600, 506, "reads: /api/bloem/v2/promotions", size=11.5, fill=SOFT, anchor="middle")

    box(s, 840, 132, 320, 104, a, "The web app",
        "Native routes: list the cards for the title, save one to the inbox.", size=14)
    s.arrow(764, 214, 836, 190)
    box(s, 840, 252, 320, 104, a, "Android TV and phones",
        "Their old routes on Silo's v1 API are retired; no cards until they move.", size=14)
    s.arrow(764, 300, 836, 304)
    box(s, 840, 372, 320, 72, g, "Bloem's Apple apps", None, size=14)
    s.text(858, 428, "Never ask for playback cards yet.", size=14, fill=MUTED)
    box(s, 840, 460, 320, 72, g, "Jellyfin apps", None, size=14)
    s.text(858, 516, "Have no playback cards.", size=14, fill=MUTED)

    para(s, 40, 580, "The phone apps read the cards from Bloem's old routes on Silo's v1 API, which Silo retired "
         "on 2026-11-11; they now answer 410 Gone. The web app and the storefront call only native routes.", 1120,
         size=14.5, fill=INK)
    footer(s, "An app learns that the server has playback cards from playback_overlays_v1 in "
              "/api/bloem/v2/capabilities.", y=638, fill=MUTED)
    return s


PICTURES = [("when-a-card-appears", when_a_card_appears), ("who-calls-what", who_calls_what)]
