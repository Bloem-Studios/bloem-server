"""Pictures for docs/architecture/bloem-messages-and-promotions.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, footer, para

PROMO = {"fill": "#FDF4FF", "stroke": "#C026D3", "label": "#86198F"}
SEASON = {"fill": "#EFF6FF", "stroke": "#3B82F6", "label": "#1D4ED8"}


def where_viewers_see():
    s = Svg(1200, 668, "Five ways Bloem talks to viewers",
            "Five cards: announcements in the inbox, promotion cards on Home and before playback, playback cards "
            "during a video, seasonal effects on Home and the sign-in page, and the upgrade prompt. Each says "
            "where it appears, who writes it and its main rule.")
    s.header("Five ways Bloem talks to viewers",
             "Silo has the inbox. Bloem adds what goes into it, and a few things around it.")
    w, p, o = TIERS["warn"], TIERS["platform"], TIERS["org"]
    cards = [
        (o, "Announcements", "In the inbox", "An administrator writes a message for everyone, an account role, an "
         "organization, people with a library, or chosen accounts. Information, warning or critical; a critical "
         "one cannot be dismissed. It can expire."),
        (PROMO, "Promotion cards", "On Home, a title's page or before it plays", "A campaign card with artwork "
         "and a button. Home cards are off by default. Children never see them, and “Continue” is "
         "always there before playback."),
        (PROMO, "Playback cards", "Over the video, at a chapter", "A short card that floats over the picture "
         "for 5 to 60 seconds. It never pauses the video or takes the sound. Adults only. The viewer can save "
         "it to their inbox."),
        (TIERS["plan"], "Seasonal effects", "On Home and the sign-in page", "Snow, banners or small pictures "
         "for a season, switched on and off by a yearly calendar. Hidden while a video plays."),
        (w, "Upgrade prompt", "In the inbox, when a stream is refused", "When every screen on the plan is busy, "
         "an offer to upgrade. At most once a day per profile."),
    ]
    pos = [(40, 124), (420, 124), (800, 124), (40, 368), (420, 368)]
    for (x, y), (color, title, where, body) in zip(pos, cards):
        s.rect(x, y, 360, 224 if y == 124 else 260, color["fill"], color["stroke"], rx=16, sw=1.8)
        s.text(x + 20, y + 38, title, size=20, weight="700")
        s.text(x + 20, y + 64, where.upper(), size=11.5, weight="700", fill=color["label"], spacing="0.8")
        para(s, x + 20, y + 96, body, 320, size=14.5, gap=21, fill=INK)
    s.rect(800, 368, 360, 260, "#FFFFFF", "#E5E7EB", rx=16)
    s.text(820, 406, "Who writes them", size=18, weight="700")
    para(s, 820, 436, "A platform administrator, in the admin console. Announcements are under Integrations; "
         "campaigns and seasonal packs under Campaigns & seasonal packs.", 320, size=14, gap=20)
    para(s, 820, 528, "The storefront (Bloem Garden) can write campaigns and seasonal packs too. Bloem stores "
         "them, so they keep working when the storefront is offline.", 320, size=14, gap=20)
    return s


def playback_card():
    s = Svg(1200, 760, "A playback card",
            "A video frame with a card floating in the lower corner, and a timeline under it showing the chapters. "
            "The card appears when playback naturally crosses a chapter and fades away after its set time. The "
            "rules are listed on the right.")
    s.header("A playback card", "A campaign can show one short card while a film or episode plays.")
    # The screen.
    s.rect(40, 124, 680, 384, "#0B1220", rx=14)
    s.add('<defs><linearGradient id="sky" x1="0" y1="0" x2="0" y2="1">'
          '<stop offset="0" stop-color="#1E3A5F"/><stop offset="1" stop-color="#0B1220"/></linearGradient></defs>')
    s.rect(52, 136, 656, 360, "url(#sky)", rx=8)
    s.add('<path d="M52,420 C180,360 300,400 420,370 C540,340 620,380 708,350 L708,496 L52,496 Z" fill="#14243B"/>')
    s.add('<circle cx="600" cy="200" r="34" fill="#F8FAFC" opacity="0.18"/>')
    # The card: artwork and text floating, no panel.
    s.rect(412, 372, 76, 100, PROMO["stroke"], rx=8)
    s.text(450, 428, "ART", size=13, weight="700", fill="#FFFFFF", anchor="middle")
    s.add('<g style="filter: drop-shadow(0 1px 2px rgba(0,0,0,0.6))">')
    s.text(502, 396, "New season, Friday", size=17, weight="700", fill="#FFFFFF")
    s.text(502, 420, "Watch the first episode", size=14, fill="#E5E7EB")
    s.text(502, 440, "a day early.", size=14, fill="#E5E7EB")
    s.add('</g>')
    s.rect(502, 450, 112, 26, "#FFFFFF", rx=13)
    s.text(558, 468, "Save to inbox", size=12.5, weight="700", fill=INK, anchor="middle")

    # Timeline.
    y = 586
    s.text(40, y - 18, "Playback", size=14, weight="700", fill=MUTED)
    s.rect(40, y, 680, 10, "#E5E7EB", rx=5)
    s.rect(40, y, 300, 10, "#9CA3AF", rx=5)
    for cx, label in [(40, "start"), (190, "chapter 2"), (340, "chapter 3"), (520, "chapter 4")]:
        s.add(f'<line x1="{cx}" y1="{y - 6}" x2="{cx}" y2="{y + 16}" stroke="{INK}" stroke-width="2"/>')
        s.text(cx + 4 if cx == 40 else cx, y + 36, label, size=12.5, fill=SOFT, anchor="start" if cx == 40 else "middle")
    s.rect(340, y - 2, 70, 14, PROMO["stroke"], rx=7)
    s.add(f'<path d="M375,{y - 6} L375,{y - 30}" stroke="{PROMO["stroke"]}" stroke-width="2" stroke-dasharray="4 4"/>')
    s.text(384, y - 22, "card shows for 10 s by default (5 to 60)", size=12.5, fill=PROMO["label"])
    para(s, 40, y + 74, "It appears only when playback runs naturally into a chapter: not right after starting, "
         "seeking or resuming. A title with no chapters never shows one.", 680, size=14, fill=INK)

    s.text(760, 140, "The rules", size=18, weight="700")
    rules = [
        "Never pauses the video, takes the sound or demands a click.",
        "Only for adults' profiles. A kids profile, or one Bloem cannot classify, never sees one.",
        "At most one per title for each profile in each app session.",
        "Hidden while the player's controls are open, and kept clear of the subtitles.",
        "“Save to inbox” keeps the offer, and pushes it to the profile's devices when push is set "
        "up. It expires with the campaign.",
        "On Android TV, Android phones and the web app. Bloem's Apple apps do not show them yet.",
    ]
    yy = 166
    for r in rules:
        s.add(f'<circle cx="772" cy="{yy + 10}" r="4" fill="{SOFT}"/>')
        yy = para(s, 786, yy + 15, r, 374, size=14, fill=INK, gap=20) + 6
    return s


def seasonal_year():
    s = Svg(1200, 660, "A seasonal calendar",
            "A year from January to December with two seasonal windows: winter snow from 1 December to 7 January "
            "every year, which crosses New Year, and a one-off spring banner. Notes explain time zones, missing "
            "dates and what happens when the storefront is offline.")
    s.header("A seasonal calendar",
             "Seasonal packs switch themselves on and off. A window can repeat every year.")
    months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]
    x0, x1, y = 40, 1160, 150
    mw = (x1 - x0) / 12
    for i, m in enumerate(months):
        x = x0 + i * mw
        s.rect(x + 2, y, mw - 4, 40, "#F3F4F6", rx=8)
        s.text(x + mw / 2, y + 26, m, size=14, weight="600", fill=MUTED, anchor="middle")

    def window(start, end, row, color, label):
        yy = y + 64 + row * 64
        a, b = x0 + start * mw, x0 + end * mw
        s.rect(a, yy, b - a, 40, color["stroke"], color["stroke"], rx=10, sw=1.6)
        return yy

    yy = window(11.0, 12.0, 0, SEASON, "")
    window(0.0, 6 / 31, 0, SEASON, "")
    s.text(x0 + 11.0 * mw - 12, yy + 26, "Winter snow, 1 December to 7 January, every year", size=14,
           weight="700", fill=SEASON["label"], anchor="end")
    yy = window(2 + 19 / 31, 3 + 19 / 30, 1, TIERS["plan"], "")
    s.text(x0 + (3 + 19 / 30) * mw + 12, yy + 26, "Spring banner, 20 March to 20 April, this year only",
           size=14, weight="700", fill=TIERS["plan"]["label"])

    notes = [
        ("Its own time zone", "Each window keeps its local date and time in the zone it was set in, so “1 "
         "December, 00:00, Amsterdam” starts at Amsterdam's midnight every year."),
        ("Dates that do not exist", "A yearly window set on 29 February is skipped in years without one, and so "
         "is a time that a clock change skips."),
        ("It runs on Bloem", "The calendar lives on the Bloem server. Seasons start and stop on time even when "
         "the storefront is offline."),
        ("Viewers stay in control", "Hidden while a video plays, calm when the device asks for reduced motion, "
         "and each device can switch it off."),
    ]
    for i, (t, b) in enumerate(notes):
        x, yy = 40 + (i % 2) * 570, 372 + (i // 2) * 130
        s.rect(x, yy, 550, 114, "#FFFFFF", "#E5E7EB", rx=12)
        s.rect(x, yy, 6, 114, SEASON["stroke"], rx=3)
        s.text(x + 24, yy + 32, t, size=16, weight="700")
        para(s, x + 24, yy + 58, b, 506, size=14, gap=19)
    return s


PICTURES = [("where-viewers-see", where_viewers_see), ("playback-card", playback_card),
            ("seasonal-year", seasonal_year)]
