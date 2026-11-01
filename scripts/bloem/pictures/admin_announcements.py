"""Pictures for docs/architecture/admin-announcements.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, card, footer, para


def publish_and_withdraw():
    s = Svg(1200, 790, "From draft to inbox",
            "Four numbered steps: write the message, choose who receives it, preview and publish, and later "
            "withdraw it. Below them, the five audiences an announcement can have, and an inbox card showing "
            "the three severities, where a critical message has no dismiss button.")
    s.header("From draft to inbox",
             "A platform administrator writes, checks and publishes. Bloem delivers at once and can take it back.")
    o, w, st, g = TIERS["org"], TIERS["warn"], TIERS["stop"], TIERS["grey"]

    steps = [
        (o, "Write it", "A title and a message, a severity, and if wanted a link, a picture, an action "
         "button and an expiry time."),
        (o, "Choose who", "Everyone, one account role, one organization, everyone who can see a library, "
         "or chosen accounts and profiles."),
        (o, "Preview, publish", "The preview shows the audience, the expiry and whether it can be "
         "dismissed. Publishing delivers it to every profile at once."),
        (g, "Withdraw it", "Unread copies disappear and read ones expire. The record of what was "
         "published stays in the list."),
    ]
    x, y, cw, ch, gap = 40, 124, 262, 176, 24
    for i, (color, title, body) in enumerate(steps):
        cx = x + i * (cw + gap)
        card(s, cx, y, cw, ch, color, title, body, n=i + 1, size=14)
        if i < len(steps) - 1:
            s.arrow(cx + cw + 3, y + ch / 2, cx + cw + gap - 3, y + ch / 2)
    para(s, 40, y + ch + 34, "There are no saved drafts, no start time in the future and no editing after "
         "publishing. To change a message, withdraw it and publish a new one.", 1120, size=14.5, fill=INK)

    # Audiences.
    ay = 392
    s.text(40, ay, "Who receives it", size=18, weight="700")
    audiences = [
        ("Everyone", "every enabled account, in every organization"),
        ("Account role", "administrators, or ordinary users"),
        ("Organization", "the members of one organization"),
        ("Library", "each profile that may see that library"),
        ("Chosen people", "named accounts (all their profiles) or profiles"),
    ]
    yy = ay + 22
    for name, rows in audiences:
        s.rect(40, yy, 640, 48, "#FFFFFF", "#E5E7EB", rx=10)
        s.rect(40, yy, 6, 48, o["stroke"], rx=3)
        s.text(62, yy + 30, name, size=15.5, weight="700")
        s.text(214, yy + 30, rows, size=14.5, fill=MUTED)
        yy += 58
    s.text(40, yy + 18, "Disabled accounts never receive anything.", size=13.5, fill=SOFT)

    # Inbox card with severities.
    bx, by = 720, ay - 16
    s.rect(bx, by, 440, 330, "#F9FAFB", "#E5E7EB", rx=16)
    s.text(bx + 22, by + 36, "In the viewer's inbox", size=17, weight="700")
    rows = [
        (TIERS["profile"], "INFORMATION", "New films this weekend", True),
        (w, "WARNING", "Maintenance on Sunday night", True),
        (st, "CRITICAL", "Streaming is down in some regions", False),
    ]
    ry = by + 58
    for color, label, title, dismissible in rows:
        s.rect(bx + 20, ry, 400, 76, "#FFFFFF", color["stroke"], rx=12, sw=1.5)
        s.badge(bx + 36, ry + 12, label, color, size=11.5)
        s.text(bx + 36, ry + 60, title, size=15, weight="600")
        if dismissible:
            s.text(bx + 400, ry + 30, "✕", size=16, fill=SOFT, anchor="end")
        else:
            s.text(bx + 400, ry + 30, "cannot be dismissed", size=12.5, weight="600", fill=st["label"], anchor="end")
        ry += 88

    footer(s, "Bloem stores announcements itself; the storefront is not needed to publish one. Organization "
              "administrators cannot publish them.", y=752, fill=INK)
    return s


PICTURES = [("publish-and-withdraw", publish_and_withdraw)]
