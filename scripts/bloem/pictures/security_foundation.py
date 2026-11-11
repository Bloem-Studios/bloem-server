"""Pictures for docs/architecture/bloem-security-foundation.md."""
from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

PART = {"fill": "#FFFBEB", "stroke": "#F59E0B", "label": "#B45309"}


def _part_mark(s, cx, cy, r=12):
    """An amber circle with a dash: "only part of it"."""
    s.add(f'<circle cx="{cx}" cy="{cy}" r="{r}" fill="#FFFFFF" stroke="{PART["stroke"]}" stroke-width="2.5"/>')
    s.add(f'<path d="M{cx - r * 0.45},{cy} L{cx + r * 0.45},{cy}" stroke="{PART["stroke"]}" '
          f'stroke-width="2.5" stroke-linecap="round"/>')


def passes():
    s = Svg(1200, 720, "Which sign-in opens which door",
            "A grid. Rows are four kinds of sign-in: an account sign-in, a profile's own sign-in, an organization "
            "administrator pass and a platform administrator pass. Columns are four kinds of request: watching and "
            "browsing, managing a profile's own sign-in, running one organization, and running the platform. Each "
            "cell says yes, partly or no, with the reason.")
    s.header("Which sign-in opens which door",
             "Each kind of sign-in opens only its own doors. An administrator pass is a separate, short-lived key.")
    a, f, o, p = TIERS["account"], TIERS["profile"], TIERS["org"], TIERS["platform"]

    cols = ["Watch and browse", "A profile's own sign-in", "Run one organization", "Run the platform"]
    col_x, col_w, row_label_w = 300, 215, 240
    for i, title in enumerate(cols):
        x = col_x + i * col_w
        s.rect(x + 4, 120, col_w - 8, 50, "#F9FAFB", "#E5E7EB", rx=10)
        s.text(x + col_w / 2, 151, title, size=15, weight="700", anchor="middle")

    rows = [
        (a, "Account sign-in", "Email and password",
         [("yes", "With a profile picked"), ("yes", "With the account password"),
          ("no", "Swap it for a pass first"), ("no", "Swap it for a pass first")]),
        (f, "Profile sign-in", "Straight into one profile",
         [("part", "Browse, play, its own book requests"), ("no", "Account only"),
          ("no", "Never"), ("no", "Never")]),
        (o, "Organization pass", "15 minutes, one organization",
         [("no", "Not a viewer"), ("no", "Not a household"),
          ("yes", "Its own organization only"), ("no", "Never")]),
        (p, "Platform pass", "15 minutes, platform administrators",
         [("no", "Not a viewer"), ("no", "Not a household"),
          ("no", "Needs an organization pass"), ("yes", "Every organization")]),
    ]
    y, h = 186, 104
    for color, title, sub, cells in rows:
        s.rect(40, y, row_label_w, h - 12, color["fill"], color["stroke"], rx=12, sw=1.6)
        s.text(60, y + 40, title, size=17, weight="700")
        para(s, 60, y + 64, sub, row_label_w - 40, size=13.5, gap=18)
        for i, (kind, note) in enumerate(cells):
            x = col_x + i * col_w
            s.rect(x + 4, y, col_w - 8, h - 12, "#FFFFFF", "#E5E7EB", rx=10)
            cx, cy = x + 30, y + 30
            if kind == "yes":
                yes_mark(s, cx, cy)
            elif kind == "no":
                no_mark(s, cx, cy)
            else:
                _part_mark(s, cx, cy)
            para(s, x + 50, y + 35, note, col_w - 64, size=13.5, gap=18, fill=INK)
        y += h

    s.rect(40, 612, 1120, 72, "#F9FAFB", "#E5E7EB", rx=12)
    para(s, 60, 640, "An account sign-in is swapped for a pass at POST /api/bloem/v2/admin/session. The pass ends with "
         "the sign-in it came from. A platform administrator's API key opens the storefront's platform routes "
         "too, never an organization's.", 1080, size=14, fill=INK, gap=20)
    return s


def checks():
    s = Svg(1200, 800, "What Bloem checks on every request",
            "Four checks run in order before Bloem answers: whose sign-in it is, which organization, which "
            "profile, and what that profile may see. Each refusal is shown beside its check. Anything missing, "
            "stale or unclear counts as no.")
    s.header("What Bloem checks on every request",
             "Checked again on every request, not only at sign-in. Any “no” stops the answer.")
    a, o, f, st, wa = TIERS["account"], TIERS["org"], TIERS["profile"], TIERS["stop"], TIERS["warn"]
    checks_ = [
        (a, "Whose sign-in is this?",
         "The sign-in must still be live and belong to the account its token names.",
         (st, "Refused. The app asks to sign in again.")),
        (o, "Which organization?",
         "The account's one organization, never one named in a header or by the web address. It must be "
         "active, with the membership unchanged.",
         (st, "Refused, without saying whether the organization exists.")),
        (f, "Which profile?",
         "It belongs to this account and organization, and its PIN was entered.",
         (wa, "Refused. A sign-in older than the organization's revocation is ended.")),
        (o, "What may it see?",
         "Libraries the organization owns or was granted, narrowed by the plan and the profile, before the "
         "catalog is searched.",
         (st, "Hidden titles are not shown at all.")),
    ]
    x, y, w, h, gap = 40, 124, 660, 100, 32
    for i, (color, title, body, (cno, out)) in enumerate(checks_):
        s.rect(x, y, w, h, color["fill"], color["stroke"], rx=14, sw=1.8)
        s.number(x + 28, y + 30, i + 1, color["stroke"])
        s.text(x + 52, y + 36, title, size=18, weight="700")
        para(s, x + 52, y + 64, body, w - 80, size=14, gap=20)
        s.arrow(x + w + 4, y + h / 2, 776, y + h / 2)
        s.text(x + w + 20, y + h / 2 - 8, "No", size=14, weight="700", fill=cno["label"])
        s.rect(780, y + 12, 380, h - 24, cno["fill"], cno["stroke"], rx=12, sw=1.5)
        rows_out = 1 if len(out) * 7.5 < 340 else 2
        para(s, 800, y + h / 2 + 5 - (rows_out - 1) * 10.5, out, 340, size=15, gap=21, fill=INK)
        s.arrow(x + w / 2, y + h + 2, x + w / 2, y + h + gap - 3)
        s.text(x + w / 2 + 12, y + h + 22, "Yes", size=13, weight="700", fill=a["label"])
        y += h + gap
    s.rect(x + w / 2 - 150, y, 300, 62, INK, rx=31)
    s.text(x + w / 2, y + 40, "Bloem answers", size=21, weight="700", fill="#FFFFFF", anchor="middle")
    footer(s, "Anything missing, stale or unclear counts as no. A web address changes how Bloem looks, never what "
              "an account can reach.", y=760, fill=INK)
    return s


def first_owner():
    s = Svg(1200, 720, "How a server gets its first owner",
            "Two panels. Left: on a new server, five steps run in one transaction, all or nothing: the account, "
            "its membership of the default organization, an optional profile, ownership, and the sign-in. Right: "
            "on a server upgraded from Silo, one enabled administrator becomes the owner by itself; with several, "
            "ownership waits until an operator picks one.")
    s.header("How a server gets its first owner",
             "The first owner runs the platform and the default organization. Nobody can win that by racing.")
    a, o, p, w, g = TIERS["account"], TIERS["org"], TIERS["platform"], TIERS["warn"], TIERS["grey"]

    # Left: a new server.
    s.rect(40, 120, 560, 560, "#FFFFFF", "#E5E7EB", rx=16)
    s.text(64, 156, "A new server", size=19, weight="700")
    s.text(64, 180, "The first person to finish setup.", size=14, fill=MUTED)
    s.rect(64, 200, 512, 400, p["fill"], p["stroke"], rx=14, sw=1.6, dash="7 5")
    s.text(84, 228, "ONE TRANSACTION: ALL OR NOTHING", size=12.5, weight="700", fill=p["label"], spacing="1")
    steps = [
        (a, "Create the account", "An administrator account."),
        (o, "Make it a member", "Of the default organization."),
        (TIERS["profile"], "Add its first profile", "If one was asked for."),
        (p, "Make it the owner", "Of the platform and the default organization, now active."),
        (a, "Sign it in", "A sign-in session and its tokens."),
    ]
    y = 244
    for i, (c, t, b) in enumerate(steps):
        s.number(100, y + 24, i + 1, c["stroke"])
        s.text(126, y + 22, t, size=15.5, weight="700")
        para(s, 126, y + 42, b, 430, size=13.5, gap=18)
        y += 70
    para(s, 64, 630, "A second person finishing setup at the same moment waits, then finds setup already done.",
         512, size=14, fill=INK, gap=20)

    # Right: an upgraded server.
    s.rect(620, 120, 540, 560, "#FFFFFF", "#E5E7EB", rx=16)
    s.text(644, 156, "A server upgraded from Silo", size=19, weight="700")
    s.text(644, 180, "Its accounts already exist.", size=14, fill=MUTED)
    box(s, 644, 204, 492, 128, a, "One enabled administrator",
        "Becomes the owner of the platform and the default organization by itself. Every account joins the "
        "default organization and keeps its profiles and libraries.", size=14)
    box(s, 644, 352, 492, 150, w, "Several enabled administrators",
        "Bloem does not guess. Ownership waits and the default organization stays getting ready. Apps can still "
        "sign in and pick profiles; organization administration waits.", size=14)
    s.arrow(890, 506, 890, 536)
    s.rect(644, 540, 492, 72, g["fill"], g["stroke"], rx=12, sw=1.4)
    para(s, 664, 570, "An operator checks who is who, then picks one in a short database transaction.", 452,
         size=14, fill=INK, gap=20)
    return s


PICTURES = [("passes", passes), ("checks", checks), ("first-owner", first_owner)]
