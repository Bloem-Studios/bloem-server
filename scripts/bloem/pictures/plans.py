"""Pictures for docs/architecture/bloem-plans.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, footer, no_mark, para, wrap, yes_mark


def starting_plans():
    s = Svg(1200, 640, "The plans Bloem starts with",
            "A table of the five built-in plans as shipped: Browse-only, Viewer, Standard, Premium and Reseller "
            "Member, with whether each plays video, how many screens, video conversions, the best quality, "
            "downloads and requests. A sixth, protected Preview plan serves storefront previews.")
    s.header("The plans Bloem starts with",
             "As shipped. Each installation can revise these or make its own.")
    plan = TIERS["plan"]
    cols = ["Browse-only", "Viewer", "Standard", "Premium", "Reseller Member"]
    rows = [
        ("Plays video", ["no", "yes", "yes", "yes", "yes"]),
        ("Screens at once", ["–", "1", "3", "4", "3"]),
        ("Video conversions at once", ["–", "none", "1", "2", "1"]),
        ("Best picture quality", ["–", "1080p", "1080p", "4K", "1080p"]),
        ("Downloads", ["no", "originals", "originals and converted", "originals and converted",
                       "originals and converted"]),
        ("Requests", ["no", "yes", "yes", "yes", "yes"]),
    ]
    x0, lw, cw, y0, rh = 40, 280, 168, 124, 62
    s.rect(x0, y0, lw + cw * len(cols), 56, plan["fill"], plan["stroke"], rx=12, sw=1.4)
    for i, c in enumerate(cols):
        s.text(x0 + lw + cw * i + cw / 2, y0 + 35, c, size=16, weight="700", fill=plan["label"], anchor="middle")
    y = y0 + 56
    for r, (label, vals) in enumerate(rows):
        if r % 2:
            s.rect(x0, y, lw + cw * len(cols), rh, "#F9FAFB", rx=0)
        s.text(x0 + 18, y + rh / 2 + 5, label, size=15, weight="600")
        for i, v in enumerate(vals):
            cx = x0 + lw + cw * i + cw / 2
            if v == "yes":
                yes_mark(s, cx, y + rh / 2, r=11)
            elif v == "no":
                no_mark(s, cx, y + rh / 2, r=11)
            else:
                rows_ = wrap(v, cw - 20, 14.5)
                s.lines(cx, y + rh / 2 + 5 - (len(rows_) - 1) * 9, rows_, size=14.5, fill=INK, gap=18, anchor="middle")
        y += rh
    s.add(f'<line x1="{x0}" y1="{y}" x2="{x0 + lw + cw * len(cols)}" y2="{y}" stroke="#E5E7EB" stroke-width="1.5"/>')
    footer(s, "A sixth plan, Preview, is protected: it serves the storefront's preview visitors, can never be "
              "loosened, and is described below. Browse-only can never be changed to play video.", y=y + 46)
    return s


def layers():
    s = Svg(1200, 800, "How a plan, an account and a profile combine",
            "Four layers, each narrowing the one above: what the organization may use, the account's plan, "
            "settings an administrator changed on one account, and the profile's own limits. An example account "
            "on the right shows the result.")
    s.header("How a plan, an account and a profile combine",
             "Each layer can only narrow the one above it. The example on the right follows one family.")
    o, p, a, f = TIERS["org"], TIERS["plan"], TIERS["account"], TIERS["profile"]
    layers_ = [
        (o, "The organization's libraries", "What the organization owns or was granted. Nothing below can reach "
         "past it.", "Movies, Series and Audiobooks."),
        (p, "The account's plan", "A plan (exactly one revision of it) on the account's group: libraries, screens, "
         "conversions, quality, downloads and requests.", "Standard, revision 3: every library, 3 screens, 1080p, "
         "downloads."),
        (a, "This account's own settings", "An administrator can change a value for one account. That value "
         "replaces the plan's, still within the organization's libraries.", "Screens raised to 4 for this account "
         "only."),
        (f, "Each profile's limits", "The household's main profile sets PINs, kids profiles, age limits and a "
         "chosen set of libraries.", "Kids profile: ages 7 and up, Movies only."),
    ]
    y, h, gap = 124, 118, 22
    s.text(820, 112, "Example: the Jansen family", size=16, weight="700")
    for i, (color, title, body, ex) in enumerate(layers_):
        s.rect(40, y, 700, h, color["fill"], color["stroke"], rx=14, sw=1.8)
        s.number(68, y + 32, i + 1, color["stroke"])
        s.text(92, y + 38, title, size=18, weight="700")
        para(s, 92, y + 66, body, 620, size=14.5, gap=20)
        s.add(f'<line x1="744" y1="{y + h / 2}" x2="812" y2="{y + h / 2}" stroke="#E5E7EB" stroke-width="2" stroke-dasharray="4 5"/>')
        s.rect(816, y + 10, 344, h - 20, "#FFFFFF", "#E5E7EB", rx=12)
        s.rect(816, y + 10, 6, h - 20, color["stroke"], rx=3)
        rows = wrap(ex, 300, 14.5)
        s.lines(838, y + h / 2 + 5 - (len(rows) - 1) * 10, rows, size=14.5, fill=INK, gap=20)
        if i < len(layers_) - 1:
            s.arrow(390, y + h + 2, 390, y + h + gap - 3)
        y += h + gap
    s.rect(40, y + 4, 1120, 92, INK, rx=16)
    s.text(64, y + 40, "Result for the Kids profile", size=18, weight="700", fill="#FFFFFF")
    para(s, 64, y + 68, "Movies only, ages 7 and up. The whole account can play on up to 4 screens at once, in up "
         "to 1080p, and download.", 1060, size=15, fill="#E5E7EB")
    return s


def revisions():
    s = Svg(1200, 640, "Changing a plan safely",
            "A plan's revisions in a row, from revision 1 to a new revision 4, with accounts staying on revision 3 "
            "until moved. Four steps move them: create the revision, choose it, preview the change and confirm "
            "within ten minutes.")
    s.header("Changing a plan safely",
             "A change never rewrites a plan. It adds a revision, and accounts move only when you say so.")
    p, a, g, w = TIERS["plan"], TIERS["account"], TIERS["grey"], TIERS["warn"]
    s.text(40, 140, "Standard", size=17, weight="700")
    xs = [160, 380, 600, 860]
    labels = [("Revision 1", "2 screens", g), ("Revision 2", "3 screens", g), ("Revision 3", "3 screens, 4K", p),
              ("Revision 4 (new)", "4 screens, 4K", w)]
    for i, (x, (t, b, c)) in enumerate(zip(xs, labels)):
        s.rect(x, 116, 200, 72, c["fill"], c["stroke"], rx=12, sw=1.6, dash="6 5" if i == 3 else None)
        s.text(x + 100, 146, t, size=15.5, weight="700", fill=c["label"], anchor="middle")
        s.text(x + 100, 170, b, size=14, fill=MUTED, anchor="middle")
        if i < 3:
            s.arrow(x + 204, 152, xs[i + 1] - 4, 152)
    for i, name in enumerate(["Jansen family", "De Vries family", "Riverside Club"]):
        s.badge(612, 204 + i * 34, f"On revision 3: {name}", a, size=12.5)
    s.text(860, 226, "Nobody is on it yet.", size=14, fill=MUTED)

    steps = [
        ("Create a new revision", "Change the screens, quality or anything else. Revisions 1 to 3 stay exactly as "
         "they were."),
        ("Choose who and which revision", "One account, one organization, or a reviewed group of up to 10,000 "
         "accounts, and the exact revision to move them to."),
        ("Preview", "Bloem shows what will change, and for whom, before anything does."),
        ("Confirm within ten minutes", "The confirmation is tied to the preview. If anything changed in between, "
         "preview again."),
    ]
    x0, y0, cw, ch, gap = 40, 330, 256, 196, 32
    for i, (t, b) in enumerate(steps):
        x = x0 + i * (cw + gap)
        s.rect(x, y0, cw, ch, p["fill"], p["stroke"], rx=14)
        s.number(x + 26, y0 + 28, i + 1, p["stroke"])
        para(s, x + 16, y0 + 70, t, cw - 32, size=16, fill=INK, weight="700", gap=21)
        para(s, x + 16, y0 + 118, b, cw - 32, size=13.5, gap=18)
        if i < len(steps) - 1:
            s.arrow(x + cw + 3, y0 + ch / 2, x + cw + gap - 3, y0 + ch / 2)
    footer(s, "To undo a change, make another new revision with the old values and move the accounts to it. "
              "The history of every revision stays.", y=590)
    return s


def preview():
    s = Svg(1200, 720, "A storefront visitor's preview",
            "Five steps: a visitor clicks preview on the storefront, Bloem opens a short guest session on a separate "
            "address, the visitor browses without playing, free play if the storefront switched it on, and "
            "signing up on the storefront. Limits that protect the server are listed below.")
    s.header("A storefront visitor's preview",
             "The storefront (Bloem Garden) lets people look around before they buy. Bloem keeps them in a "
             "locked room.")
    o, a, g, w, pl = TIERS["org"], TIERS["account"], TIERS["grey"], TIERS["warn"], TIERS["plan"]
    steps = [
        (o, "Click Preview", "The storefront hands the visitor a one-time link that works for one minute."),
        (o, "A guest session", "Bloem opens it on a separate preview address. It ends after two hours, or "
         "20 minutes without use."),
        (pl, "Look around", "Chosen libraries, descriptions, seasons and episodes. Nothing plays, downloads or "
         "saves."),
        (w, "Free play, if on", "The storefront confirms an email address. Bloem then plays real video for a "
         "set number of seconds, and counts them."),
        (a, "Sign up", "The preview's buttons lead back to the storefront, which creates an ordinary account "
         "on a plan."),
    ]
    x0, y0, cw, ch, gap = 40, 128, 204, 236, 25
    for i, (c, t, b) in enumerate(steps):
        x = x0 + i * (cw + gap)
        s.rect(x, y0, cw, ch, c["fill"], c["stroke"], rx=14)
        s.number(x + 26, y0 + 28, i + 1, c["stroke"])
        s.text(x + 16, y0 + 74, t, size=16.5, weight="700")
        para(s, x + 16, y0 + 102, b, cw - 30, size=13.5, gap=18)
        if i < len(steps) - 1:
            s.arrow(x + cw + 3, y0 + ch / 2, x + cw + gap - 3, y0 + ch / 2)

    s.text(40, 420, "What keeps it safe", size=18, weight="700")
    facts = [
        ("One shared guest account", "Every visitor uses the same locked account on the protected Preview plan. "
         "It cannot be loosened."),
        ("Its own address", "Guest sessions work only on the preview address, and nothing else signs in there."),
        ("Capacity", "200 visitors at once by default, at most three per internet address. Free play: 20 "
         "streams at once by default."),
        ("Off means off", "Switching the preview off ends every guest session straight away. Switching free play "
         "off stops every allowance."),
    ]
    for i, (t, b) in enumerate(facts):
        x, y = 40 + (i % 2) * 570, 440 + (i // 2) * 128
        s.rect(x, y, 550, 112, "#FFFFFF", "#E5E7EB", rx=12)
        s.rect(x, y, 6, 112, g["stroke"], rx=3)
        s.text(x + 24, y + 32, t, size=16, weight="700")
        para(s, x + 24, y + 58, b, 506, size=14, gap=19)
    return s


def upgrade_prompt():
    s = Svg(1200, 600, "When every screen is busy",
            "A viewer starts a video while the plan's screens are all in use. Playback is refused, and, if a "
            "platform administrator switched the prompt on, an alert with an upgrade button lands in the viewer's "
            "inbox. The button leads to the storefront.")
    s.header("When every screen is busy",
             "A refused stream can come with an offer to upgrade, if the storefront wants that.")
    a, st, w, p = TIERS["account"], TIERS["stop"], TIERS["warn"], TIERS["platform"]

    # 1: the refusal.
    s.rect(40, 128, 340, 250, "#111827", rx=16)
    s.rect(56, 144, 308, 178, "#1F2937", rx=8)
    no_mark(s, 210, 196, r=18)
    s.text(210, 252, "Can't play here", size=17, weight="700", fill="#FFFFFF", anchor="middle")
    s.text(210, 278, "Your plan plays on 3 screens", size=14, fill="#D1D5DB", anchor="middle")
    s.text(210, 298, "at once, and all 3 are in use.", size=14, fill="#D1D5DB", anchor="middle")
    s.text(210, 356, "1  The fourth screen is refused", size=15, weight="700", fill="#FFFFFF", anchor="middle")
    s.arrow(384, 253, 438, 253)

    # 2: the inbox alert.
    s.rect(442, 128, 340, 250, "#FFFFFF", "#E5E7EB", rx=16)
    s.text(462, 160, "Inbox", size=13, weight="700", fill=SOFT, spacing="1")
    s.rect(462, 176, 300, 124, w["fill"], w["stroke"], rx=12)
    s.text(480, 206, "Want more screens?", size=16, weight="700")
    para(s, 480, 230, "Upgrade your plan to watch on more devices at once.", 264, size=13.5, gap=18)
    s.rect(480, 262, 110, 28, w["stroke"], rx=14)
    s.text(535, 281, "Upgrade", size=13.5, weight="700", fill="#FFFFFF", anchor="middle")
    s.text(612, 356, "2  An alert, and a push if allowed", size=15, weight="700", anchor="middle")
    s.arrow(786, 253, 840, 253)

    # 3: the storefront.
    s.rect(844, 128, 316, 250, p["fill"], p["stroke"], rx=16, sw=1.8)
    s.text(1002, 220, "The storefront", size=19, weight="700", anchor="middle")
    para(s, 1002, 250, "The button opens the link the administrator set, such as the storefront's plan page.",
         270, size=14, anchor="middle")
    s.text(1002, 356, "3  The viewer chooses a plan", size=15, weight="700", anchor="middle")

    s.text(40, 432, "The rules", size=18, weight="700")
    rules = [
        "Off until a platform administrator saves the wording and the link, with the prompt switched on.",
        "A profile gets it at most once a day, and the alert disappears after three days.",
        "It is a push notification too, only where the profile allows notifications.",
    ]
    y = 452
    for r in rules:
        s.add(f'<circle cx="52" cy="{y + 14}" r="4" fill="{SOFT}"/>')
        para(s, 66, y + 19, r, 1080, size=14.5, fill=INK)
        y += 36
    return s


PICTURES = [("starting-plans", starting_plans), ("layers", layers), ("revisions", revisions),
            ("preview", preview), ("upgrade-prompt", upgrade_prompt)]
