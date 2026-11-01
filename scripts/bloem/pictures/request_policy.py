"""Pictures for docs/architecture/bloem-request-policy.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, footer, no_mark, para, yes_mark


def whose_rules():
    s = Svg(1200, 700, "Whose request rules apply",
            "A movie or series request is checked against the signed-in account, the chosen profile and the "
            "profile's organization together, in one moment. The answer then comes from the account's own setting "
            "if it has one, else the profile's group in that organization, else the server's defaults. All of an "
            "account's profiles share one count.")
    s.header("Whose request rules apply",
             "Bloem checks who is asking, then reads the rules in a fixed order. The first one set wins.")
    o, a, f, p, g, st = TIERS["org"], TIERS["account"], TIERS["profile"], TIERS["plan"], TIERS["grey"], TIERS["stop"]

    # Step 1: who is asking.
    s.text(40, 140, "1. Check who is asking, all at once", size=17, weight="700")
    s.rect(40, 156, 520, 300, "#FFFFFF", "#E5E7EB", rx=16)
    who = [
        (a, "The account", "Signed in and switched on."),
        (f, "The profile", "Chosen in the app, and belongs to this account."),
        (o, "The organization", "The profile's organization, and it is active."),
    ]
    for i, (c, t, b) in enumerate(who):
        y = 176 + i * 82
        s.rect(60, y, 480, 68, c["fill"], c["stroke"], rx=12, sw=1.5)
        yes_mark(s, 88, y + 34, r=11, color=c["stroke"])
        s.text(112, y + 29, t, size=16, weight="700")
        s.text(112, y + 52, b, size=14, fill=MUTED)
    no_mark(s, 82, 432, r=10)
    s.text(102, 437, "Anything changed since sign-in? Refused.", size=14, weight="600", fill=st["label"])
    s.arrow(564, 300, 636, 300)

    # Step 2: the order.
    s.text(640, 140, "2. Read the rules, first one set wins", size=17, weight="700")
    order = [
        (a, "The account's own setting", "For example, this account may ask for 20 a month."),
        (p, "The profile's group", "Usually its plan, in that organization."),
        (g, "The server's defaults", "Used when nothing above is set."),
    ]
    for i, (c, t, b) in enumerate(order):
        y = 156 + i * 110
        s.rect(640, y, 520, 80, c["fill"], c["stroke"], rx=14, sw=1.6)
        s.number(668, y + 40, i + 1, c["stroke"])
        s.text(694, y + 34, t, size=17, weight="700")
        s.text(694, y + 58, b, size=14, fill=MUTED)
        if i < len(order) - 1:
            s.arrow(900, y + 82, 900, y + 108)
            s.text(914, y + 100, "if not set", size=12.5, fill=SOFT)

    # Step 3: one count.
    s.rect(40, 488, 1120, 150, f["fill"], f["stroke"], rx=16, sw=1.6)
    s.text(64, 524, "3. One count per account", size=17, weight="700")
    para(s, 64, 552, "Every profile on the account draws from the same count. Switching profiles never resets it. "
         "Organization administrators are not held to their group's limits, but a setting on their own account "
         "still applies.", 560, size=14.5, gap=20)
    names = ["Ana", "Ben", "Kids"]
    for i, n in enumerate(names):
        cx = 760 + i * 100
        s.add(f'<circle cx="{cx}" cy="530" r="24" fill="#FFFFFF" stroke="{f["stroke"]}" stroke-width="2"/>')
        s.text(cx, 535, n, size=13, weight="700", fill=f["label"], anchor="middle")
        s.arrow(cx, 556, cx, 584)
    s.rect(720, 588, 280, 38, "#FFFFFF", f["stroke"], rx=10, sw=1.6)
    s.text(860, 613, "One count: 7 of 20 used this month", size=14, weight="700", anchor="middle")
    footer(s, "Audiobook and ebook requests follow the same order, but each kind keeps its own count.",
           y=672, size=14, fill=MUTED)
    return s


PICTURES = [("whose-rules", whose_rules)]
