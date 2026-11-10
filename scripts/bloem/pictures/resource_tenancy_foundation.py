"""Pictures for docs/architecture/resource-tenancy-foundation.md."""
from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

LIB = {"fill": "#FFFFFF", "stroke": "#FDBA74", "label": INK}


def owners():
    s = Svg(1200, 780, "Who owns a library, and who may use it",
            "The platform owns Movies, Series and Kids. The default organization holds active grants to all three. "
            "Riverside Club owns its own Club matches library and holds active grants to Movies and Series and a "
            "suspended grant to Kids. Harbour Hotel holds a grant to Movies only. Nobody can be granted a library "
            "another organization owns.")
    s.header("Who owns a library, and who may use it",
             "Every library has one owner. An organization uses a platform library only through a grant.")
    p, o, a, w, st, g = (TIERS["platform"], TIERS["org"], TIERS["account"], TIERS["warn"], TIERS["stop"],
                         TIERS["grey"])

    # The platform's libraries.
    s.rect(40, 116, 1120, 132, p["fill"], p["stroke"], rx=16, sw=2)
    s.text(64, 150, "Owned by the platform", size=18, weight="700")
    s.text(64, 174, "Platform administrators add them. No organization sees one without a grant.", size=14,
           fill=MUTED)
    libs = [("Movies", 330), ("Series", 600), ("Kids", 870)]
    for name, cx in libs:
        s.rect(cx - 90, 192, 180, 40, "#FFFFFF", p["stroke"], rx=10, sw=1.5)
        s.text(cx, 218, name, size=16, weight="700", anchor="middle")

    # Organizations.
    orgs = [
        (40, "Default organization", [("Movies", "active", a), ("Series", "active", a), ("Kids", "active", a)], None),
        (420, "Riverside Club", [("Movies", "active", a), ("Series", "active", a), ("Kids", "suspended", w)],
         "Club matches"),
        (800, "Harbour Hotel", [("Movies", "active", a)], None),
    ]
    oy = 340
    for ox, name, grants, own in orgs:
        s.rect(ox, oy, 360, 300, o["fill"], o["stroke"], rx=16, sw=1.8)
        s.text(ox + 22, oy + 36, name, size=18, weight="700")
        s.text(ox + 22, oy + 66, "Grants", size=12.5, weight="700", fill=SOFT, spacing="1")
        for i, (lib, state, c) in enumerate(grants):
            y = oy + 78 + i * 44
            s.rect(ox + 22, y, 316, 36, "#FFFFFF", "#E5E7EB", rx=10, sw=1)
            s.text(ox + 38, y + 24, lib, size=15, weight="600")
            s.badge(ox + 326, y + 6, state, c, size=12, anchor="end")
        if own:
            s.text(ox + 22, oy + 232, "Owns", size=12.5, weight="700", fill=SOFT, spacing="1")
            s.rect(ox + 22, oy + 244, 316, 36, "#FFFFFF", o["stroke"], rx=10, sw=1.5)
            s.text(ox + 38, oy + 268, own, size=15, weight="600")
            s.text(ox + 326, oy + 268, "only Riverside Club", size=12.5, fill=o["label"], anchor="end")

    # Grants flow down from the platform to each organization.
    for cx in (220, 600, 980):
        s.arrow(cx, 254, cx, oy - 6, dash="6 5")
        s.text(cx + 12, 300, "grants", size=13, fill=SOFT)

    s.rect(40, 664, 1120, 84, st["fill"], st["stroke"], rx=14, sw=1.5)
    no_mark(s, 70, 706)
    s.text(96, 698, "A grant can only point at a platform library.", size=16, weight="700", fill=st["label"])
    s.text(96, 722, "Club matches can never be granted to Harbour Hotel. A suspended or revoked grant shows nothing.",
           size=14.5, fill=INK)
    return s


def starting_set():
    s = Svg(1200, 630, "The starting set, and libraries added later",
            "The starting set has versions. Version 1 held Movies, Series and Audiobooks; version 2, now active, "
            "holds Movies and Series, and Documentaries joined it when it was added. Riverside Club, created under "
            "version 1, keeps its three grants. Harbour Hotel, created after Documentaries was added, gets Movies, "
            "Series and Documentaries. A platform library added later is granted to the default organization at "
            "once and joins the active version; a deleted library leaves every version.")
    s.header("The starting set, and libraries added later",
             "A new organization gets the active version. A platform library added later joins it.")
    p, o, a, g, pl = TIERS["platform"], TIERS["org"], TIERS["account"], TIERS["grey"], TIERS["plan"]

    # Versions.
    versions = [
        (40, "Version 1", "earlier", g, ["Movies", "Series", "Audiobooks"]),
        (420, "Version 2", "active", a, ["Movies", "Series", "Documentaries"]),
    ]
    for x, title, state, c, libs in versions:
        s.rect(x, 120, 340, 196, pl["fill"], pl["stroke"], rx=16, sw=1.8)
        s.text(x + 22, 156, "Starting set", size=13, weight="700", fill=SOFT, spacing="1")
        s.text(x + 22, 182, title, size=19, weight="700")
        s.badge(x + 318, 138, state, c, size=12.5, anchor="end")
        bx = x + 22
        for lib in libs:
            bx += s.badge(bx, 206, lib, LIB, size=13.5) + 8
        para(s, x + 22, 264, "Kept as history. What it gave stays given." if state == "earlier" else
             "Given to every organization created from now on.", 296, size=14)
    s.arrow(384, 218, 416, 218)

    # Organizations created under each version.
    for x, name, when, libs in [(40, "Riverside Club", "created under version 1", ["Movies", "Series", "Audiobooks"]),
                                (420, "Harbour Hotel", "created after Documentaries was added",
                                 ["Movies", "Series", "Documentaries"])]:
        s.arrow(x + 170, 320, x + 170, 372)
        s.rect(x, 376, 340, 132, o["fill"], o["stroke"], rx=16, sw=1.8)
        s.text(x + 22, 410, name, size=18, weight="700")
        s.text(x + 22, 432, when, size=14, fill=MUTED)
        bx = x + 22
        for lib in libs:
            bx += s.badge(bx, 452, lib, LIB, size=13.5) + 8
    para(s, 40, 540, "Riverside Club keeps Audiobooks: making version 2 active did not take it away. It gets "
         "Documentaries only if someone grants it.", 720, size=14.5, fill=INK)

    # A platform library added later.
    s.rect(800, 120, 360, 388, p["fill"], p["stroke"], rx=16, sw=1.8)
    s.text(822, 156, "A platform library", size=18, weight="700")
    s.text(822, 180, "added later", size=18, weight="700")
    s.badge(822, 198, "Documentaries", LIB, size=13.5)
    steps = ["Owned by the platform. Silo's API cannot name any other owner.",
             "Granted to the default organization straight away.",
             "Joins the active version of the starting set, so organizations created afterwards get it.",
             "Deleted later, it leaves every version."]
    y = 256
    for i, t in enumerate(steps):
        s.number(836, y + 6, i + 1, p["stroke"], r=12)
        y = para(s, 858, y + 11, t, 284, size=14, fill=INK) + 16

    footer(s, "An organization's own libraries are created through native storage and never join the starting set.",
           y=596, fill=INK)
    return s


PICTURES = [("owners", owners), ("starting-set", starting_set)]
