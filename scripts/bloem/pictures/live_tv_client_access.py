"""Pictures for docs/architecture/live-tv-client-access.md."""
from xml.sax.saxutils import escape

from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

LIVE = {"fill": "#FDF2F8", "stroke": "#DB2777", "label": "#9D174D"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}
MONO = "'SFMono-Regular', Menlo, Consolas, 'Liberation Mono', monospace"


def _mono(s, x, y, txt, size=13.5, fill=INK, anchor="middle"):
    s.add(f'<text x="{x}" y="{y}" font-family="{MONO}" font-size="{size}" fill="{fill}" '
          f'text-anchor="{anchor}">{escape(txt)}</text>')


def tune_in_flow():
    s = Svg(1200, 920, "How an app tunes in to a channel",
            "A conversation between an app and the Bloem server, top to bottom. The app asks what Live TV this "
            "viewer has; lists channels and the guide; starts a session on a channel; fetches the stream, either as "
            "a browser-friendly playlist with a signed ticket or as a raw stream with its normal sign-in; checks in "
            "every 30 seconds; and releases the session when the viewer leaves.")
    s.header("How an app tunes in to a channel",
             "Every route is under /api/bloem/v1/livetv. The server checks the viewer again at each step.")
    a, f, g, st = TIERS["account"], TIERS["profile"], TIERS["grey"], TIERS["stop"]

    left, right = 170, 1030
    s.rect(left - 120, 120, 240, 56, f["fill"], f["stroke"], rx=12, sw=1.6)
    s.text(left, 155, "The app", size=18, weight="700", anchor="middle")
    s.rect(right - 120, 120, 240, 56, BLOEM["fill"], BLOEM["stroke"], rx=12, sw=1.6)
    s.text(right, 155, "Bloem server", size=18, weight="700", anchor="middle")
    for x in (left, right):
        s.add(f'<line x1="{x}" y1="178" x2="{x}" y2="846" stroke="{LINE}" stroke-width="2" stroke-dasharray="4 6"/>')

    steps = [
        ("GET /capability", "What may this viewer do?",
         "supported, allowed, available, heartbeat every 30 s"),
        ("GET /channels  ·  GET /guide", "What's on?",
         "only with the Live TV permission"),
        ("POST /channels/{id}/session", "Tune in",
         "takes a free connection, returns the stream's address"),
        ("GET /live-hls/{id}/index.m3u8?st=…", "Watch in a browser",
         "a signed ticket for this stream only"),
        ("POST /sessions/{id}/heartbeat", "Still watching",
         "every 30 seconds, keeps the connection"),
        ("DELETE /sessions/{id}", "Leave",
         "frees the connection, even after access is taken away"),
    ]
    y = 214
    for i, (route, title, reply) in enumerate(steps):
        s.number(left - 140 + 6, y + 6, i + 1, LIVE["stroke"], r=12)
        s.text(left + 20, y + 11, title, size=16, weight="700")
        _mono(s, (left + right) / 2, y + 38, route, size=14)
        s.arrow(left + 4, y + 50, right - 6, y + 50)
        s.add(f'<line x1="{right - 4}" y1="{y + 66}" x2="{left + 6}" y2="{y + 66}" stroke="{SOFT}" '
              f'stroke-width="1.5" stroke-dasharray="5 5" marker-end="url(#arrow)"/>')
        s.text((left + right) / 2, y + 88, reply, size=13.5, fill=MUTED, anchor="middle")
        y += 108

    footer(s, "A raw MPEG-TS stream (GET /sessions/{id}/stream) uses the app's normal sign-in instead of a ticket. "
              "Tickets and stream addresses never carry the account's own credentials.", y=884, fill=INK)
    return s


def who_may_watch():
    s = Svg(1200, 480, "Who may watch Live TV",
            "Two permissions are needed. The Live TV permission is separate from library access and not on by "
            "default; an access group or plan can still forbid it, and an admin account's main profile always "
            "has it. The playback permission comes from the plan; a browse-only plan or a preview visitor never "
            "plays. Without the Live TV permission, the app shows no Live TV.")
    s.header("Who may watch Live TV",
             "Two separate permissions, both checked again every time video is sent.")
    a, f, st, p = TIERS["account"], TIERS["profile"], TIERS["stop"], TIERS["plan"]
    box(s, 40, 124, 540, 160, LIVE, "The Live TV permission",
        "Separate from movie, series and book libraries, and not on by default. An administrator turns it on for "
        "an account; an access group or plan can still forbid it. An admin account's main profile always has "
        "it. Other profiles need it explicitly.", size=15)
    box(s, 620, 124, 540, 160, p, "Permission to play video",
        "Comes from the account's plan. A browse-only plan, or a storefront preview visitor, never plays Live TV, "
        "even with the Live TV permission.", size=15)
    s.rect(40, 310, 1120, 130, "#FFFFFF", "#E5E7EB", rx=14)
    no_mark(s, 74, 346)
    para(s, 100, 352, "Without the Live TV permission, the capability answers allowed: false and the app shows "
         "no Live TV at all. Without playback, tuning in is refused.", 1030, size=15, fill=INK, gap=21)
    yes_mark(s, 74, 406)
    s.text(100, 412, "A viewer can always end their own session, even after either permission is taken away.",
           size=15, fill=INK)
    return s


PICTURES = [("tune-in-flow", tune_in_flow), ("who-may-watch", who_may_watch)]
