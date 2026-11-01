"""Pictures for docs/operations/entitlement-templates.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, footer, no_mark, para, yes_mark


def _library(s, x, y, w, name, color, note=None, dash=None):
    s.rect(x, y, w, 40, "#FFFFFF", color["stroke"], rx=10, sw=1.4, dash=dash)
    s.text(x + 16, y + 26, name, size=14.5, weight="600", fill=INK)
    if note:
        s.text(x + w - 14, y + 26, note, size=12.5, fill=SOFT, anchor="end")


def two_entitlements():
    s = Svg(1200, 612, "Two things called an entitlement",
            "Three columns. The platform owns four libraries. Library grants give an organization two of them, and "
            "the organization also owns a library of its own. The plan, a policy entitlement template, picks from "
            "the organization's three libraries and sets screens, conversions, quality, downloads and requests. It "
            "cannot permit the platform library the organization was not granted.")
    s.header("Two things called an entitlement",
             "A library grant decides what an organization may use. A plan decides what each member may do inside it.")
    pl, o, p, st = TIERS["platform"], TIERS["org"], TIERS["plan"], TIERS["stop"]

    # Platform.
    s.rect(40, 124, 320, 366, pl["fill"], pl["stroke"], rx=16, sw=1.8)
    s.text(60, 160, "The platform", size=19, weight="700")
    s.text(60, 184, "Its libraries", size=14, fill=MUTED)
    plat = ["Movies", "Series", "Audiobooks", "Ebooks"]
    for i, n in enumerate(plat):
        _library(s, 60, 206 + i * 56, 280, n, pl)

    # Organization.
    s.rect(440, 124, 320, 366, o["fill"], o["stroke"], rx=16, sw=1.8)
    s.text(460, 160, "Riverside Club", size=19, weight="700")
    s.text(460, 184, "What it may use", size=14, fill=MUTED)
    _library(s, 460, 206, 280, "Movies", o, "granted")
    _library(s, 460, 262, 280, "Series", o, "granted")
    _library(s, 460, 318, 280, "Club films", o, "its own")
    _library(s, 460, 374, 280, "Audiobooks", TIERS["grey"], "not granted", dash="5 4")

    for i in range(2):
        s.arrow(344, 226 + i * 56, 456, 226 + i * 56, color=pl["stroke"])
    s.text(400, 249, "library", size=13, weight="700", fill=pl["label"], anchor="middle")
    s.text(400, 265, "grants", size=13, weight="700", fill=pl["label"], anchor="middle")

    # Plan.
    s.rect(840, 124, 320, 366, p["fill"], p["stroke"], rx=16, sw=1.8)
    s.text(860, 160, "The plan", size=19, weight="700")
    s.text(860, 184, "Standard, revision 3", size=14, fill=MUTED)
    rows = [("All libraries", True), ("Movies, Series, Club films", True), ("Audiobooks", False)]
    s.rect(860, 206, 280, 128, "#FFFFFF", "#E5E7EB", rx=10)
    s.text(876, 230, "LIBRARIES", size=11.5, weight="700", fill=p["label"], spacing="1")
    s.text(876, 256, "All libraries, which means:", size=14, fill=INK)
    yes_mark(s, 886, 282, r=9)
    s.text(902, 287, "Movies, Series, Club films", size=14, fill=INK)
    no_mark(s, 886, 312, r=9)
    s.text(902, 317, "Audiobooks: never", size=14, fill=INK)
    s.rect(860, 346, 280, 124, "#FFFFFF", "#E5E7EB", rx=10)
    s.text(876, 370, "WHAT EACH MEMBER MAY DO", size=11.5, weight="700", fill=p["label"], spacing="1")
    s.lines(876, 396, ["3 screens at once", "1 video conversion", "Up to 1080p", "Downloads and requests"],
            size=14, fill=INK, gap=20)
    s.arrow(764, 306, 836, 306, color=o["stroke"])

    s.rect(40, 510, 1120, 72, "#FFFFFF", "#E5E7EB", rx=14)
    para(s, 64, 540, "A plan can never reach past the organization's libraries. To let Riverside Club's members "
         "listen to audiobooks, a platform administrator first grants the library to the club; the plan's "
         "“All libraries” then includes it.", 1080, size=14.5, fill=INK, gap=20)
    return s


def where():
    s = Svg(1200, 600, "Where plans live in the admin console",
            "Two columns of admin pages. In the Platform context: Entitlement Templates, Direct Accounts, Bulk Account "
            "Policies, and an organization's Tenant entitlement panel. In the Organization context: People with "
            "Apply policy, and Policy Cohorts. Every change is previewed first and confirmed within ten minutes.")
    s.header("Where plans live in the admin console",
             "Pick the administrative context first. The pages differ by who you are acting as.")
    pl, o, p = TIERS["platform"], TIERS["org"], TIERS["plan"]
    cols = [
        (40, 640, pl, "Platform context", "A platform administrator", [
            ("Entitlement Templates", "/admin/platform/entitlement-templates",
             "Create, revise, clone and archive plans; history."),
            ("Direct Accounts", "/admin/platform/direct-accounts",
             "Put one direct account on an exact revision."),
            ("Bulk Account Policies", "/admin/platform/direct-accounts/bulk",
             "Move a reviewed list of direct accounts in one job."),
            ("Organizations → Tenant entitlement", "/admin/platform/organizations",
             "An organization's plan, and its history."),
        ]),
        (700, 460, o, "Organization context", "An organization administrator", [
            ("People → Apply policy", "/admin/organization/people",
             "Move selected people in one job."),
            ("Policy Cohorts", "/admin/organization/policy-cohorts",
             "Each policy revision in use, and who is on it."),
        ]),
    ]
    for x, w, c, title, who, pages in cols:
        s.rect(x, 124, w, 400, c["fill"], c["stroke"], rx=16, sw=1.8)
        s.text(x + 24, 160, title, size=19, weight="700")
        s.text(x + 24, 184, who, size=14, fill=MUTED)
        for i, (name, path, what) in enumerate(pages):
            y = 204 + i * 78
            s.rect(x + 20, y, w - 40, 66, "#FFFFFF", "#E5E7EB", rx=10)
            s.text(x + 38, y + 26, name, size=15.5, weight="700")
            s.text(x + w - 36, y + 26, path, size=12, fill=SOFT, anchor="end")
            s.text(x + 38, y + 50, what, size=13.5, fill=MUTED)
    footer(s, "Every change is previewed first and confirmed within ten minutes. If anything changed in between, "
              "preview again.", y=566, size=14.5)
    return s


PICTURES = [("two-entitlements", two_entitlements), ("where", where)]
