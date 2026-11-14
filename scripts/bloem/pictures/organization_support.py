"""Pictures for docs/architecture/bloem-organization-support.md."""
from kit import INK, TIERS, Svg, para, no_mark, yes_mark


def who_may_act():
    s = Svg(1200, 560, "Who may act on whom",
            "A grid. Rows are four kinds of person an administrator might help: an ordinary member, another "
            "administrator, the organization owner and a platform account. Columns are three helpers: an "
            "organization administrator, the organization owner and the platform operator. Each cell says yes or no.")
    s.header("Who may act on whom",
             "Resetting a password, signing out and editing profiles follow one table. Nobody acts on themselves.")
    org, owner, plat = TIERS["org"], TIERS["org"], TIERS["platform"]
    cols = [("Organization administrator", org), ("Organization owner", owner), ("Platform operator", plat)]
    col_x, col_w, row_w = 340, 280, 280
    for i, (title, color) in enumerate(cols):
        x = col_x + i * col_w
        s.rect(x + 4, 120, col_w - 8, 50, color["fill"], color["stroke"], rx=10, sw=1.6)
        s.text(x + col_w / 2, 151, title, size=15, weight="700", anchor="middle")
    rows = [
        ("An ordinary member", "Anyone who is not an administrator", [True, True, True]),
        ("Another administrator", "Not the owner", [False, True, True]),
        ("The organization owner", "The person who runs it", [False, False, True]),
        ("A platform account", "An operator's own account", [False, False, False]),
    ]
    notes = {(3, 2): "Only where the server Owner rule allows"}
    y, h = 190, 84
    for r, (title, sub, cells) in enumerate(rows):
        s.rect(40, y, row_w, h - 10, "#FFFFFF", "#E5E7EB", rx=12)
        s.text(60, y + 36, title, size=16, weight="700")
        para(s, 60, y + 58, sub, row_w - 40, size=13.5, gap=18)
        for c, allowed in enumerate(cells):
            x = col_x + c * col_w
            s.rect(x + 4, y, col_w - 8, h - 10, "#FFFFFF", "#E5E7EB", rx=10)
            (yes_mark if allowed else no_mark)(s, x + 36, y + 37)
            if (r, c) in notes:
                para(s, x + 62, y + 31, notes[(r, c)], col_w - 84, size=13, gap=17, fill=INK)
        y += h
    s.text(60, 548 - 26, "A refusal is a 409 with a reason, never a 403: apps read a 403 as losing the organization.",
           size=14, fill=INK)
    return s


PICTURES = [("who-may-act", who_may_act)]
