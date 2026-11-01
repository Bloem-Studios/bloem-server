"""Pictures for docs/architecture/bloem-organizations.md."""
from kit import INK, LINE, MUTED, SOFT, TIERS, Svg


def tiers():
    s = Svg(1200, 840, "How Bloem is organized",
            "Four nested tiers: the platform contains organizations, organizations contain accounts, "
            "accounts contain profiles. A legend explains each tier.")
    s.header("How Bloem is organized", "Four tiers. Each one lives inside the one above it.")
    p, o, a, f = TIERS["platform"], TIERS["org"], TIERS["account"], TIERS["profile"]

    # Tier 1: the platform.
    s.rect(40, 112, 760, 690, p["fill"], p["stroke"], rx=20, sw=2)
    s.number(66, 141, 1, p["stroke"])
    s.text(88, 146, "THE PLATFORM", size=14, weight="700", fill=p["label"], spacing="1")
    s.text(64, 174, "The Bloem service: its servers, its libraries and its platform administrators.", size=15)
    s.text(64, 206, "Platform libraries", size=13, weight="600", fill=SOFT)
    x = 64
    for name in ["Movies", "Series", "Audiobooks", "Ebooks", "Live TV"]:
        x += s.badge(x, 216, name, {"fill": "#FFFFFF", "stroke": "#FDBA74", "label": INK}, size=14) + 10

    def account(x, y, email, tag, tag_color, people):
        s.rect(x, y, 308, 162, a["fill"], a["stroke"], rx=14)
        s.number(x + 18, y + 22, 3, a["stroke"], r=11)
        s.text(x + 36, y + 27, "ACCOUNT", size=12.5, weight="700", fill=a["label"], spacing="1")
        s.text(x + 16, y + 52, email, size=15, weight="600")
        s.badge(x + 292, y + 10, tag, tag_color, size=12, anchor="end")
        s.number(x + 18, y + 78, 4, f["stroke"], r=11)
        s.text(x + 36, y + 83, "PROFILES", size=12.5, weight="700", fill=f["label"], spacing="1")
        cw = (308 - 32 - 10 * (len(people) - 1)) / len(people)
        for i, (name, note) in enumerate(people):
            cx = x + 16 + i * (cw + 10)
            s.rect(cx, y + 96, cw, 52, f["fill"], f["stroke"], rx=10, sw=1.2)
            s.text(cx + cw / 2, y + (118 if note else 127), name, size=15, weight="600", anchor="middle")
            if note:
                s.text(cx + cw / 2, y + 137, note, size=12, fill=MUTED, anchor="middle")

    plan = TIERS["plan"]
    # Tier 2: two organizations.
    for ox, name, rows in [
        (64, "Default organization", ["Bloem's own direct customers,", "each account managed one by one."]),
        (428, "Riverside Club", ["A reseller sold a block of seats on", "the storefront: 10 seats, 2 shared", "video conversions."]),
    ]:
        s.rect(ox, 268, 348, 516, o["fill"], o["stroke"], rx=16, sw=1.6)
        s.number(ox + 22, 294, 2, o["stroke"], r=12)
        s.text(ox + 42, 299, "ORGANIZATION", size=13, weight="700", fill=o["label"], spacing="1")
        s.text(ox + 20, 326, name, size=18, weight="700")
        s.lines(ox + 20, 348, rows, size=13.5, gap=18)
    s.badge(448, 394, "Its own library: Club matches", {"fill": "#FFFFFF", "stroke": "#FDBA74", "label": INK}, size=12)
    s.badge(84, 394, "Granted: every platform library", {"fill": "#FFFFFF", "stroke": "#FDBA74", "label": INK}, size=12)

    account(84, 432, "jansen@example.com", "Plan: Standard", plan,
            [("Anna", "PIN"), ("Tom", ""), ("Kids", "ages 7+")])
    account(84, 606, "devries@example.com", "Plan: Premium", plan,
            [("Eva", ""), ("Sem", "")])
    account(448, 432, "coach@riverside.example", "Admin", o,
            [("Coach", "")])
    account(448, 606, "sam@riverside.example", "Member", plan,
            [("Sam", ""), ("Lee", "ages 12+")])

    # Legend.
    s.text(828, 146, "What each tier means", size=17, weight="700")
    cards = [
        (p, 1, "Platform", ["The Bloem service itself. It owns",
                            "the servers and the main libraries.",
                            "Run by platform administrators."]),
        (o, 2, "Organization", ["One customer: Bloem's direct customers",
                                "together, or a reseller, club or business.",
                                "Sees only its own people, libraries",
                                "and settings. Run by its admin."]),
        (a, 3, "Account", ["One sign-in (email and password).",
                           "Belongs to exactly one organization.",
                           "Its plan sets screens, quality and",
                           "downloads."]),
        (f, 4, "Profile", ["One person using the account. Can",
                           "have a PIN, an age limit and only",
                           "the libraries chosen for them."]),
    ]
    y = 166
    for color, n, title, rows in cards:
        h = 58 + 20 * len(rows)
        s.rect(828, y, 332, h, "#FFFFFF", "#E5E7EB", rx=12)
        s.rect(828, y, 8, h, color["stroke"], rx=4)
        s.number(862, y + 26, n, color["stroke"], r=12)
        s.text(884, y + 32, title, size=17, weight="700")
        s.lines(852, y + 58, rows, size=14, gap=20)
        y += h + 14
    return s


def funnel():
    s = Svg(1200, 760, "What can this person watch?",
            "A funnel. The server's libraries narrow to what the organization may use, then to what the "
            "account's plan allows, then to what the profile may see, which is what appears on screen.")
    s.header("What can this person watch?",
             "The organization sets the outer limit. The plan and the profile narrow it down.")
    steps = [
        (TIERS["platform"], 1, 760, "Everything on the Bloem server",
         "All libraries: movies, series, audiobooks, ebooks, Live TV",
         ["Platform administrators", "add libraries."]),
        (TIERS["org"], 2, 664, "What the organization may use",
         "Libraries it owns, plus platform libraries granted to it",
         ["Platform administrators grant", "libraries. A new organization", "gets a standard starting set."]),
        (TIERS["account"], 3, 568, "What the account's plan allows",
         "Which libraries, how many screens, quality, downloads",
         ["The plan the storefront or an", "admin puts the account on. An", "admin can adjust one account."]),
        (TIERS["profile"], 4, 472, "What this profile may see",
         "Kids profiles, age limits, a chosen set of libraries",
         ["The account's main profile,", "who manages the household."]),
    ]
    cx, y, h, gap = 420, 120, 96, 26
    s.text(880, 112, "Who sets it", size=17, weight="700")
    for color, n, w, title, desc, who in steps:
        x = cx - w / 2
        s.rect(x, y, w, h, color["fill"], color["stroke"], rx=16, sw=2)
        s.number(x + 30, y + h / 2, n, color["stroke"], r=14)
        s.text(cx + 14, y + 40, title, size=19, weight="700", anchor="middle")
        s.text(cx + 14, y + 66, desc, size=14, fill=MUTED, anchor="middle")
        s.rect(880, y + 6, 280, h - 12, "#FFFFFF", "#E5E7EB", rx=12)
        s.rect(880, y + 6, 6, h - 12, color["stroke"], rx=3)
        s.lines(902, y + h / 2 + 5 - (len(who) - 1) * 9.5, who, size=14, gap=19, fill=INK)
        s.add(f'<line x1="{cx + w / 2 + 8}" y1="{y + h / 2}" x2="872" y2="{y + h / 2}" stroke="#E5E7EB" stroke-width="2" stroke-dasharray="4 5"/>')
        if n < len(steps):
            s.arrow(cx, y + h + 2, cx, y + h + gap - 3)
        y += h + gap
    s.arrow(cx, y - gap + 2, cx, y - 3)
    s.rect(cx - 170, y, 340, 72, INK, rx=36)
    s.text(cx, y + 45, "What appears on screen", size=20, weight="700", fill="#FFFFFF", anchor="middle")
    s.lines(40, y + 112, [
        "Step 2 is a hard ceiling: nothing below it can reach a library the organization does not own or hold.",
        "A browse-only plan shows the catalog but never plays anything.",
    ], size=14, gap=22)
    return s


def lifecycle():
    s = Svg(1200, 790, "How an organization starts, runs and ends",
            "Five steps from a storefront sale to people watching, an alternative where a platform "
            "administrator creates the organization by hand, and the organization's four states.")
    s.header("How an organization starts, runs and ends",
             "Direct customers are accounts in the default organization. A reseller gets an organization of its own.")
    o, a, g, w = TIERS["org"], TIERS["account"], TIERS["grey"], TIERS["warn"]
    steps = [
        ("A reseller buys", "a block of seats", ["On the storefront", "(Bloem Garden): a club,", "business or reseller", "with its own members."], None),
        ("The storefront asks", "Bloem to create it", ["With its number of", "seats and shared video", "conversions."], ("Getting ready", g)),
        ("Bloem gives it", "starting libraries", ["The platform's starting", "set of libraries and", "plugins is granted."], None),
        ("The first admin", "account is added", ["The organization's", "own administrator."], ("Active", a)),
        ("People are added", "and watch", ["The organization's admin", "adds them. Each takes a", "seat and gets the", "plan's limits."], None),
    ]
    x0, y0, cw, ch, gap = 40, 124, 204, 196, 26
    for i, (t1, t2, body, state) in enumerate(steps):
        x = x0 + i * (cw + gap)
        s.rect(x, y0, cw, ch, o["fill"], o["stroke"], rx=14)
        s.number(x + 26, y0 + 28, i + 1, o["stroke"])
        s.text(x + 16, y0 + 70, t1, size=16, weight="700")
        s.text(x + 16, y0 + 91, t2, size=16, weight="700")
        s.lines(x + 16, y0 + 118, body, size=13.5, gap=18)
        if state:
            s.badge(x + cw - 14, y0 + 14, state[0], state[1], size=12.5, anchor="end")
        if i < len(steps) - 1:
            s.arrow(x + cw + 3, y0 + ch / 2, x + cw + gap - 3, y0 + ch / 2)

    # Alternative entry by hand.
    s.rect(40, 352, 434, 70, "#FFFFFF", LINE, rx=12, dash="6 5")
    s.lines(60, 381, ["Or a platform administrator creates an organization by",
                      "hand in the admin console, with its owner. It starts active."],
            size=14, gap=20, fill=INK)
    s.path(f"M 474 387 L 602 387 L 602 {y0 + ch + 4}", dash="6 5")

    # States.
    s.text(40, 474, "Its states", size=19, weight="700")
    states = [
        (40, "Getting ready", g, ["Sold, but no admin yet."]),
        (300, "Active", a, ["Members sign in and watch."]),
        (700, "Suspended", w, ["Members are refused until", "it is active again."]),
        (980, "Retired", g, ["Members removed. Accounts", "with no other home are", "deleted. The record stays."]),
    ]
    sy, sw_, sh = 548, 180, 56
    for x, label, color, rows in states:
        s.rect(x, sy, sw_, sh, color["fill"], color["stroke"], rx=28, sw=2)
        s.text(x + sw_ / 2, sy + 36, label, size=18, weight="700", fill=color["label"], anchor="middle")
        s.lines(x + 4, sy + sh + 32, rows, size=14, gap=20, fill=MUTED)
    s.arrow(223, sy + sh / 2, 296, sy + sh / 2)
    s.lines(260, sy + sh / 2 - 12, ["first admin"], size=12.5, fill=SOFT, anchor="middle")
    s.path(f"M 482 {sy + 14} C 540 {sy - 36}, 640 {sy - 36}, 696 {sy + 14}")
    s.lines(590, 498, ["Too many people for its seats, or stopped",
                       "by an admin or the storefront (e.g. unpaid)"], size=12.5, fill=SOFT, gap=16, anchor="middle")
    s.path(f"M 698 {sy + sh - 12} C 640 {sy + sh + 34}, 540 {sy + sh + 34}, 486 {sy + sh - 12}")
    s.lines(592, sy + sh + 46, ["Back under its seats by itself,", "or lifted by hand"], size=12.5, fill=SOFT, gap=16, anchor="middle")
    s.arrow(883, sy + sh / 2, 976, sy + sh / 2)
    s.lines(930, sy - 14, ["storefront ends it", "(from any state)"], size=12.5, fill=SOFT, gap=16, anchor="middle")
    s.lines(40, 756, ["An organization is never deleted outright. A retired one stays on record as the owner of anything it had."],
            size=14, fill=INK)
    return s


def press_play():
    s = Svg(1200, 860, "What happens when someone presses play",
            "Five checks run in order: who is watching, whether the organization is active, whether the "
            "profile may see the title, whether a screen is free, and whether there is room to convert "
            "the video. Each refusal is shown beside its check.")
    s.header("What happens when someone presses play",
             "Bloem runs these checks in a fraction of a second. Any “no” stops the video.")
    o, a, f, st, wa = TIERS["org"], TIERS["account"], TIERS["profile"], TIERS["stop"], TIERS["warn"]
    checks = [
        (a, "Who is watching?", ["The app has signed in to an account and picked a profile.",
                                 "Bloem looks up the account's one organization."], None),
        (o, "Is the organization active?", ["A suspended organization's members are refused."],
         (st, ["Refused: the organization", "is suspended."])),
        (f, "May this profile see this title?", ["The organization, plan and profile limits",
                                                 "from “What can this person watch?”"],
         (st, ["The title is not shown", "at all."])),
        (a, "Is a screen free?", ["The plan sets how many videos the account",
                                  "can play at the same time."],
         (wa, ["Refused. If switched on, the", "viewer also gets an upgrade", "offer in their inbox."])),
        (o, "If the video needs converting, is there room?",
         ["The account's own limit, and for a storefront group,",
          "the group's shared pool of conversions."],
         (st, ["Refused until a conversion", "frees up."])),
    ]
    x, y, w, h, gap = 40, 124, 640, 96, 34
    for i, (color, title, rows, no) in enumerate(checks):
        s.rect(x, y, w, h, color["fill"], color["stroke"], rx=14, sw=1.8)
        s.number(x + 28, y + 30, i + 1, color["stroke"])
        s.text(x + 52, y + 36, title, size=18, weight="700")
        s.lines(x + 52, y + 62, rows, size=14, gap=19)
        if no:
            color_no, out = no
            s.arrow(x + w + 4, y + h / 2, 776, y + h / 2)
            s.text(x + w + 22, y + h / 2 - 8, "No", size=14, weight="700", fill=color_no["label"])
            s.rect(780, y + 8, 380, h - 16, color_no["fill"], color_no["stroke"], rx=12, sw=1.5)
            s.lines(800, y + 40, out, size=15, gap=20, fill=INK)
        s.arrow(x + w / 2, y + h + 2, x + w / 2, y + h + gap - 3)
        s.text(x + w / 2 + 12, y + h + 22, "Yes" if no else "", size=13, weight="700", fill=TIERS["account"]["label"])
        y += h + gap
    s.rect(x + w / 2 - 150, y, 300, 66, INK, rx=33)
    s.text(x + w / 2, y + 42, "The video plays", size=21, weight="700", fill="#FFFFFF", anchor="middle")
    s.lines(780, y + 22, ["While it plays, Bloem checks library", "access again each time it sends video."],
            size=14, gap=20, fill=MUTED)
    return s


def reseller_bought():
    s = Svg(1200, 720, "Example: what Riverside Club bought",
            "A reseller organization with ten seats, eight taken by member accounts and two free; two shared "
            "video conversions, both in use, so a third member's converted video is refused; and its libraries "
            "and plan.")
    s.header("Example: what Riverside Club bought",
             "A football club resells Bloem to its members. It bought 10 seats and 2 shared video conversions.")
    o, a, f, st, p = TIERS["org"], TIERS["account"], TIERS["profile"], TIERS["stop"], TIERS["platform"]

    # Seats.
    s.rect(40, 116, 640, 360, o["fill"], o["stroke"], rx=16, sw=1.8)
    s.text(64, 152, "10 seats", size=20, weight="700")
    s.text(64, 176, "Each member account takes one seat.", size=14, fill=MUTED)
    names = [("Coach", "admin"), ("Sam", ""), ("Lee", ""), ("Ana", ""), ("Bo", ""),
             ("Kim", ""), ("Max", ""), ("Noor", ""), (None, ""), (None, "")]
    for i, (name, note) in enumerate(names):
        col, row = i % 5, i // 5
        x, y = 64 + col * 120, 200 + row * 112
        if name:
            s.rect(x, y, 104, 96, a["fill"], a["stroke"], rx=12, sw=1.5)
            s.add(f'<circle cx="{x + 52}" cy="{y + 34}" r="16" fill="{a["stroke"]}"/>')
            s.text(x + 52, y + 40, name[0], size=16, weight="700", fill="#FFFFFF", anchor="middle")
            s.text(x + 52, y + 72, name, size=15, weight="600", anchor="middle")
            if note:
                s.text(x + 52, y + 89, note, size=11.5, fill=a["label"], anchor="middle")
        else:
            s.rect(x, y, 104, 96, "#FFFFFF", LINE, rx=12, sw=1.5, dash="6 5")
            s.text(x + 52, y + 54, "free", size=15, fill=SOFT, anchor="middle")
    s.rect(64, 428, 592, 34, st["fill"], st["stroke"], rx=10, sw=1.2)
    s.text(80, 450, "An 11th member is refused until a seat is free.", size=14, weight="600", fill=st["label"])

    # Shared conversions.
    s.rect(712, 116, 448, 360, TIERS["plan"]["fill"], TIERS["plan"]["stroke"], rx=16, sw=1.8)
    s.text(736, 152, "2 shared video conversions", size=20, weight="700")
    s.lines(736, 176, ["Shared by every member. A video that plays",
                       "without converting does not use one."], size=14, gap=19)
    for i, (who, why) in enumerate([("Sam", "for an old TV"), ("Lee", "on a slow phone connection")]):
        y = 230 + i * 70
        s.rect(736, y, 400, 56, "#FFFFFF", TIERS["plan"]["stroke"], rx=12, sw=1.5)
        s.text(756, y + 24, f"In use: {who}", size=15, weight="700")
        s.text(756, y + 44, f"converting {why}", size=13.5, fill=MUTED)
    s.rect(736, 382, 400, 74, st["fill"], st["stroke"], rx=12, sw=1.5)
    s.text(756, 408, "Ana wants a converted video too", size=15, weight="700")
    s.lines(756, 430, ["Refused until Sam or Lee stops."], size=13.5, fill=st["label"])

    # Libraries and plan.
    s.rect(40, 500, 1120, 180, p["fill"], p["stroke"], rx=16, sw=1.8)
    s.text(64, 536, "What the members can watch", size=20, weight="700")
    x = 64
    s.text(64, 568, "From the platform's starting set:", size=14, fill=MUTED)
    x = 300
    for name in ["Movies", "Series"]:
        x += s.badge(x, 550, name, {"fill": "#FFFFFF", "stroke": "#FDBA74", "label": INK}, size=14) + 10
    s.text(64, 610, "Its own library:", size=14, fill=MUTED)
    s.badge(300, 592, "Club matches", {"fill": "#FFFFFF", "stroke": o["stroke"], "label": o["label"]}, size=14)
    s.text(64, 652, "Plan for every member:", size=14, fill=MUTED)
    s.badge(300, 634, "Reseller member", TIERS["plan"], size=14)
    s.lines(620, 572, ["Members never see another organization's people",
                       "or libraries, and other organizations never see",
                       "Riverside Club's. The plan sets each member's",
                       "screens, quality and downloads."], size=14, gap=21, fill=INK)
    return s


def reseller_story():
    s = Svg(1200, 860, "Example: a year with Riverside Club",
            "A timeline of eight events in a reseller organization's year, each with the organization's state "
            "after it: bought, first admin, members added and one refused, a missed payment and its fix, a seat "
            "cut below the number of members and its fix, and cancellation.")
    s.header("Example: a year with Riverside Club",
             "What the club, the storefront and Bloem each do, and the organization's state after each step.")
    g, a, w, o = TIERS["grey"], TIERS["account"], TIERS["warn"], TIERS["org"]
    events = [
        ("January 5", "The club buys 10 seats and 2 conversions on the storefront.",
         "The storefront asks Bloem to create the organization.", "Getting ready", g),
        ("January 5", "The storefront adds the coach's account as its admin.",
         "Bloem grants the starting libraries.", "Active", a),
        ("February", "The coach adds 9 members, filling all 10 seats.",
         "An 11th member is refused: no seat is free.", "Active", a),
        ("March 1", "The club's payment fails. The storefront suspends it.",
         "Members are refused until it is lifted.", "Suspended", w),
        ("March 3", "The club pays. The storefront lifts the suspension.",
         "Everyone can watch again.", "Active", a),
        ("June", "The club changes to 6 seats while it has 10 members.",
         "Too many people for its seats: suspended straight away.", "Suspended", w),
        ("June", "The coach removes 4 members.",
         "Back within its seats, it becomes active again by itself.", "Active", a),
        ("December", "The club cancels. The storefront ends it.",
         "Members removed; accounts with no other home deleted.", "Retired", g),
    ]
    x_line, y, step = 230, 132, 86
    s.add(f'<line x1="{x_line}" y1="{y + 10}" x2="{x_line}" y2="{y + step * (len(events) - 1) + 30}" stroke="#E5E7EB" stroke-width="4"/>')
    for date, what, then, state, color in events:
        s.text(40, y + 30, date, size=16, weight="700")
        s.add(f'<circle cx="{x_line}" cy="{y + 25}" r="10" fill="{color["stroke"]}" stroke="#FFFFFF" stroke-width="3"/>')
        s.rect(258, y, 690, 70, "#FFFFFF", "#E5E7EB", rx=12)
        s.rect(258, y, 6, 70, color["stroke"], rx=3)
        s.text(280, y + 28, what, size=15.5, weight="600")
        s.text(280, y + 52, then, size=14, fill=MUTED)
        s.rect(970, y + 12, 190, 46, color["fill"], color["stroke"], rx=23, sw=2)
        s.text(1065, y + 41, state, size=16, weight="700", fill=color["label"], anchor="middle")
        y += step
    return s


PICTURES = [("tiers", tiers), ("what-can-i-watch", funnel), ("lifecycle", lifecycle), ("press-play", press_play),
            ("reseller-bought", reseller_bought), ("reseller-year", reseller_story)]
