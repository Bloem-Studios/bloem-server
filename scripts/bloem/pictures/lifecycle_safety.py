"""Pictures for docs/architecture/bloem-lifecycle-safety.md."""
from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, card, footer, no_mark, para, yes_mark


def retire():
    s = Svg(1200, 720, "Retiring an organization",
            "The storefront ends Riverside Club. In one step, Bloem removes each member: Sam's account, which has "
            "nothing else, is deleted; the coach's account, which has the admin role, and Kim's account, which owns "
            "another organization, are kept without the membership. The organization is suspended and kept on "
            "record. If any removal fails, nothing changes. Afterwards, the kept accounts' things in the "
            "organization are cleaned up.")
    s.header("Retiring an organization",
             "Every member is removed in one step. Only accounts with nothing else to hold them are deleted.")
    o, a, g, st, w, p = (TIERS["org"], TIERS["account"], TIERS["grey"], TIERS["stop"], TIERS["warn"],
                         TIERS["platform"])

    # Start.
    box(s, 40, 124, 300, 120, g, "The storefront ends it",
        "Riverside Club cancels, and the storefront asks Bloem to retire the organization.", size=14,
        title_size=16.5)
    s.arrow(344, 184, 396, 184)

    # The one step.
    s.rect(400, 116, 760, 404, o["fill"], o["stroke"], rx=16, sw=1.8)
    s.text(424, 150, "One step, all or nothing", size=18, weight="700")
    s.text(424, 174, "Bloem locks the organization first, then removes each member as it would one by one.",
           size=14, fill=MUTED)
    members = [
        ("sam@riverside.example", "Has nothing else", "Account deleted", st),
        ("coach@riverside.example", "Has the admin role", "Kept, membership removed", a),
        ("kim@riverside.example", "Owns another organization", "Kept, membership removed", a),
    ]
    y = 196
    for email, why, result, c in members:
        s.rect(424, y, 712, 60, "#FFFFFF", "#E5E7EB", rx=12)
        s.text(444, y + 26, email, size=15, weight="700")
        s.text(444, y + 47, why, size=13.5, fill=MUTED)
        s.badge(1120, y + 17, result, c, size=13, anchor="end")
        y += 72
    s.rect(424, y + 4, 712, 72, w["fill"], w["stroke"], rx=12, sw=1.4)
    s.text(444, y + 32, "Then the organization is retired", size=15, weight="700")
    s.text(444, y + 55, "Suspended, its storefront link cleared. The record stays as the owner of what it had.",
           size=13.5, fill=INK)

    # Failure and afterwards.
    s.rect(40, 276, 300, 160, st["fill"], st["stroke"], rx=14, sw=1.5)
    no_mark(s, 66, 306, r=11)
    s.text(88, 312, "If one removal fails", size=16, weight="700", fill=st["label"])
    para(s, 60, 340, "Everything is undone: every member, every ownership change and the organization stay as "
         "they were.", 260, size=14, fill=INK)

    s.arrow(780, 524, 780, 556)
    box(s, 400, 560, 760, 108, p, "Afterwards",
        "Whatever the kept accounts still had in Riverside Club is cleaned up. This finishes even if the request "
        "that started it is cancelled.", size=14, title_size=16.5)
    footer(s, "An organization is never deleted outright.", y=694, fill=INK)
    return s


def step_card(s, x, y, w, h, color, n, title, body):
    """A narrow card: the number on its own line, then the title and body, both wrapped."""
    s.rect(x, y, w, h, "#FFFFFF", "#E5E7EB", rx=12)
    s.rect(x, y, 8, h, color["stroke"], rx=4)
    s.number(x + 34, y + 30, n, color["stroke"], r=13)
    ty = para(s, x + 24, y + 70, title, w - 40, size=16, fill=INK, weight="700", gap=21)
    para(s, x + 24, ty + 6, body, w - 40, size=14, gap=19)


def library_deletion():
    s = Svg(1200, 744, "Deleting a library safely",
            "Five steps. An administrator asks to delete a library. Bloem first checks whether any version of the "
            "starting set names it, and refuses if so. Otherwise the library is switched off and a job starts. The "
            "worker takes a lock that stops anyone adding the library to the starting set and checks again, deletes "
            "the catalog in small batches, and in a final step moves shared collections, removes the grants and the "
            "library, and recounts series that live on elsewhere.")
    s.header("Deleting a library safely",
             "A library the starting set still names is never deleted, not even halfway.")
    p, o, a, st, g, pl = (TIERS["platform"], TIERS["org"], TIERS["account"], TIERS["stop"], TIERS["grey"],
                          TIERS["plan"])

    steps = [
        (g, "Ask to delete", "An administrator deletes the library."),
        (pl, "Check the starting set", "Does any version of it, old or current, name this library?"),
        (o, "Switch off and queue", "The library is switched off and a deletion job is created."),
        (p, "Lock and check again", "The worker holds a lock that stops anyone adding the library to the "
                                    "starting set, and checks once more."),
        (a, "Delete and finish", "The catalog goes in small batches. Then shared collections move, grants and "
                                 "the library go, and series elsewhere are recounted."),
    ]
    x, y, w, h, gap = 40, 124, 204, 236, 25
    for i, (c, title, body) in enumerate(steps):
        bx = x + i * (w + gap)
        step_card(s, bx, y, w, h, c, i + 1, title, body)
        if i < len(steps) - 1:
            s.arrow(bx + w + 2, y + h / 2, bx + w + gap - 2, y + h / 2)

    # Refusals from steps 2 and 4.
    for i in (1, 3):
        cx = x + i * (w + gap) + w / 2
        s.arrow(cx, y + h + 4, cx, 418)
        s.text(cx + 10, y + h + 30, "named", size=13, weight="700", fill=st["label"])
    s.rect(229, 422, 688, 80, st["fill"], st["stroke"], rx=14, sw=1.5)
    no_mark(s, 258, 462, r=12)
    s.text(282, 456, "Refused, and nothing is deleted", size=16, weight="700", fill=st["label"])
    s.text(282, 480, "Step 2 answers 409 before the library is switched off or a job exists.", size=14, fill=INK)

    s.rect(40, 534, 1120, 170, "#FFFFFF", "#E5E7EB", rx=14)
    s.rect(40, 534, 8, 170, p["stroke"], rx=4)
    s.text(66, 566, "Why both checks and the lock", size=17, weight="700")
    rows = ["Deleting the catalog first and finding the reference afterwards would lose titles and watch history "
            "while leaving the library behind.",
            "The lock means nobody can add the library to the starting set between the last check and the end.",
            "Old versions of the starting set count too: they are kept as history and never rewritten.",
            "A large library is not deleted in one transaction. A deletion that failed earlier is not undone."]
    yy = 596
    for r in rows:
        s.add(f'<circle cx="72" cy="{yy - 5}" r="3.5" fill="{INK}"/>')
        yy = para(s, 86, yy, r, 1050, size=14, fill=INK) + 8
    return s


PICTURES = [("retire", retire), ("library-deletion", library_deletion)]
