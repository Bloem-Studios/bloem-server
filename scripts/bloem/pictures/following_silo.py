"""Pictures for docs/architecture/bloem-following-silo.md."""
from xml.sax.saxutils import escape

from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, footer, para

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}
MONO = "'SFMono-Regular', Menlo, Consolas, 'Liberation Mono', monospace"


def _code(s, x, y, rows, size=14, gap=22):
    for i, (txt, color) in enumerate(rows):
        s.add(f'<text x="{x}" y="{y + i * gap}" font-family="{MONO}" font-size="{size}" fill="{color}" '
              f'xml:space="preserve">{escape(txt)}</text>')


def _line(s, x1, y1, x2, y2, color=LINE, dash=None):
    """A connector without an arrowhead, for the shared part of a branch."""
    d = f' stroke-dasharray="{dash}"' if dash else ""
    s.add(f'<line x1="{x1}" y1="{y1}" x2="{x2}" y2="{y2}" stroke="{color}" stroke-width="2"{d}/>')


def daily_flow():
    s = Svg(1200, 740, "How Bloem keeps up with Silo",
            "A merge bot takes each batch of Silo's work. Silo's main has new commits and has been quiet for 30 "
            "minutes; a free check every 10 minutes starts one batch at a time, each in its own T3 thread the owner "
            "can watch and answer; the merge takes Silo's side of every conflict. If Bloem still works, after the "
            "review list and the full local gate, the merge is pushed to Bloem's main, Bloem CI checks that exact "
            "commit, the bot deploys it, writes the receipt and sends one message. If not, Bloem adapts on its own "
            "lines with a test. When a check cannot be fixed, Bloem cannot keep working without editing Silo's "
            "lines, CI stays red or the deploy fails, the bot stops in its thread and blocks the next batch.")
    s.header("How Bloem keeps up with Silo",
             "A merge bot takes in each batch of Silo's work, checks that Bloem still works, and deploys it.")
    g, a, w, p, st = TIERS["grey"], TIERS["account"], TIERS["warn"], TIERS["plan"], TIERS["stop"]

    s.text(40, 140, "Each Silo batch", size=17, weight="700")
    first = [
        (SILO, "Silo's main", "New commits, then 30 quiet minutes. The batch is all of it."),
        (g, "The check", "Every 10 minutes, at no cost. One batch at a time."),
        (BLOEM, "One thread per batch", "Its own T3 thread, which the owner can watch and answer."),
        (g, "The merge", "Silo wins every conflict, word for word."),
    ]
    x = 40
    for i, (c, t, b) in enumerate(first):
        box(s, x, 156, 260, 120, c, t, b, size=14)
        if i < len(first) - 1:
            s.arrow(x + 264, 216, x + 282, 216)
        x += 286
    # From the merge, round to the start of the second row.
    _line(s, 1028, 278, 1028, 300)
    _line(s, 1028, 300, 130, 300)
    s.arrow(130, 300, 130, 318)

    # Decision diamond.
    s.add(f'<path d="M130,322 L220,388 L130,454 L40,388 Z" fill="{w["fill"]}" stroke="{w["stroke"]}" '
          'stroke-width="1.8"/>')
    s.text(130, 384, "Does Bloem", size=14, weight="700", anchor="middle")
    s.text(130, 402, "still work?", size=14, weight="700", anchor="middle")
    s.arrow(224, 388, 254, 388)
    s.text(239, 378, "Yes", size=13, weight="700", fill=a["label"], anchor="middle")
    second = [
        (a, 210, "Bloem's main", "Pushed once the review and the full local gate pass."),
        (p, 210, "Bloem CI", "On that exact commit. A failed job runs once more."),
        (w, 200, "Deploy", "Only a commit CI passed. A snapshot and a restore point first."),
        (a, 192, "One message", "The receipt, then deployed, stuck or rolled back."),
    ]
    x = 258
    centers = []
    for i, (c, wdt, t, b) in enumerate(second):
        box(s, x, 322, wdt, 132, c, t, b, size=14)
        centers.append(x + wdt / 2)
        if i < len(second) - 1:
            s.arrow(x + wdt + 4, 388, x + wdt + 26, 388)
        x += wdt + 30

    s.arrow(130, 458, 130, 510)
    s.text(142, 490, "No", size=13, weight="700", fill=st["label"])
    box(s, 40, 514, 330, 166, BLOEM, "Bloem adapts",
        "Where Silo's change would break Bloem, Bloem's behavior goes back on its own lines, with a test that "
        "fails without it. Then every check again.", size=14)
    s.arrow(374, 597, 456, 597, color=st["stroke"], dash="6 5")
    s.text(415, 585, "cannot fix", size=12.5, weight="700", fill=st["label"], anchor="middle")
    for cx, label in ((centers[1], "stays red"), (centers[2], "fails or rolls back")):
        s.arrow(cx, 458, cx, 510, color=st["stroke"], dash="6 5")
        s.text(cx + 10, 490, label, size=12.5, weight="700", fill=st["label"])
    box(s, 460, 514, 700, 166, st, "Stops for the owner",
        "The bot stops in its thread, writes what failed, what it tried and what it suggests, and sends the "
        "stuck message. No new batch starts until the owner answers in that thread or releases it. A change to "
        "Silo's lines is never the way out.", size=14.5)
    return s


def deploy():
    s = Svg(1200, 660, "How a batch is deployed",
            "Three steps: a snapshot of the server and a restore point in the database; a restart of about a "
            "minute, in which migrations run before the new version serves; then the checks. Three outcomes: every "
            "check passes, so the batch is deployed and the bot writes the receipt and sends deployed; a check "
            "fails, so the previous image goes back on the server and both transcode nodes and the bot sends rolled "
            "back, whether or not migrations ran; or the server stops answering during the restart, so nothing "
            "more is tried and the bot stops for the owner. The bot never restores the database.")
    s.header("How a batch is deployed",
             "Only a commit Bloem CI passed. The image can go back by itself; the database never does.")
    g, a, w, p, st = TIERS["grey"], TIERS["account"], TIERS["warn"], TIERS["plan"], TIERS["stop"]

    steps = [
        (w, "1. Safety first", "A snapshot of the server and a restore point in the database."),
        (g, "2. Restart", "Usually about a minute offline. Migrations run, then the new version serves."),
        (p, "3. Checks", "Ready, healthy, on the new version, no errors in the logs, both transcode nodes "
                         "on the new image."),
    ]
    x = 40
    for i, (c, t, b) in enumerate(steps):
        box(s, x, 124, 346, 136, c, t, b, size=14.5)
        if i < len(steps) - 1:
            s.arrow(x + 350, 192, x + 383, 192)
        x += 387
    # The checks branch into three outcomes.
    _line(s, 987, 262, 987, 290)
    _line(s, 213, 290, 987, 290)
    for cx in (213, 600, 987):
        s.arrow(cx, 290, cx, 318)

    outcomes = [
        (a, "Every check passes", "Deployed. The bot writes the receipt and sends “deployed”."),
        (st, "A check fails", "The previous image goes back on the server and both transcode nodes, by itself, "
                              "migrations or not. The bot sends “rolled back” and stops."),
        (w, "The server stops answering", "During the restart the bot cannot reach it, so nothing more is "
                                          "tried. The bot stops for the owner."),
    ]
    x = 40
    for c, t, b in outcomes:
        box(s, x, 322, 346, 176, c, t, b, size=14.5, title_size=16.5)
        x += 387

    s.rect(40, 530, 1120, 86, INK, rx=14)
    s.text(64, 564, "The bot never restores the database.", size=17, weight="700", fill="#FFFFFF")
    s.text(64, 592, "That would undo what people did since the restore point, so it is the owner's decision.",
           size=14.5, fill="#E5E7EB")
    return s


def silo_wins():
    s = Svg(1200, 760, "When Silo and Bloem change the same lines",
            "A real fix from 31 October 2026 in a household check. Left: Bloem's check folded into Silo's line. "
            "Right: Silo's line as Silo wrote it, and Bloem's check on its own lines above it. Below: where Bloem "
            "keeps its own work, and the seam list, which may only shrink.")
    s.header("When Silo and Bloem change the same lines",
             "Silo always wins. A real example from 31 October 2026, in the household check.")
    st, a = TIERS["stop"], TIERS["account"]

    # Before.
    s.text(40, 140, "Before: Bloem's check inside Silo's line", size=17, weight="700")
    s.rect(40, 156, 540, 250, "#0F172A", rx=14)
    _code(s, 64, 194, [
        ("// A remote-playback handoff never", "#94A3B8"),
        ("// manages the household.", "#94A3B8"),
        ("if bloemDirectProfileClaims(ctx) ||", "#FCA5A5"),
        ("    apimw.GetClaims(ctx).IsRemotePlayback() {", "#FCA5A5"),
        ("    return false, nil, nil", "#E2E8F0"),
        ("}", "#E2E8F0"),
    ], size=15, gap=27)
    s.arrow(586, 280, 616, 280)

    # After.
    s.text(620, 140, "After: Silo's line as Silo wrote it", size=17, weight="700")
    s.rect(620, 156, 540, 250, "#0F172A", rx=14)
    _code(s, 644, 194, [
        ("if bloemDirectProfileClaims(ctx) {", "#FDBA74"),
        ("    return false, nil, nil", "#FDBA74"),
        ("}", "#FDBA74"),
        ("// A remote-playback handoff never", "#7DD3FC"),
        ("// manages the household.", "#7DD3FC"),
        ("if apimw.GetClaims(ctx).IsRemotePlayback() {", "#7DD3FC"),
        ("    return false, nil, nil", "#7DD3FC"),
        ("}", "#7DD3FC"),
    ], size=15, gap=26)
    s.rect(644, 420, 14, 14, "#FDBA74", rx=3)
    s.text(666, 432, "Bloem's check, on its own lines", size=13.5, fill=MUTED)
    s.rect(910, 420, 14, 14, "#7DD3FC", rx=3)
    s.text(932, 432, "Silo's lines, exactly", size=13.5, fill=MUTED)
    para(s, 40, 432, "Same behavior. But a Silo line with Bloem inside it conflicts again whenever Silo changes "
         "it, and Silo changes every day.", 520, size=14, fill=INK)

    s.text(40, 510, "Where Bloem keeps its own work", size=17, weight="700")
    homes = [
        (BLOEM, "Its own files", "Files named bloem_*, and whole packages Silo does not have, such as Live TV."),
        (BLOEM, "Overlays", "Where a Silo document needs a Bloem note, a separate Bloem page carries it."),
        (st, "The seam list", "The Silo files Bloem still edits: 309 on 2 November 2026. The number may only go "
         "down."),
    ]
    for i, (c, t, b) in enumerate(homes):
        x = 40 + i * 380
        s.rect(x, 530, 360, 150, c["fill"], c["stroke"], rx=14, sw=1.6)
        s.text(x + 20, 564, t, size=17, weight="700")
        para(s, x + 20, 592, b, 320, size=14.5, gap=20)
    footer(s, "The aim: Bloem's additions sit beside Silo's code, never inside it, so taking Silo's work stays "
              "cheap.", y=724, fill=INK)
    return s


PICTURES = [("daily-flow", daily_flow), ("deploy", deploy), ("silo-wins", silo_wins)]
