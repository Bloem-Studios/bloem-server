"""Pictures for docs/architecture/multitenant-administration.md."""
from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, card, footer, no_mark, para, yes_mark


def contexts():
    s = Svg(1200, 752, "Two contexts in one admin console",
            "Four steps: an administrator signs in, chooses the Platform or one Organization context, gets a pass "
            "that lasts at most 15 minutes and is tied to that sign-in, and every request checks the pass again. "
            "Below, what each context manages, and the changes that end a pass.")
    s.header("Two contexts in one admin console",
             "An administrator chooses which hat to wear. The console never mixes them.")
    p, o, a, st, g = TIERS["platform"], TIERS["org"], TIERS["account"], TIERS["stop"], TIERS["grey"]

    steps = [
        (a, "Sign in", "With the account's email and password, as anywhere in Bloem."),
        (g, "Choose a context", "Platform, or one organization the account administers."),
        (o, "Get a pass", "It names the context, lasts at most 15 minutes and is tied to that sign-in."),
        (p, "Every request checks", "The sign-in, the account, the organization and the membership, again."),
    ]
    x, y, w, h, gap = 40, 124, 262, 132, 24
    for i, (c, title, body) in enumerate(steps):
        bx = x + i * (w + gap)
        card(s, bx, y, w, h, c, title, body, n=i + 1, size=14, title_size=16.5)
        if i < len(steps) - 1:
            s.arrow(bx + w + 2, y + h / 2, bx + w + gap - 2, y + h / 2)

    # The two contexts.
    s.rect(40, 288, 548, 272, p["fill"], p["stroke"], rx=16, sw=1.8)
    s.text(64, 324, "Platform context", size=19, weight="700")
    s.text(64, 348, "For platform administrators (the admin role).", size=14, fill=MUTED)
    rows = ["Create, rename, suspend and reactivate organizations",
            "Memberships, and ownership transfers (with a password)",
            "Platform libraries and the grants made to each organization",
            "Plans, reseller organizations, domains and branding"]
    for i, r in enumerate(rows):
        yes_mark(s, 74, 382 + i * 46, r=10, color=p["stroke"])
        para(s, 94, 387 + i * 46, r, 470, size=14.5, fill=INK)

    s.rect(612, 288, 548, 272, o["fill"], o["stroke"], rx=16, sw=1.8)
    s.text(636, 324, "Organization context", size=19, weight="700")
    s.text(636, 348, "For one organization's administrators. Only that organization.", size=14, fill=MUTED)
    rows = ["People, their profiles and access groups",
            "Its own libraries, and grants already made to it",
            "Invitations, recorded activity and policy explanations",
            "Bulk changes, with exact counts of what changed"]
    for i, r in enumerate(rows):
        yes_mark(s, 646, 382 + i * 46, r=10, color=o["stroke"])
        para(s, 666, 387 + i * 46, r, 470, size=14.5, fill=INK)

    # What ends a pass.
    s.rect(40, 584, 1120, 128, st["fill"], st["stroke"], rx=14, sw=1.5)
    s.text(64, 616, "A pass stops working at once when", size=17, weight="700", fill=st["label"])
    ends = ["the sign-in ends, expires or is revoked", "the organization or membership is suspended",
            "the account is disabled or replaced", "ownership changes, or a revision moves on"]
    for i, e in enumerate(ends):
        cx = 64 + (i % 2) * 560
        cy = 648 + (i // 2) * 34
        no_mark(s, cx + 10, cy - 5, r=9)
        s.text(cx + 30, cy, e, size=15, fill=INK)
    return s


def silo_apps():
    s = Svg(1200, 640, "How an app built for Silo finds its organization",
            "Apps built for Silo know nothing about organizations. Before a profile is picked and after, Bloem "
            "answers from the account's one organization. An account with no membership gets the default "
            "organization's answer from before organizations existed.")
    s.header("How an app built for Silo finds its organization",
             "The app only knows an account and, later, a profile. Bloem works out the rest.")
    a, o, f, g, w = TIERS["account"], TIERS["org"], TIERS["profile"], TIERS["grey"], TIERS["warn"]

    # Row 1: a member account.
    s.text(40, 142, "An account in an organization", size=17, weight="700")
    box(s, 40, 160, 300, 112, a, "Before picking a profile",
        "The app browses with the account alone.", size=14, title_size=16)
    box(s, 40, 292, 300, 112, f, "After picking a profile",
        "The app now names the profile too.", size=14, title_size=16)
    s.arrow(344, 216, 470, 260)
    s.arrow(344, 348, 470, 304)
    s.rect(474, 222, 330, 120, o["fill"], o["stroke"], rx=16, sw=2)
    s.text(639, 262, "The account's one", size=18, weight="700", anchor="middle")
    s.text(639, 286, "organization", size=18, weight="700", anchor="middle")
    s.text(639, 314, "Same answer both times", size=14, fill=MUTED, anchor="middle")
    s.arrow(808, 282, 866, 282)
    box(s, 870, 214, 290, 136, g, "Its libraries only",
        "Never a mix of two organizations, and never an empty answer that looks like a locked-out user.",
        size=14, title_size=16)

    # Row 2: no membership.
    s.text(40, 466, "An account with no membership", size=17, weight="700")
    box(s, 40, 484, 300, 96, w, "Account alone",
        "No organization holds it.", size=14, title_size=16)
    s.arrow(344, 532, 470, 532)
    s.rect(474, 484, 330, 96, TIERS["platform"]["fill"], TIERS["platform"]["stroke"], rx=16, sw=2)
    s.text(639, 524, "The default organization", size=17, weight="700", anchor="middle")
    s.text(639, 550, "as before organizations existed", size=14, fill=MUTED, anchor="middle")
    footer(s, "Picking a profile never moves an account to another organization.", y=616, fill=INK)
    return s


def group_deletion():
    s = Svg(1200, 600, "Deleting an access group",
            "Before: the Teens group holds two profiles and one account. After deletion in Bloem, all three move "
            "to the organization's default group in one step and take its limits. In Silo, the account would be "
            "left in no group, which reads as no restrictions, so deleting a group would widen what it can reach.")
    s.header("Deleting an access group",
             "Its people move to the organization's default group. Deleting never widens access.")
    o, f, a, st, g = TIERS["org"], TIERS["profile"], TIERS["account"], TIERS["stop"], TIERS["grey"]

    def group(x, y, w, title, color, members, dashed=False):
        s.rect(x, y, w, 200, color["fill"], color["stroke"], rx=16, sw=1.8, dash="6 5" if dashed else None)
        s.text(x + 22, y + 36, title, size=17, weight="700")
        for i, (label, c) in enumerate(members):
            s.rect(x + 22, y + 56 + i * 44, w - 44, 36, "#FFFFFF", c["stroke"], rx=10, sw=1.3)
            s.text(x + 38, y + 80 + i * 44, label, size=14.5, weight="600")

    people = [("Lee (profile)", f), ("Sam's Kids (profile)", f), ("sam@riverside.example", a)]
    s.text(40, 140, "Before", size=15, weight="700", fill=SOFT, spacing="0.5")
    group(40, 152, 300, "Group: Teens", o, people)
    s.arrow(346, 252, 410, 252)
    s.text(378, 238, "delete", size=13, fill=SOFT, anchor="middle")

    s.text(420, 140, "In Bloem", size=15, weight="700", fill=SOFT, spacing="0.5")
    group(420, 152, 340, "Default group", o, people)
    yes_mark(s, 736, 176)
    para(s, 420, 386, "All three move in one step, with the default group's limits. The default group itself "
         "cannot be deleted.", 340, size=14, fill=INK)

    s.text(820, 140, "In Silo, for comparison", size=15, weight="700", fill=SOFT, spacing="0.5")
    group(820, 152, 340, "No group", g, [people[2]], dashed=True)
    no_mark(s, 1136, 176)
    para(s, 820, 386, "Silo leaves the group's accounts in no group, which reads as “no restrictions”: "
         "deleting the group would widen what they can reach.", 340, size=14, fill=INK)

    s.rect(40, 476, 1120, 84, st["fill"], st["stroke"], rx=14, sw=1.5)
    s.text(64, 508, "An administrative delete must never be a privilege grant.", size=17, weight="700",
           fill=st["label"])
    s.text(64, 536, "Bloem keeps this even though it differs from Silo. A merge from Silo must not undo it.",
           size=14.5, fill=INK)
    return s


PICTURES = [("contexts", contexts), ("silo-apps", silo_apps), ("group-deletion", group_deletion)]
