"""Pictures for docs/architecture/bloem-native-storage.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

SOURCE = {"fill": "#F5F3FF", "stroke": "#8B5CF6", "label": "#6D28D9"}


def scan():
    s = Svg(1200, 600, "How a storage scan works",
            "Four steps: check that the source is still allowed and ask the plugin to describe it; start the scan "
            "journal or pick up where it stopped; list up to 512 files at a time, skip what a folder scan would skip "
            "and add each page to the catalog; finish by working out what is gone and tidying up. A failed scan "
            "resumes from its journal.")
    s.header("How a storage scan works",
             "Bloem keeps a journal of every page it lists, so a scan that stops can pick up where it left off.")
    o, a, w = TIERS["org"], TIERS["account"], TIERS["warn"]
    steps = [
        (SOURCE, "Check first", "Is the source switched on and still allowed for this library? Bloem starts the "
         "plugin and asks it to describe the source."),
        (o, "Start or resume", "Bloem opens the scan journal. A scan that stopped part-way carries on from the last "
         "page it finished."),
        (o, "A page at a time", "Up to 512 files a page. Samples, extras and system folders are skipped. Each page "
         "joins the catalog in one go."),
        (a, "Finish", "When every folder is listed, Bloem works out what is gone, marks it missing and tidies up."),
    ]
    x0, y0, cw, ch, gap = 40, 128, 258, 216, 29
    for i, (c, t, b) in enumerate(steps):
        x = x0 + i * (cw + gap)
        s.rect(x, y0, cw, ch, c["fill"], c["stroke"], rx=14)
        s.number(x + 28, y0 + 30, i + 1, c["stroke"])
        s.text(x + 18, y0 + 78, t, size=18, weight="700")
        para(s, x + 18, y0 + 108, b, cw - 34, size=14.5, gap=20)
        if i < len(steps) - 1:
            s.arrow(x + cw + 3, y0 + ch / 2, x + cw + gap - 3, y0 + ch / 2)

    # The loop back over the pages of step 3.
    lx = x0 + 2 * (cw + gap)
    s.path(f"M {lx + cw - 40} {y0 + ch + 4} C {lx + cw - 40} {y0 + ch + 44}, {lx + 40} {y0 + ch + 44}, "
           f"{lx + 40} {y0 + ch + 8}")
    s.text(lx + cw / 2, y0 + ch + 58, "next page", size=13.5, fill=SOFT, anchor="middle")

    # Failure: resume from the journal.
    s.rect(40, 416, 1120, 140, w["fill"], w["stroke"], rx=14, sw=1.5)
    s.text(62, 450, "When something goes wrong", size=17, weight="700", fill=w["label"])
    rows = [
        "A page that fails because the storage was briefly unavailable is listed again after 1, 2 and 4 seconds.",
        "A scan that still fails tries again by itself, up to five times, waiting a little longer each time.",
        "If one source fails, the library's other folders and sources are still scanned.",
    ]
    y = 480
    for r in rows:
        s.add(f'<circle cx="70" cy="{y - 5}" r="4" fill="{SOFT}"/>')
        s.text(84, y, r, size=14.5, fill=INK)
        y += 26
    return s


def gone():
    s = Svg(1200, 520, "When a file disappears",
            "A made-up example over three scans. Monday's listing has three files, A, B and C. Tuesday's complete "
            "listing leaves out C, so Bloem only suspects it has gone and keeps it. Wednesday's complete listing "
            "leaves it out again, so Bloem marks C missing. A file the source reports as removed is marked missing "
            "at once.")
    s.header("When a file disappears",
             "A file that is only left out of a listing is marked missing after a second complete scan.")
    a, w, st, g = TIERS["account"], TIERS["warn"], TIERS["stop"], TIERS["grey"]
    days = [
        ("Monday", "Lists A, B, C", [("A", a), ("B", a), ("C", a)], "All three are in the catalog.", None),
        ("Tuesday", "Lists A, B", [("A", a), ("B", a), ("C", w)], "C is left out. Bloem suspects it has gone, "
         "but keeps it: the listing may have missed it.", "suspected"),
        ("Wednesday", "Lists A, B", [("A", a), ("B", a), ("C", st)], "C is left out again. Bloem marks it "
         "missing, then clears it with the library's grace period.", "missing"),
    ]
    x0, y0, cw, ch, gap = 40, 128, 350, 300, 35
    for i, (day, listed, files, note, tag) in enumerate(days):
        x = x0 + i * (cw + gap)
        s.rect(x, y0, cw, ch, "#FFFFFF", "#E5E7EB", rx=14)
        s.text(x + 22, y0 + 38, day, size=19, weight="700")
        s.text(x + 22, y0 + 64, listed, size=14.5, fill=MUTED)
        for j, (name, c) in enumerate(files):
            fx = x + 22 + j * 104
            s.rect(fx, y0 + 88, 90, 72, c["fill"], c["stroke"], rx=10, sw=1.6)
            s.text(fx + 45, y0 + 132, name, size=22, weight="700", fill=c["label"], anchor="middle")
        if tag == "suspected":
            s.text(x + 22 + 2 * 104 + 45, y0 + 186, "suspected", size=13, weight="600", fill=w["label"],
                   anchor="middle")
        if tag == "missing":
            s.text(x + 22 + 2 * 104 + 45, y0 + 186, "missing", size=13, weight="600", fill=st["label"],
                   anchor="middle")
        para(s, x + 22, y0 + 226, note, cw - 44, size=14.5, fill=INK, gap=20)
        if i < len(days) - 1:
            s.arrow(x + cw + 4, y0 + ch / 2, x + cw + gap - 4, y0 + ch / 2)
    footer(s, "When the source reports a file as removed, Bloem marks it missing at once. A file that comes back "
              "before it is cleared is restored.", y=478)
    return s


def covers():
    s = Svg(1200, 620, "Where a stored book's cover comes from",
            "An app asks for a cover with a signed address that stays the same for a week. Bloem first checks that "
            "the viewer may still see the book; if not, there is no cover. Then it serves a kept copy, or a copy "
            "from the optional cover cache, or reads the cover once from the source, at most 16 at a time on each "
            "server, and keeps a copy.")
    s.header("Where a stored book's cover comes from",
             "Every request is checked against the viewer first. The source is read only once per cover.")
    o, a, f, st, p = TIERS["org"], TIERS["account"], TIERS["profile"], TIERS["stop"], TIERS["platform"]

    box(s, 40, 128, 300, 112, f, "The app asks", "With a signed cover address. It stays the same for a week, "
        "so apps keep the cover.", size=14.5)
    s.arrow(344, 184, 396, 184)
    box(s, 400, 128, 360, 112, o, "May this viewer see the book?", "Checked on every request, before any copy "
        "is served.", size=14.5)
    s.arrow(764, 184, 816, 184)
    s.text(780, 174, "No", size=14, weight="700", fill=st["label"])
    box(s, 820, 128, 340, 112, st, "No cover", "A hidden or removed book, or a viewer whose access was taken "
        "away, gets none.", size=14.5)

    s.path("M 580 244 L 580 266 L 215 266 L 215 300")
    s.text(594, 260, "Yes", size=14, weight="700", fill=a["label"])

    tiers = [
        (p, "1. A kept copy", "Bloem keeps each cover anyone has viewed in artwork storage. Served from there."),
        (TIERS["plan"], "2. The cover cache", "An optional Redis of its own. Checked when there is no kept copy."),
        (SOURCE, "3. Read from the source", "Once, shared by everyone asking at that moment. At most 16 at a time "
         "on each server. Then a copy is kept."),
    ]
    x = 40
    for i, (c, t, b) in enumerate(tiers):
        box(s, x, 304, 350, 124, c, t, b, size=14.5)
        if i < 2:
            s.arrow(x + 354, 366, x + 381, 366)
        x += 385
    s.text(40, 460, "Bloem tries them in this order and stops at the first that has the cover.", size=14.5,
           fill=MUTED)

    s.rect(40, 484, 1120, 100, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(62, 516, "Good to know", size=16, weight="700")
    para(s, 62, 542, "A changed cover gets a new address, so nobody sees an old one. A daily task removes kept "
         "copies that no book uses any more. If the source is unavailable, apps show a blurred placeholder.",
         1080, size=14.5, fill=INK)
    return s


PICTURES = [("scan", scan), ("gone", gone), ("covers", covers)]
