"""Pictures for docs/architecture/bloem-live-tv.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, card, footer, no_mark, para, yes_mark

LIVE = {"fill": "#FDF2F8", "stroke": "#DB2777", "label": "#9D174D"}


def sources():
    s = Svg(1200, 760, "Where Live TV comes from",
            "Three kinds of source, a network tuner, Dispatcharr and an Xtream provider, feed channels into "
            "Bloem, which adds a programme guide and a recorder. Bloem's own apps and the web app show Live TV; "
            "Silo's apps and Jellyfin apps do not.")
    s.header("Where Live TV comes from",
             "A platform administrator connects the sources. Bloem turns them into channels with a guide.")
    o, p, st = TIERS["org"], TIERS["platform"], TIERS["stop"]

    s.text(40, 136, "The sources", size=17, weight="700")
    rows = [
        ("A tuner on your network", "A SiliconDust HDHomeRun box on an antenna or cable. Bloem finds it on the "
         "network, or you give its address."),
        ("Dispatcharr", "A program that makes IPTV sources look like an HDHomeRun tuner, so Bloem treats it as one."),
        ("An Xtream provider", "A paid IPTV service on the internet. Bloem takes its live channels only, never its "
         "films or series, and stores its password encrypted."),
    ]
    y = 152
    for title, body in rows:
        box(s, 40, y, 380, 148, LIVE, title, body, size=14.5)
        s.arrow(424, y + 74, 486, y + 74)
        y += 168

    # Bloem in the middle.
    s.rect(490, 152, 340, 484, p["fill"], p["stroke"], rx=18, sw=2)
    s.text(660, 190, "Bloem", size=22, weight="700", anchor="middle")
    inner = [
        ("Channels", "Each source's channels. An administrator can adjust them or switch them off."),
        ("Programme guide", "From Schedules Direct, an XML lineup or the Xtream provider's own guide. Up to three "
         "guides at once."),
        ("Recorder", "Records a programme or a whole series to a folder on the server."),
    ]
    y = 212
    for title, body in inner:
        s.rect(510, y, 300, 128, "#FFFFFF", "#FDBA74", rx=12, sw=1.4)
        s.text(528, y + 30, title, size=16, weight="700")
        para(s, 528, y + 54, body, 266, size=13.5)
        y += 140

    # Apps.
    s.text(890, 136, "Where viewers watch", size=17, weight="700")
    s.arrow(834, 330, 886, 260)
    s.rect(890, 152, 270, 214, TIERS["account"]["fill"], TIERS["account"]["stroke"], rx=14, sw=1.8)
    yes_mark(s, 918, 184)
    s.text(940, 190, "Bloem's apps", size=17, weight="700")
    para(s, 910, 220, "The web app and Bloem's Android and Apple apps: the channel list, the guide, live "
         "playback and recordings.", 232, size=14)
    s.arrow(834, 470, 886, 500, dash="6 5")
    s.rect(890, 420, 270, 216, st["fill"], st["stroke"], rx=14, sw=1.8)
    no_mark(s, 918, 452)
    s.text(940, 458, "Other apps", size=17, weight="700")
    para(s, 910, 488, "Silo's own apps, and Jellyfin and Audiobookshelf apps, get no Live TV. They do not know "
         "Bloem's Live TV and Bloem does not offer it to them.", 232, size=14)

    footer(s, "Bloem only plays channels from a source an administrator set up. It never plays a web address "
              "someone pastes in, and it does not import an IPTV provider's films or series.", y=700)
    return s


def tune_in():
    s = Svg(1200, 820, "What happens when someone tunes in",
            "Four checks before a channel plays: who is watching, whether the viewer has the Live TV permission, "
            "whether they may play video at all, and whether one of the source's connections is free. Each "
            "refusal is shown beside its check.")
    s.header("What happens when someone tunes in",
             "Live TV has its own permission, and each source can only carry so many streams at once.")
    a, f, st, w = TIERS["account"], TIERS["profile"], TIERS["stop"], TIERS["warn"]
    checks = [
        (a, "Who is watching?", "The app has signed in and picked a profile in an active organization, as for "
         "any video.", None),
        (LIVE, "May this viewer watch Live TV?", "A separate permission. Ordinary accounts do not have it until an "
         "administrator turns it on for the account. An admin account's main profile always has it.",
         (st, "No Live TV in the app at all.")),
        (f, "May this viewer play video?", "The plan must allow playback. A browse-only plan or a preview visitor "
         "never plays Live TV, even with the permission.", (st, "Refused.")),
        (w, "Is a connection free?", "Each tuner or provider carries a set number of streams. Watching and "
         "recording share them, across every Bloem server.", (st, "Refused: every connection is busy.")),
    ]
    x, y, wd, h, gap = 40, 124, 640, 112, 32
    for i, (color, title, body, no) in enumerate(checks):
        s.rect(x, y, wd, h, color["fill"], color["stroke"], rx=14, sw=1.8)
        s.number(x + 28, y + 30, i + 1, color["stroke"])
        s.text(x + 52, y + 36, title, size=18, weight="700")
        para(s, x + 52, y + 62, body, wd - 76, size=14, gap=19)
        if no:
            color_no, out = no
            s.arrow(x + wd + 4, y + h / 2, 776, y + h / 2)
            s.text(x + wd + 22, y + h / 2 - 8, "No", size=14, weight="700", fill=color_no["label"])
            s.rect(780, y + 16, 380, h - 32, color_no["fill"], color_no["stroke"], rx=12, sw=1.5)
            para(s, 800, y + h / 2 + 5, out, 340, size=15, fill=INK)
        s.arrow(x + wd / 2, y + h + 2, x + wd / 2, y + h + gap - 3)
        if no:
            s.text(x + wd / 2 + 12, y + h + 22, "Yes", size=13, weight="700", fill=a["label"])
        y += h + gap
    s.rect(x + wd / 2 - 150, y, 300, 66, INK, rx=33)
    s.text(x + wd / 2, y + 42, "The channel plays", size=21, weight="700", fill="#FFFFFF", anchor="middle")
    para(s, 780, y + 6, "The app checks in every 30 seconds to keep its connection. Leaving the channel frees it "
         "for someone else. Bloem checks both permissions again as it sends the video.", 380, size=14, fill=MUTED)
    return s


def recording():
    s = Svg(1200, 760, "How a recording works",
            "Three ways to ask for a recording, from the guide, by channel and time, or with a recording rule, "
            "lead to the recorder, which writes a file to the recordings folder. An administrator scans that "
            "folder through a library before the recording appears in the catalog.")
    s.header("How a recording works",
             "Viewers with the Live TV permission ask for recordings. Bloem records at the right time.")
    o, a, g = TIERS["org"], TIERS["account"], TIERS["grey"]

    s.text(40, 136, "Ask for it", size=17, weight="700")
    asks = [
        ("From the guide", "Pick a programme in the guide.", 96),
        ("By channel and time", "Choose a channel, a start and an end.", 96),
        ("With a recording rule", "Every programme whose title matches, on one channel or only new episodes, or "
         "one series from the guide.", 124),
    ]
    y = 152
    for title, body, h in asks:
        box(s, 40, y, 300, h, LIVE, title, body, size=13.5, title_size=16)
        s.arrow(344, y + h / 2, 388, 324)
        y += h + 14
    steps = [
        (o, "Bloem records it", "At the time, using one of the source's connections, just as watching does."),
        (g, "A recording file", "Written to the recordings folder on the server's disk."),
        (a, "Scanned into a library", "An administrator adds that folder to a library and scans it. Then the "
         "recording appears in the catalog and plays like any video."),
    ]
    x = 392
    for i, (color, title, body) in enumerate(steps):
        box(s, x, 239, 228, 170, color, title, body, size=13.5, title_size=16)
        if i < len(steps) - 1:
            s.arrow(x + 232, 324, x + 266, 324)
        x += 270

    s.text(40, 548, "Good to know", size=17, weight="700")
    notes = [
        "A recording does not appear in the catalog by itself: the folder has to be scanned through a library.",
        "Removing a recording rule does not cancel recordings it already scheduled, and deletes no files.",
        "Removing an Xtream provider deletes its channels and scheduled recordings, but never recorded files.",
        "Bloem does not yet delete old recordings for you; a rule's “keep the last N” is not applied.",
    ]
    y = 564
    for n in notes:
        s.add(f'<circle cx="52" cy="{y + 14}" r="4" fill="{SOFT}"/>')
        para(s, 66, y + 19, n, 1080, size=14.5, fill=INK)
        y += 40
    return s


PICTURES = [("sources", sources), ("tune-in", tune_in), ("recording", recording)]
