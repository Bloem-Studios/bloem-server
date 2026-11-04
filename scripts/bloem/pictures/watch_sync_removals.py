"""Pictures for docs/architecture/bloem-watch-sync-removals.md."""
from kit import INK, MUTED, TIERS, Svg, para, yes_mark


def flow():
    s = Svg(1200, 500, "How a removal in a tracker reaches one profile's watch history",
            "Four stages from left to right: Robin removes a show in the tracker; the tracker keeps a note of the "
            "removal for Robin's profile; the tracker's plugin passes it on Robin's own connection; Bloem clears "
            "the show from Robin's watch history. Below, three checks: Sam's profile on the same account keeps "
            "its history, an episode Robin watched again after the removal stays, and repeating the note changes "
            "nothing.")
    s.header("How a removal in a tracker reaches one profile's watch history",
             "Every step carries one profile. Nothing else on the account changes.")
    o, a, pl, g = TIERS["org"], TIERS["account"], TIERS["plan"], TIERS["grey"]
    stages = [
        (pl, "Robin removes a show", "In the tracker, Robin deletes the show from their library."),
        (g, "The tracker keeps a note", "One note: this title, this profile, removed at this time."),
        (o, "Robin's connection", "The plugin passes the note on with the profile's next sync."),
        (a, "Robin's watch history", "Bloem clears the show's episodes watched up to that time."),
    ]
    x0, y0, cw, ch, gap = 40, 128, 260, 150, 26
    for i, (c, t, b) in enumerate(stages):
        x = x0 + i * (cw + gap)
        s.rect(x, y0, cw, ch, c["fill"], c["stroke"], rx=14)
        s.number(x + 26, y0 + 28, i + 1, c["stroke"])
        s.text(x + 16, y0 + 74, t, size=17, weight="700")
        para(s, x + 16, y0 + 102, b, cw - 30, size=14, gap=19)
        if i < len(stages) - 1:
            s.arrow(x + cw + 3, y0 + ch / 2, x + cw + gap - 3, y0 + ch / 2)

    s.rect(40, 304, 1120, 156, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(62, 338, "What stays", size=16, weight="700", fill=INK)
    checks = [
        ("Sam's history", "Sam is another profile on the same account. Sam's watches of the show stay."),
        ("A later watch", "An episode Robin watched again in Bloem after the removal stays."),
        ("A repeated note", "The tracker repeats the note on every sync. After the first, nothing changes."),
    ]
    for i, (title, body) in enumerate(checks):
        x = 62 + i * 368
        yes_mark(s, x + 12, 376)
        s.text(x + 34, 381, title, size=15, weight="700", fill=INK)
        para(s, x, 412, body, 330, size=14, gap=19, fill=MUTED)
    return s


PICTURES = [("flow", flow)]
