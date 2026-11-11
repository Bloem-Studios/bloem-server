"""Pictures for docs/architecture/bloem-web-feature-coverage.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para


def web_app_map():
    s = Svg(1200, 800, "What the web app has, and what it calls",
            "The web app built into Bloem Server, in three parts: what viewers use, what an organization "
            "administrator uses, and what a platform administrator uses. Silo's screens are kept; Bloem adds "
            "its own. Below, the two APIs it calls: Silo's /api/v2 for everything Silo has and Bloem's "
            "/api/bloem/v2 for Bloem's features. It calls none of Silo's /api/v1.")
    s.header("What the web app has, and what it calls",
             "Silo's web app with Bloem's screens added. Grey items are Silo's, kept as Silo ships them.")
    p, o, a, g, st = TIERS["platform"], TIERS["org"], TIERS["account"], TIERS["grey"], TIERS["stop"]

    cols = [
        (a, "For viewers", [
            ("Home with campaign cards", True), ("Seasonal effects", True), ("Live TV, guide, recordings", True),
            ("Playback cards in the player", True), ("Inbox and announcements", False),
            ("Profile sign-ins", True), ("Catalog, player, profiles, requests", False),
        ]),
        (o, "Organization administrator", [
            ("People and their plans", True), ("Access groups", True), ("Invitations", True),
            ("Library grants", True), ("Activity", True), ("Policy decisions", True),
        ]),
        (p, "Platform administrator", [
            ("Organizations, domains, branding", True), ("Plans (entitlement templates)", True),
            ("Campaigns & seasonal packs", True), ("Announcements", True), ("Live TV sources", True),
            ("Silo's server settings", False),
        ]),
    ]
    x, cw, gap = 40, 360, 20
    for i, (color, title, items) in enumerate(cols):
        cx = x + i * (cw + gap)
        s.rect(cx, 124, cw, 400, color["fill"], color["stroke"], rx=16, sw=1.8)
        s.text(cx + 20, 160, title, size=18, weight="700")
        y = 180
        for name, bloem in items:
            fill = "#FFFFFF" if bloem else "#F3F4F6"
            stroke = color["stroke"] if bloem else "#D1D5DB"
            s.rect(cx + 18, y, cw - 36, 40, fill, stroke, rx=10, sw=1.2)
            s.text(cx + 34, y + 26, name, size=14.5, weight="600" if bloem else "normal",
                   fill=INK if bloem else MUTED)
            y += 48

    # APIs.
    ay = 568
    s.rect(40, ay, 540, 120, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(60, ay + 34, "Silo's /api/v2", size=17, weight="700")
    para(s, 60, ay + 62, "Everything Silo has: accounts, libraries, the catalog, playback, settings. Through "
         "Silo's own web code.", 500, size=14, gap=20)
    s.rect(620, ay, 540, 120, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(640, ay + 34, "Bloem's /api/bloem/v2", size=17, weight="700")
    para(s, 640, ay + 62, "Bloem's features: organizations, plans, Live TV, campaigns, seasonal packs, "
         "announcements, profile sign-ins.", 500, size=14, gap=20)
    no_mark(s, 64, ay + 158, r=12)
    s.text(86, ay + 163, "Silo's old v1 API: not called since 2026-11-01, and retired by Silo on 2026-11-11.",
           size=14, fill=INK)
    footer(s, "Each feature's status, evidence and limits are in the tables on this page.", y=770, fill=MUTED)
    return s


PICTURES = [("web-app-map", web_app_map)]
