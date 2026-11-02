"""Pictures for docs/bloem/ci.md."""
from xml.sax.saxutils import escape

from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, para

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}
MONO = "'SFMono-Regular', Menlo, Consolas, 'Liberation Mono', monospace"


def _mono(s, x, y, text, size=13, fill=SOFT, anchor="start"):
    s.add(f'<text x="{x}" y="{y}" font-family="{MONO}" font-size="{size}" fill="{fill}" '
          f'text-anchor="{anchor}">{escape(text)}</text>')


def workflows():
    s = Svg(1200, 740, "What runs, and when",
            "Top, every change: a pull request, a push to main, the Monday run or a manual run starts Bloem CI. "
            "When Bloem CI passes on a push to main, Bloem Image builds the server image tagged with that commit, "
            "and moves latest if the commit is still main's newest. Deploying is a separate step a person starts. "
            "Bottom, every day, only when switched on (off since November 2, 2026): the upstream sync merges Silo's "
            "main into a branch and opens a pull request; Bloem CI checks it; the merge gate lands it on main. A "
            "sync with conflicts opens as a draft that a person resolves.")
    s.header("What runs, and when",
             "Four Bloem workflows. Bloem CI is the gate for everything that reaches main.")
    a, p, g, w, o = TIERS["account"], TIERS["plan"], TIERS["grey"], TIERS["warn"], TIERS["org"]

    # Every change.
    s.text(40, 140, "Every change", size=17, weight="700")
    triggers = ["Pull request", "Push to main", "Monday, 04:23 UTC", "Started by hand"]
    mid = 160 + (3 * 50 + 38) / 2
    for i, t in enumerate(triggers):
        y = 160 + i * 50
        s.rect(40, y, 200, 38, "#FFFFFF", "#D1D5DB", rx=19, sw=1.4)
        s.text(140, y + 25, t, size=14.5, weight="600", anchor="middle")
        s.add(f'<line x1="240" y1="{y + 19}" x2="262" y2="{y + 19}" stroke="#9CA3AF" stroke-width="2"/>')
    s.add(f'<line x1="262" y1="179" x2="262" y2="{160 + 3 * 50 + 19}" stroke="#9CA3AF" stroke-width="2"/>')
    s.arrow(262, mid, 286, mid)
    box(s, 290, 160, 240, 190, p, "Bloem CI",
        "Go tests, lint, contract checks, database checks, and the web build and tests, side by side.",
        size=14.5)
    s.badge(308, 306, "up to 24 jobs at once", p, size=12.5)
    s.arrow(534, mid, 636, mid)
    s.lines(585, mid - 30, ["passed, on a", "push to main"], size=12.5, fill=MUTED, gap=16, anchor="middle")
    box(s, 640, 160, 240, 190, BLOEM, "Bloem Image",
        "Builds the server image for exactly that commit, with the web bundle CI built.", size=14.5)
    s.arrow(884, mid, 916, mid)
    s.rect(920, 160, 240, 190, a["fill"], a["stroke"], rx=14, sw=1.8)
    s.text(940, 192, "The image", size=17, weight="700")
    _mono(s, 940, 222, "bloem-server:", size=13, fill=INK)
    _mono(s, 940, 242, "<full commit SHA>", size=13, fill=INK)
    para(s, 940, 274, "latest moves to it only while that commit is still main's newest.", 200, size=14,
         gap=19)
    s.rect(640, 370, 520, 48, "#FFFFFF", "#9CA3AF", rx=12, sw=1.4, dash="6 5")
    s.text(660, 400, "Deploying is a separate step a person starts.", size=14.5, fill=MUTED)

    # Every day.
    s.text(40, 468, "Every day, only when switched on (off since November 2, 2026)", size=17, weight="700")
    row_y, row_h = 484, 116
    steps = [
        (g, 210, "Upstream sync", "05:20 UTC: merges Silo's main into a new branch."),
        (SILO, 210, "Pull request", "Opened for that branch."),
        (p, 190, "Bloem CI", "The same checks as above."),
        (o, 210, "Merge gate", "Five checks on the exact commit CI tested."),
        (a, 172, "main", "Merged, never squashed."),
    ]
    x = 40
    for i, (c, wdt, title, body) in enumerate(steps):
        box(s, x, row_y, wdt, row_h, c, title, body, size=14, title_size=16.5)
        if i < len(steps) - 1:
            s.arrow(x + wdt + 3, row_y + row_h / 2, x + wdt + 29, row_y + row_h / 2)
        if i == 1:
            pr_mid = x + wdt / 2
        x += wdt + 32
    s.path(f"M{pr_mid},{row_y + row_h + 2} L{pr_mid},{row_y + row_h + 34}", color=w["stroke"])
    s.rect(40, row_y + row_h + 38, 760, 64, w["fill"], w["stroke"], rx=12, sw=1.5)
    s.text(60, row_y + row_h + 66, "Conflicts?", size=15, weight="700", fill=w["label"])
    s.text(60, row_y + row_h + 88, "A draft pull request. A person resolves it on Silo's side and merges it by hand.",
           size=14, fill=INK)
    return s


def runners():
    s = Svg(1200, 700, "Where a Bloem CI job runs",
            "Three questions in order. Are the disposable runners switched on (BLOEM_SELF_HOSTED is true)? If not, "
            "the job runs on GitHub's ubuntu-24.04. Is it a run on main (a push, the Monday run or a manual run)? "
            "If so, a disposable runner. Is it a pull request whose author is listed in BLOEM_CI_MEMBERS? If so, a "
            "disposable runner; if not, GitHub's ubuntu-24.04.")
    s.header("Where a Bloem CI job runs",
             "Three questions, in order. The runner controller applies the same rule from its own settings.")
    a, g, p = TIERS["account"], TIERS["grey"], TIERS["plan"]
    rows = [
        ("Are the disposable runners switched on?", ["The repository variable BLOEM_SELF_HOSTED", "is true."],
         "No", g, "GitHub's ubuntu-24.04", "Yes"),
        ("Is it a run on main?", ["A push to main, the Monday run, or a", "manual run on main."],
         "Yes", a, "A disposable runner", "No"),
        ("Is it a pull request from a listed member?", ["The author's login is in the repository", "variable BLOEM_CI_MEMBERS."],
         "Yes", a, "A disposable runner", None),
    ]
    x, y, w, h, gap = 40, 124, 640, 100, 40
    for i, (title, body, side, color, outcome, down) in enumerate(rows):
        s.rect(x, y, w, h, p["fill"], p["stroke"], rx=14, sw=1.8)
        s.number(x + 28, y + 30, i + 1, p["stroke"])
        s.text(x + 52, y + 36, title, size=18, weight="700")
        s.lines(x + 52, y + 64, body, size=14, gap=19)
        s.arrow(x + w + 4, y + h / 2, 816, y + h / 2)
        s.text(x + w + 22, y + h / 2 - 8, side, size=14, weight="700", fill=color["label"])
        s.rect(820, y + 14, 340, h - 28, color["fill"], color["stroke"], rx=12, sw=1.6)
        s.text(990, y + h / 2 + 6, outcome, size=17, weight="700", anchor="middle")
        if down:
            s.arrow(x + w / 2, y + h + 2, x + w / 2, y + h + gap - 3)
            s.text(x + w / 2 + 12, y + h + 25, down, size=13, weight="700", fill=MUTED)
        y += h + gap
    # The last "no".
    last_bottom = y - gap
    s.arrow(x + w / 2, last_bottom + 2, x + w / 2, last_bottom + 36)
    s.text(x + w / 2 + 12, last_bottom + 25, "No", size=13, weight="700", fill=MUTED)
    s.rect(x + w / 2 - 170, last_bottom + 40, 340, 56, g["fill"], g["stroke"], rx=28, sw=1.6)
    s.text(x + w / 2, last_bottom + 74, "GitHub's ubuntu-24.04", size=17, weight="700", anchor="middle")

    footer(s, "A disposable runner takes one job, carrying its run's bloem-run-<id>-<attempt> and bloem-job-<job id> "
              "labels, then is thrown away. The cache prune always runs on GitHub's ubuntu-24.04.", y=660, fill=INK)
    return s


def merge_gate():
    s = Svg(1200, 966, "When a sync merges itself",
            "When the sync is switched on, every morning it merges Silo's main into a branch and opens a pull "
            "request. Five checks then run "
            "in order on the commit Bloem CI tested: Bloem CI passed; the branch comes from this repository; it is "
            "the one open sync pull request and not a draft; its head is the exact commit CI tested; and that "
            "commit is the untouched merge of Bloem's main and Silo's main. Any no leaves the pull request for a "
            "person. If all pass, it merges into main as a merge commit, never squashed.")
    s.header("When a sync merges itself",
             "Five checks on the exact commit Bloem CI tested. Any “no” leaves the pull request for a person.")
    a, g, o, w, st = TIERS["account"], TIERS["grey"], TIERS["org"], TIERS["warn"], TIERS["stop"]

    s.rect(40, 116, 1120, 70, g["fill"], g["stroke"], rx=14, sw=1.6)
    s.text(64, 146, "Every morning, when switched on", size=16, weight="700")
    s.text(64, 170, "The sync merges Silo's main into upstream-sync/<date>-<sha> and opens a pull request. "
                    "Bloem CI then runs on it.", size=14, fill=MUTED)

    checks = [
        ("Did Bloem CI pass?", ["Re-runs count: the gate looks again after each one."],
         (st, ["Nothing merges."])),
        ("Is the branch from this repository?", ["A pull request from a fork never merges itself."],
         (st, ["Nothing merges."])),
        ("Is it the one open sync pull request, not a draft?",
         ["A sync with conflicts opens as a draft."],
         (w, ["A person resolves it on Silo's", "side and merges by hand."])),
        ("Is its head the commit CI tested?", ["Nothing pushed since the run."],
         (w, ["Left for a person."])),
        ("Is it the untouched merge?", ["Bloem's main and Silo's main as its parents, and",
                                        "exactly the tree git merge-tree rebuilds."],
         (w, ["A hand fix or an extra commit:", "left for a person."])),
    ]
    x, y, wdt, h, gap = 40, 220, 640, 96, 32
    for i, (title, rows, no) in enumerate(checks):
        s.rect(x, y, wdt, h, o["fill"], o["stroke"], rx=14, sw=1.8)
        s.number(x + 28, y + 30, i + 1, o["stroke"])
        s.text(x + 52, y + 36, title, size=17.5, weight="700")
        s.lines(x + 52, y + 62, rows, size=14, gap=19)
        color_no, out = no
        s.arrow(x + wdt + 4, y + h / 2, 776, y + h / 2)
        s.text(x + wdt + 22, y + h / 2 - 8, "No", size=14, weight="700", fill=color_no["label"])
        s.rect(780, y + 10, 380, h - 20, color_no["fill"], color_no["stroke"], rx=12, sw=1.5)
        s.lines(800, y + h / 2 + 6 - (len(out) - 1) * 10, out, size=15, gap=20, fill=INK)
        s.arrow(x + wdt / 2, y + h + 2, x + wdt / 2, y + h + gap - 3)
        s.text(x + wdt / 2 + 12, y + h + 21, "Yes", size=13, weight="700", fill=a["label"])
        y += h + gap
    s.rect(x + wdt / 2 - 190, y, 380, 62, INK, rx=31)
    s.text(x + wdt / 2, y + 39, "Merged into main", size=20, weight="700", fill="#FFFFFF", anchor="middle")
    s.lines(780, y + 22, ["A merge commit, never a squash, so the", "next sync sees Silo's commits as merged."],
            size=14, gap=20, fill=MUTED)
    return s


PICTURES = [("workflows", workflows), ("runners", runners), ("merge-gate", merge_gate)]
