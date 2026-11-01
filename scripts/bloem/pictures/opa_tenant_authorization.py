"""Pictures for docs/architecture/opa-tenant-authorization.md."""
from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, card, footer, no_mark, para, yes_mark


def decision():
    s = Svg(1200, 640, "How Bloem decides what someone may reach",
            "Four steps: Bloem works out who is asking from the sign-in, loads that organization's facts from the "
            "database, asks the built-in policy engine, and applies the answer in the database query that reads "
            "the catalog or starts playback. Below, the failures that refuse the request instead of guessing.")
    s.header("How Bloem decides what someone may reach",
             "The database holds the facts, the policy engine decides, and the query applies the answer.")
    a, o, pl, p, st = TIERS["account"], TIERS["org"], TIERS["plan"], TIERS["platform"], TIERS["stop"]

    steps = [
        (a, "Who is asking?", "The account, the profile and their organization, worked out by the server, "
                              "never taken from the request."),
        (o, "Load the facts", "Organization, membership, the profile's group, who owns each library and which "
                              "grants are active."),
        (pl, "Ask the policy engine", "Silo's built-in engine (OPA) gets the facts and returns what is allowed."),
        (p, "Apply it in the query", "The catalog and playback queries read only what the answer allows."),
    ]
    x, y, w, h, gap = 40, 124, 262, 150, 24
    for i, (c, title, body) in enumerate(steps):
        bx = x + i * (w + gap)
        card(s, bx, y, w, h, c, title, body, n=i + 1, size=14, title_size=16.5)
        if i < len(steps) - 1:
            s.arrow(bx + w + 2, y + h / 2, bx + w + gap - 2, y + h / 2)

    s.rect(40, 300, 1120, 300, st["fill"], st["stroke"], rx=16, sw=1.6)
    s.text(64, 336, "Any doubt refuses the request", size=19, weight="700", fill=st["label"])
    s.text(64, 362, "Bloem never guesses and never falls back to a looser check.", size=14.5, fill=INK)
    fails = [
        "A fact is missing, or belongs to someone else",
        "The membership or organization changed since the sign-in",
        "Who owns a library cannot be worked out",
        "The ownership tables cannot be read",
        "The policy engine is slow, fails or gives an odd answer",
        "A hidden or foreign library: it looks the same as a missing one",
    ]
    for i, f in enumerate(fails):
        cx = 64 + (i % 2) * 548
        cy = 410 + (i // 2) * 58
        no_mark(s, cx + 12, cy - 5, r=11)
        s.text(cx + 34, cy, f, size=15, fill=INK)
    s.text(64, 580, "Repeated refusals of this kind are an incident: stop the traffic, keep the evidence, repair, "
                    "and run the checks again.", size=14, fill=MUTED)
    return s


def scope():
    s = Svg(1200, 624, "What an organization's people can reach",
            "Libraries the organization owns plus platform libraries with an active grant form the upper bound. "
            "Missing, suspended or revoked grants and other organizations' libraries are left out. The policy then "
            "narrows the bound by account, group, profile, disabled libraries and custom rules, and no rule can add "
            "a library outside it.")
    s.header("What an organization's people can reach",
             "An upper bound from the database, narrowed by policy. A rule can never add to it.")
    p, o, a, f, g, st = (TIERS["platform"], TIERS["org"], TIERS["account"], TIERS["profile"], TIERS["grey"],
                         TIERS["stop"])

    # Left: the upper bound.
    s.rect(40, 120, 540, 420, o["fill"], o["stroke"], rx=16, sw=1.8)
    s.text(64, 156, "1. The upper bound", size=18, weight="700")
    s.text(64, 180, "One database query, for one organization.", size=14, fill=MUTED)
    ins = [("Libraries it owns", o), ("Platform libraries with an active grant", p)]
    outs = ["A platform library with no grant", "A suspended or revoked grant",
            "Another organization's library, always"]
    y = 214
    for label, c in ins:
        yes_mark(s, 78, y + 18, r=11)
        s.rect(100, y, 456, 36, "#FFFFFF", c["stroke"], rx=10, sw=1.4)
        s.text(116, y + 24, label, size=15, weight="600")
        y += 48
    s.text(64, y + 20, "Left out", size=12.5, weight="700", fill=SOFT, spacing="1")
    y += 34
    for label in outs:
        no_mark(s, 78, y + 18, r=11)
        s.rect(100, y, 456, 36, "#FFFFFF", LINE, rx=10, sw=1.2, dash="6 5")
        s.text(116, y + 24, label, size=15, fill=MUTED)
        y += 48

    s.arrow(586, 330, 640, 330)

    # Right: narrowing.
    s.rect(646, 120, 514, 420, a["fill"], a["stroke"], rx=16, sw=1.8)
    s.text(670, 156, "2. Narrowed by policy", size=18, weight="700")
    s.text(670, 180, "Each of these can only take libraries away.", size=14, fill=MUTED)
    narrow = ["The account's own settings", "The profile's access group", "The profile's limits",
              "Libraries switched off", "Custom rules"]
    y = 214
    for label in narrow:
        s.rect(670, y, 466, 36, "#FFFFFF", a["stroke"], rx=10, sw=1.2)
        s.text(686, y + 24, label, size=15, weight="600")
        y += 48
    s.rect(670, y + 8, 466, 50, INK, rx=25)
    s.text(903, y + 40, "What the person can reach", size=17, weight="700", fill="#FFFFFF", anchor="middle")

    footer(s, "Owning nothing and holding no grant means reaching nothing: platform ownership alone is not access.",
           y=584, fill=INK)
    return s


PICTURES = [("decision", decision), ("scope", scope)]
