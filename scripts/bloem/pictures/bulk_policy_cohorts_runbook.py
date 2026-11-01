"""Pictures for docs/operations/bulk-policy-cohorts-runbook.md."""
from kit import TIERS, Svg, card, footer


def canary():
    s = Svg(1200, 600, "Releasing bulk policy changes with one test account",
            "Two rows of steps. Prepare: freeze the reviewed build, check the way back, deploy and check the build. "
            "Then the canary loop on one test account: record its state, apply one plan, check its playback and "
            "downloads, put it back, and compare with the recorded state. The release goes wider only when the "
            "account is back where it started and every check passed.")
    s.header("Releasing bulk policy changes with one test account",
             "Change one account, check it, put it back exactly. Only then use the feature more widely.")
    o, p, a, w, pl = TIERS["org"], TIERS["platform"], TIERS["account"], TIERS["warn"], TIERS["plan"]

    s.text(40, 140, "Prepare", size=17, weight="700")
    prep = [
        (p, "Freeze the build", "One reviewed commit, every test suite passed, a database backup, an image tied to it."),
        (w, "Check the way back", "No job running, and whether a derived cohort already blocks a schema rollback."),
        (p, "Deploy and check", "Health and ready answer, and the server reports the reviewed commit."),
    ]
    cw, gap = 356, 26
    for i, (color, title, body) in enumerate(prep):
        x = 40 + i * (cw + gap)
        card(s, x, 156, cw, 132, color, title, body, n=i + 1, size=14, title_size=16.5)
        if i < len(prep) - 1:
            s.arrow(x + cw + 2, 222, x + cw + gap - 2, 222)

    s.text(40, 334, "The canary loop, on one test account", size=17, weight="700")
    loop = [
        (o, "Record", "Its group, cohort, profiles and a digest of its policy."),
        (pl, "Apply", "One exact plan revision, after a preview that shows 1 matched, 0 left out."),
        (a, "Check", "Read it back, play a title, try a download, read the audit."),
        (pl, "Put it back", "A new preview to the exact earlier cohort, with a new key."),
        (a, "Compare", "Same policy digest and profiles as recorded."),
    ]
    lw, lgap = 206, 22
    for i, (color, title, body) in enumerate(loop):
        x = 40 + i * (lw + lgap)
        card(s, x, 350, lw, 168, color, title, body, n=i + 4, size=14, title_size=16)
        if i < len(loop) - 1:
            s.arrow(x + lw + 2, 434, x + lw + lgap - 2, 434)

    footer(s, "Stop at any failed check. Go wider only when every suite passed on this exact build, both jobs "
              "finished as expected and the account is back where it started.", y=560)
    return s


PICTURES = [("canary", canary)]
