"""Pictures for docs/bloem/overlays/invitations-api.md and
docs/bloem/overlays/architecture/invitations-onboarding.md."""
from xml.sax.saxutils import escape

from kit import INK, MUTED, SOFT, TIERS, Svg, no_mark, para, yes_mark

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
MONO = "'SFMono-Regular', Menlo, Consolas, 'Liberation Mono', monospace"


def _mono(s, x, y, text, size=12.5, fill=SOFT, anchor="start"):
    s.add(f'<text x="{x}" y="{y}" font-family="{MONO}" font-size="{size}" fill="{fill}" '
          f'text-anchor="{anchor}">{escape(text)}</text>')


def _facts(s, x, y, rows, width):
    for ok, text in rows:
        (yes_mark if ok else no_mark)(s, x + 12, y - 5, r=10)
        y = para(s, x + 32, y, text, width - 32, size=14.5, gap=20, fill=INK) + 14
    return y


def two_paths():
    s = Svg(1200, 640, "Two ways to invite someone",
            "Left: Silo's administrator invitation routes work only in the default organization; they can send "
            "the link by email or hand back a link, and only the server Owner may invite an administrator. "
            "Right: the organization console works in its own organization, invites ordinary users only, sends "
            "no email, and shows a one-time link that the administrator passes on. Bottom: both keep one live "
            "invitation per address in each organization; a new one replaces the old link, and an address that "
            "already has an account is refused.")
    s.header("Two ways to invite someone",
             "Every invitation belongs to one organization. Which routes made it decides which one.")
    o, p, g = TIERS["org"], TIERS["platform"], TIERS["grey"]

    # Silo's routes.
    s.rect(40, 120, 545, 360, SILO["fill"], SILO["stroke"], rx=16, sw=1.8)
    s.text(64, 156, "Silo's administrator routes", size=19, weight="700")
    _mono(s, 64, 182, "/api/v2/admin/invitations (and /api/v1)")
    s.badge(64, 198, "Default organization only", SILO, size=13)
    _facts(s, 64, 262, [
        (True, "Lists, sends, resends, revokes and deletes the default organization's invitations."),
        (True, "Can email the link, or hand back a link to share."),
        (True, "Can invite an administrator, but only the server Owner may."),
        (False, "Does not see invitations made in an organization's console."),
    ], 497)

    # Organization console.
    s.rect(615, 120, 545, 360, o["fill"], o["stroke"], rx=16, sw=1.8)
    s.text(639, 156, "The organization console", size=19, weight="700")
    _mono(s, 639, 182, "/api/bloem/v1/admin/organization/invitations")
    s.badge(639, 198, "Its own organization", o, size=13)
    _facts(s, 639, 262, [
        (True, "Invites ordinary users only, never an administrator."),
        (False, "Sends no email, even when the server can send mail."),
        (True, "Shows a one-time link once; the administrator passes it on."),
        (True, "Keeps the link out of the browser's caches and storage."),
    ], 497)

    # Shared rules.
    for x, where, c in ((64, "the default organization", SILO), (639, "the console's organization", o)):
        s.text(x, 452, "The new account joins", size=14.5, fill=MUTED)
        s.badge(x + 168, 433, where, c, size=13)
    s.rect(40, 500, 1120, 100, g["fill"], g["stroke"], rx=14, sw=1.6)
    s.text(64, 532, "Both", size=17, weight="700")
    para(s, 64, 560, "One live invitation per address in each organization: a new invitation or a resend "
         "replaces the old link, so a forwarded copy stops working. An address that already has an account "
         "anywhere on the server is refused.", 1070, size=14.5, gap=20)
    return s


PICTURES = [("two-paths", two_paths)]
