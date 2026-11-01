"""Pictures for docs/operations/bulk-policy-cohorts.md."""
from kit import MUTED, TIERS, Svg, box, card, footer


def review_and_apply():
    s = Svg(1200, 600, "Changing many accounts' plans at once",
            "Six numbered steps: pick the accounts, which freezes the list; choose one change; read the preview; "
            "confirm within 15 minutes; Bloem runs the change in batches and checks each account again; then "
            "read the accounts back. Below, the four kinds of change.")
    s.header("Changing many accounts' plans at once",
             "Pick, choose, preview, confirm. Bloem then applies exactly what you reviewed, and nothing else.")
    o, pl, a, w = TIERS["org"], TIERS["plan"], TIERS["account"], TIERS["warn"]

    steps = [
        (o, "Pick", "Up to 10,000 accounts. The list is frozen: later matches are not added."),
        (pl, "Choose", "Exactly one change. Nothing is chosen for you."),
        (pl, "Preview", "Who changes, every field that changes, and which profiles move."),
        (w, "Confirm", "Within 15 minutes, and only if nothing changed meanwhile."),
        (a, "Apply", "Bloem works in batches and checks each account again. A changed one is skipped."),
        (a, "Check", "Read the accounts again. Don't trust the first answer alone."),
    ]
    cw, gap = 168, 22
    for i, (color, title, body) in enumerate(steps):
        x = 40 + i * (cw + gap)
        card(s, x, 120, cw, 210, color, title, body, n=i + 1, size=14, title_size=16)
        if i < len(steps) - 1:
            s.arrow(x + cw + 2, 228, x + cw + gap - 2, 228)

    s.text(40, 384, "The four kinds of change", size=18, weight="700")
    kinds = [
        ("Move to an existing cohort", "A policy this organization already uses."),
        ("Apply a plan revision", "One exact revision of an enabled plan."),
        ("Derive a new policy", "An existing cohort with a few stated changes."),
        ("Restore the default", "The organization's protected default policy."),
    ]
    kw = 262
    for i, (title, body) in enumerate(kinds):
        x = 40 + i * (kw + 24)
        box(s, x, 402, kw, 110, pl, title, body, size=14, title_size=15.5)

    footer(s, "A cohort never changes once made. Accounts that match the same filter later are not changed: this "
              "is a one-off move, not a rule.", y=556)
    return s


def profiles():
    s = Svg(1200, 520, "What happens to profiles",
            "An account on policy A with three profiles: two follow the account and one, Kids, has a policy of its "
            "own. After moving the account to policy B, the two that follow it move to B and Kids stays on its "
            "own policy, unless Move custom profiles too was ticked.")
    s.header("What happens to profiles",
             "Profiles that follow the account move with it. A profile with its own policy stays, unless you say so.")
    a, f, pl, g = TIERS["account"], TIERS["profile"], TIERS["plan"], TIERS["grey"]

    def household(x, title, account_policy, rows):
        s.rect(x, 124, 480, 290, a["fill"], a["stroke"], rx=16, sw=1.6)
        s.text(x + 24, 160, title, size=18, weight="700")
        s.badge(x + 456, 140, account_policy, pl, size=13, anchor="end")
        for i, (name, policy, note, colour) in enumerate(rows):
            y = 186 + i * 72
            s.rect(x + 24, y, 432, 58, f["fill"], f["stroke"], rx=12, sw=1.2)
            s.text(x + 44, y + 25, name, size=16, weight="700")
            s.text(x + 44, y + 46, note, size=13.5, fill=MUTED)
            s.badge(x + 440, y + 15, policy, colour, size=13, anchor="end")

    household(40, "Before", "Account: policy A", [
        ("Anna", "policy A", "follows the account", pl),
        ("Tom", "policy A", "follows the account", pl),
        ("Kids", "Kids policy", "a policy of its own (custom)", g),
    ])
    s.arrow(528, 268, 676, 268, width=3)
    s.text(602, 252, "Move to B", size=15, weight="700", anchor="middle")
    household(680, "After", "Account: policy B", [
        ("Anna", "policy B", "moved with the account", pl),
        ("Tom", "policy B", "moved with the account", pl),
        ("Kids", "Kids policy", "stayed (box not ticked)", g),
    ])

    footer(s, "Tick “Move custom profiles too” to move Kids as well. It is off unless you turn it on.",
           y=470, size=15)
    return s


PICTURES = [("review-and-apply", review_and_apply), ("profiles", profiles)]
