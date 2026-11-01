"""Pictures for docs/architecture/admin-remote-control.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark


def command_journey():
    s = Svg(1200, 770, "How a remote command travels",
            "Top: who may send which commands. A household's main profile sends a reduced set to its own "
            "account's sessions; an administrator also sends terminate, which needs a reason, and replan. "
            "Middle: the path of one command from the sender through the Bloem server to the app's playback "
            "connection, refused when the app did not list the command or has no live connection. Bottom: the "
            "command's states, from sent to accepted to done or rejected, or expired.")
    s.header("How a remote command travels",
             "From the sender, through the server, to the playing app, and the states it can end in.")
    p, a, f, w, st, g = (TIERS["platform"], TIERS["account"], TIERS["profile"], TIERS["warn"], TIERS["stop"],
                         TIERS["grey"])

    # Who may send what.
    box(s, 40, 124, 540, 116, f, "A household's main profile", "Only its own account's sessions: pause, "
        "resume, play or pause, seek, stop, volume, audio track, subtitle track, show a message.", size=14)
    box(s, 620, 124, 540, 116, p, "An administrator", "Sessions in their organization scope: everything a "
        "household can send, plus terminate (with a reason the viewer sees) and replan (a better playback "
        "plan).", size=14)

    # Path.
    y = 290
    s.text(40, y, "The path of one command", size=18, weight="700")
    nodes = [
        (40, g, "Sender", ["admin console or", "household app"]),
        (330, p, "Bloem server", ["checks the sender's", "rights and the app's list"]),
        (620, p, "Playback connection", ["the app's live socket", "for this session"]),
        (910, TIERS["account"], "Playing app", ["acts, then answers", "accepted and done"]),
    ]
    for x, color, title, rows in nodes:
        s.rect(x, y + 20, 250, 96, color["fill"], color["stroke"], rx=14, sw=1.6)
        s.text(x + 125, y + 52, title, size=16, weight="700", anchor="middle")
        s.lines(x + 125, y + 76, rows, size=13.5, fill=MUTED, anchor="middle", gap=18)
    for x in (290, 580, 870):
        s.arrow(x + 2, y + 68, x + 38, y + 68)

    # Refusals.
    ry = y + 150
    no_mark(s, 356, ry + 8, r=12)
    para(s, 378, ry + 4, "Not on the app's list of commands: recorded as rejected_unsupported and never sent.",
         240, size=13.5, fill=INK, gap=18)
    no_mark(s, 646, ry + 8, r=12)
    para(s, 668, ry + 4, "No live connection: 409 session_not_connected, recorded as failed. Never queued.",
         240, size=13.5, fill=INK, gap=18)

    # States.
    sy = 540
    s.text(40, sy, "Its states", size=18, weight="700")
    pill = lambda x, label, color: (s.rect(x, sy + 24, 150, 46, color["fill"], color["stroke"], rx=23, sw=1.8),
                                    s.text(x + 75, sy + 53, label, size=15, weight="700", fill=color["label"],
                                           anchor="middle"))
    pill(40, "sent", p)
    pill(260, "accepted", a)
    pill(500, "done", a)
    pill(500 + 190, "rejected", st)
    pill(940, "expired", w)
    s.arrow(194, sy + 47, 256, sy + 47)
    s.arrow(414, sy + 47, 496, sy + 47)
    s.path(f"M 414 {sy + 60} C 520 {sy + 110}, 640 {sy + 110}, 700 {sy + 74}")
    s.path(f"M 120 {sy + 72} C 300 {sy + 150}, 820 {sy + 150}, 1000 {sy + 74}", dash="5 5")
    s.text(560, sy + 154, "no answer within 8 seconds, past the 60-second expiry, or the session ended",
           size=13, fill=SOFT, anchor="middle")

    footer(s, "A finished command never changes again; a late duplicate answer is ignored. Only the session a "
              "command was sent to can answer it.", y=736, fill=INK)
    return s


PICTURES = [("command-journey", command_journey)]
