"""Pictures for docs/bloem/api/v1-routes.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, footer, no_mark

OLD = {"fill": "#FEF2F2", "stroke": "#EF4444", "label": "#B91C1C"}
NEW = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}


def replacements():
    s = Svg(1200, 700, "Where Bloem's /api/v1 routes moved",
            "Six rows, one for each area Bloem added to Silo's /api/v1: tenants, tenant members, announcements, "
            "campaigns, seasonal packs and remote control. Each row shows the old path and its replacement under "
            "/api/bloem/v2. Remote control has no replacement: it is dormant. Since 11 November 2026 every old route "
            "answers 410 Gone.")
    s.header("Where Bloem's /api/v1 routes moved",
             "Silo retired /api/v1 on 11 November 2026; every old route now answers 410 Gone.")
    s.text(40, 140, "Area", size=14, weight="700", fill=SOFT)
    s.text(250, 140, "Old, on Silo's /api/v1", size=14, weight="700", fill=OLD["label"])
    s.text(700, 140, "Now, on Bloem's /api/bloem/v2", size=14, weight="700", fill=NEW["label"])
    rows = [
        ("Tenants", "7", ["/admin/tenants/..."], ["/admin/platform/tenants/..."]),
        ("Tenant members", "17", ["/admin/tenants/{id}/members/..."], ["/admin/platform/tenants/{id}/members/..."]),
        ("Announcements", "3", ["/admin/notifications/announcements"], ["/admin/platform/announcements"]),
        ("Campaigns", "6", ["/admin/promotions/...", "/promotions, /promotions/{id}/save"],
         ["/admin/platform/promotions/...", "/promotions/..."]),
        ("Seasonal packs", "7", ["/admin/ambience/...", "/ambience/assets/{ref}"],
         ["/admin/platform/ambience/...", "/ambience/assets/{ref}"]),
        ("Remote control", "6", ["/admin/remote/..., /devices/{id}/remote-control,", "/profiles/household/sessions/..."],
         None),
    ]
    y = 156
    for area, count, old, new in rows:
        h = 64 if len(old) == 1 else 80
        s.rect(40, y, 1120, h, "#FFFFFF", "#E5E7EB", rx=12)
        s.text(60, y + h / 2 - 2, area, size=16, weight="700")
        s.text(60, y + h / 2 + 18, f"{count} routes", size=13, fill=MUTED)
        s.rect(240, y + 8, 410, h - 16, OLD["fill"], OLD["stroke"], rx=10, sw=1.2)
        s.lines(256, y + h / 2 + 5 - (len(old) - 1) * 10, old, size=14, gap=20, fill=INK)
        s.arrow(654, y + h / 2, 686, y + h / 2)
        if new:
            s.rect(690, y + 8, 450, h - 16, NEW["fill"], NEW["stroke"], rx=10, sw=1.2)
            s.lines(706, y + h / 2 + 5 - (len(new) - 1) * 10, new, size=14, gap=20, fill=INK)
        else:
            s.rect(690, y + 8, 450, h - 16, "#F3F4F6", "#9CA3AF", rx=10, sw=1.2, dash="6 5")
            no_mark(s, 716, y + h / 2)
            s.text(740, y + h / 2 + 5, "None: dormant until native routes are built", size=14, fill=INK)
        y += h + 12
    footer(s, "Paths are shortened: each old path starts with /api/v1, each new one with /api/bloem/v2.", y=y + 34,
           fill=INK)
    s.h = int(y + 70)
    return s


PICTURES = [("replacements", replacements)]
