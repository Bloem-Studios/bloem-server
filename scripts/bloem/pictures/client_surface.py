"""Pictures for docs/architecture/bloem-client-surface.md."""
from kit import INK, TIERS, Svg, footer, para

CHIP = {"fill": "#FFFFFF", "stroke": "#D1D5DB", "label": INK}


def _chips(s, x, y, right, names, size=13.5):
    """Lay pill labels left to right, wrapping at right; return the y below the last row."""
    row_h = size + 13 + 10
    cx = x
    for name in names:
        w = round(len(name) * size * 0.58 + 22)
        if cx + w > right and cx > x:
            cx = x
            y += row_h
        s.badge(cx, y, name, CHIP, size=size)
        cx += w + 10
    return y + row_h


def route_map():
    s = Svg(1200, 820, "The parts of Bloem's native API",
            "Four bands, one for each kind of credential. Public routes need nothing. Viewer routes need an "
            "account sign-in and a chosen profile. A few routes need the account sign-in alone. Administration "
            "needs an administrator pass, or an administrator's API key on the storefront's platform routes.")
    s.header("The parts of Bloem's native API",
             "Everything under /api/bloem/v2, grouped by what a caller must show to use it.")
    bands = [
        (TIERS["grey"], "Public", "Nothing: a client asks before it signs in.",
         ["/capabilities", "/server/identity", "/ambience/public", "/ambience/assets/{ref}",
          "/preview/sessions", "/preview/status"]),
        (TIERS["account"], "Viewer", "An account sign-in and X-Profile-Id, with the PIN proof when needed.",
         ["/watch/home", "/watch/items/{id}", "/watch/search", "/persons/{id}",
          "/catalog/items/{id}/collections", "/notifications", "/livetv/...", "/ambience", "/promotions",
          "/book-requests", "/ebook-requests"]),
        (TIERS["profile"], "Account only", "An account sign-in, no administrator pass.",
         ["/organizations", "/admin/session", "/profile-credentials/{id}"]),
        (TIERS["org"], "Administration", "An administrator pass from /admin/session; some platform routes also "
                                         "take an administrator's API key.",
         ["/admin/organization/...", "/admin/platform/organizations/...", "/admin/platform/tenants/...",
          "/admin/platform/accounts/...", "/admin/platform/entitlement-templates/...",
          "/admin/platform/preview/...", "/admin/platform/native-storage/..."]),
    ]
    y = 120
    for color, title, who, routes in bands:
        # Measure first so the band fits its chips.
        probe = Svg(10, 10, "", "")
        bottom = _chips(probe, 360, y + 22, 1140, routes)
        h = max(bottom - y + 10, 120)
        s.rect(40, y, 1120, h, color["fill"], color["stroke"], rx=16, sw=1.6)
        s.text(64, y + 38, title, size=19, weight="700", fill=color["label"])
        para(s, 64, y + 64, who, 270, size=14, gap=19, fill=INK)
        _chips(s, 360, y + 22, 1140, routes)
        y += h + 16
    footer(s, "Profile sign-ins reach only the book request routes here. A route that is not built on this "
              "server answers 404; capabilities say what this build can do.", y=y + 30, fill=INK)
    s.h = int(round(y + 74))
    return s


PICTURES = [("route-map", route_map)]
