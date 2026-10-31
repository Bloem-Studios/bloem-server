#!/usr/bin/env python3
"""Draw the four pictures on docs/architecture/bloem-organizations.md as SVG files.

Usage: python3 scripts/bloem/draw-organization-pictures.py docs/architecture/images/organizations
"""
import os
import sys
from xml.sax.saxutils import escape

FONT = "'Segoe UI', 'Helvetica Neue', Helvetica, Arial, sans-serif"
INK = "#101827"
MUTED = "#4B5563"
SOFT = "#6B7280"
LINE = "#9CA3AF"

TIERS = {
    "platform": {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"},
    "org": {"fill": "#EEF2FF", "stroke": "#6366F1", "label": "#4338CA"},
    "account": {"fill": "#ECFDF5", "stroke": "#10B981", "label": "#047857"},
    "profile": {"fill": "#F0F9FF", "stroke": "#0EA5E9", "label": "#0369A1"},
    "plan": {"fill": "#F0FDFA", "stroke": "#14B8A6", "label": "#0F766E"},
    "warn": {"fill": "#FFFBEB", "stroke": "#F59E0B", "label": "#B45309"},
    "stop": {"fill": "#FEF2F2", "stroke": "#EF4444", "label": "#B91C1C"},
    "grey": {"fill": "#F3F4F6", "stroke": "#9CA3AF", "label": "#374151"},
}


class Svg:
    def __init__(self, width, height, title, desc):
        self.w, self.h = width, height
        self.parts = []
        self.title, self.desc = title, desc

    def add(self, s):
        self.parts.append(s)

    def rect(self, x, y, w, h, fill, stroke="none", rx=12, sw=1.5, dash=None):
        d = f' stroke-dasharray="{dash}"' if dash else ""
        self.add(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{rx}" fill="{fill}" stroke="{stroke}" stroke-width="{sw}"{d}/>')

    def text(self, x, y, s, size=15, weight="normal", fill=INK, anchor="start", spacing=None, italic=False):
        extra = f' letter-spacing="{spacing}"' if spacing else ""
        if italic:
            extra += ' font-style="italic"'
        self.add(f'<text x="{x}" y="{y}" font-size="{size}" font-weight="{weight}" fill="{fill}" text-anchor="{anchor}"{extra}>{escape(s)}</text>')

    def lines(self, x, y, rows, size=14, fill=MUTED, gap=None, weight="normal", anchor="start"):
        gap = gap or round(size * 1.4)
        for i, row in enumerate(rows):
            self.text(x, y + i * gap, row, size=size, fill=fill, weight=weight, anchor=anchor)

    def badge(self, x, y, label, color, size=13, anchor="start"):
        """Pill with a label. x is the left edge, or the right edge with anchor="end"."""
        w = round(len(label) * size * 0.58 + 22)
        left = x - w if anchor == "end" else x
        self.rect(left, y, w, size + 13, color["fill"], color["stroke"], rx=(size + 13) / 2, sw=1.2)
        self.text(left + w / 2, y + (size + 13) / 2 + size * 0.36, label, size=size, weight="600", fill=color["label"], anchor="middle")
        return w

    def number(self, cx, cy, n, color, r=13):
        self.add(f'<circle cx="{cx}" cy="{cy}" r="{r}" fill="{color}"/>')
        self.text(cx, cy + 5, str(n), size=14, weight="700", fill="#FFFFFF", anchor="middle")

    def arrow(self, x1, y1, x2, y2, color=LINE, dash=None, width=2):
        d = f' stroke-dasharray="{dash}"' if dash else ""
        self.add(f'<line x1="{x1}" y1="{y1}" x2="{x2}" y2="{y2}" stroke="{color}" stroke-width="{width}"{d} marker-end="url(#arrow)"/>')

    def path(self, d, color=LINE, dash=None, width=2):
        da = f' stroke-dasharray="{dash}"' if dash else ""
        self.add(f'<path d="{d}" fill="none" stroke="{color}" stroke-width="{width}"{da} marker-end="url(#arrow)"/>')

    def header(self, title, subtitle):
        self.text(40, 56, title, size=28, weight="700")
        self.text(40, 88, subtitle, size=17, fill=MUTED)

    def render(self):
        head = (
            f'<svg xmlns="http://www.w3.org/2000/svg" width="{self.w}" height="{self.h}" '
            f'viewBox="0 0 {self.w} {self.h}" role="img" aria-labelledby="t d" '
            f'font-family="{FONT}">\n'
            f'<title id="t">{escape(self.title)}</title>\n<desc id="d">{escape(self.desc)}</desc>\n'
            '<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" '
            f'orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="{LINE}"/></marker></defs>\n'
            f'<rect x="0.5" y="0.5" width="{self.w - 1}" height="{self.h - 1}" rx="16" fill="#FFFFFF" stroke="#E5E7EB"/>\n'
        )
        return head + "\n".join(self.parts) + "\n</svg>\n"


def tiers():
    s = Svg(1200, 840, "How Bloem is organized",
            "Four nested tiers: the platform contains organizations, organizations contain accounts, "
            "accounts contain profiles. A legend explains each tier.")
    s.header("How Bloem is organized", "Four tiers. Each one lives inside the one above it.")
    p, o, a, f = TIERS["platform"], TIERS["org"], TIERS["account"], TIERS["profile"]

    # Tier 1: the platform.
    s.rect(40, 112, 760, 690, p["fill"], p["stroke"], rx=20, sw=2)
    s.number(66, 141, 1, p["stroke"])
    s.text(88, 146, "THE PLATFORM", size=14, weight="700", fill=p["label"], spacing="1")
    s.text(64, 174, "The Bloem service: its servers, its libraries and its platform administrators.", size=15)
    s.text(64, 206, "Platform libraries", size=13, weight="600", fill=SOFT)
    x = 64
    for name in ["Movies", "Series", "Audiobooks", "Ebooks", "Live TV"]:
        x += s.badge(x, 216, name, {"fill": "#FFFFFF", "stroke": "#FDBA74", "label": INK}, size=14) + 10

    def account(x, y, email, tag, tag_color, people):
        s.rect(x, y, 308, 162, a["fill"], a["stroke"], rx=14)
        s.number(x + 18, y + 22, 3, a["stroke"], r=11)
        s.text(x + 36, y + 27, "ACCOUNT", size=12.5, weight="700", fill=a["label"], spacing="1")
        s.text(x + 16, y + 52, email, size=15, weight="600")
        s.badge(x + 292, y + 10, tag, tag_color, size=12, anchor="end")
        s.number(x + 18, y + 78, 4, f["stroke"], r=11)
        s.text(x + 36, y + 83, "PROFILES", size=12.5, weight="700", fill=f["label"], spacing="1")
        cw = (308 - 32 - 10 * (len(people) - 1)) / len(people)
        for i, (name, note) in enumerate(people):
            cx = x + 16 + i * (cw + 10)
            s.rect(cx, y + 96, cw, 52, f["fill"], f["stroke"], rx=10, sw=1.2)
            s.text(cx + cw / 2, y + (118 if note else 127), name, size=15, weight="600", anchor="middle")
            if note:
                s.text(cx + cw / 2, y + 137, note, size=12, fill=MUTED, anchor="middle")

    plan = TIERS["plan"]
    # Tier 2: two organizations.
    for ox, name, rows in [
        (64, "Default organization", ["Bloem's own direct customers,", "each account managed one by one."]),
        (428, "Riverside Club", ["A reseller sold a block of seats on", "the storefront: 10 seats, 2 shared", "video conversions."]),
    ]:
        s.rect(ox, 268, 348, 516, o["fill"], o["stroke"], rx=16, sw=1.6)
        s.number(ox + 22, 294, 2, o["stroke"], r=12)
        s.text(ox + 42, 299, "ORGANIZATION", size=13, weight="700", fill=o["label"], spacing="1")
        s.text(ox + 20, 326, name, size=18, weight="700")
        s.lines(ox + 20, 348, rows, size=13.5, gap=18)
    s.badge(448, 394, "Its own library: Club matches", {"fill": "#FFFFFF", "stroke": "#FDBA74", "label": INK}, size=12)
    s.badge(84, 394, "Granted: every platform library", {"fill": "#FFFFFF", "stroke": "#FDBA74", "label": INK}, size=12)

    account(84, 432, "jansen@example.com", "Plan: Standard", plan,
            [("Anna", "PIN"), ("Tom", ""), ("Kids", "ages 7+")])
    account(84, 606, "devries@example.com", "Plan: Premium", plan,
            [("Eva", ""), ("Sem", "")])
    account(448, 432, "coach@riverside.example", "Admin", o,
            [("Coach", "")])
    account(448, 606, "sam@riverside.example", "Member", plan,
            [("Sam", ""), ("Lee", "ages 12+")])

    # Legend.
    s.text(828, 146, "What each tier means", size=17, weight="700")
    cards = [
        (p, 1, "Platform", ["The Bloem service itself. It owns",
                            "the servers and the main libraries.",
                            "Run by platform administrators."]),
        (o, 2, "Organization", ["One customer: Bloem's direct customers",
                                "together, or a reseller, club or business.",
                                "Sees only its own people, libraries",
                                "and settings. Run by its admin."]),
        (a, 3, "Account", ["One sign-in (email and password).",
                           "Belongs to exactly one organization.",
                           "Its plan sets screens, quality and",
                           "downloads."]),
        (f, 4, "Profile", ["One person using the account. Can",
                           "have a PIN, an age limit and only",
                           "the libraries chosen for them."]),
    ]
    y = 166
    for color, n, title, rows in cards:
        h = 58 + 20 * len(rows)
        s.rect(828, y, 332, h, "#FFFFFF", "#E5E7EB", rx=12)
        s.rect(828, y, 8, h, color["stroke"], rx=4)
        s.number(862, y + 26, n, color["stroke"], r=12)
        s.text(884, y + 32, title, size=17, weight="700")
        s.lines(852, y + 58, rows, size=14, gap=20)
        y += h + 14
    return s


def funnel():
    s = Svg(1200, 760, "What can this person watch?",
            "A funnel. The server's libraries narrow to what the organization may use, then to what the "
            "account's plan allows, then to what the profile may see, which is what appears on screen.")
    s.header("What can this person watch?",
             "The organization sets the outer limit. The plan and the profile narrow it down.")
    steps = [
        (TIERS["platform"], 1, 760, "Everything on the Bloem server",
         "All libraries: movies, series, audiobooks, ebooks, Live TV",
         ["Platform administrators", "add libraries."]),
        (TIERS["org"], 2, 664, "What the organization may use",
         "Libraries it owns, plus platform libraries granted to it",
         ["Platform administrators grant", "libraries. A new organization", "gets a standard starting set."]),
        (TIERS["account"], 3, 568, "What the account's plan allows",
         "Which libraries, how many screens, quality, downloads",
         ["The plan the storefront or an", "admin puts the account on. An", "admin can adjust one account."]),
        (TIERS["profile"], 4, 472, "What this profile may see",
         "Kids profiles, age limits, a chosen set of libraries",
         ["The account's main profile,", "who manages the household."]),
    ]
    cx, y, h, gap = 420, 120, 96, 26
    s.text(880, 112, "Who sets it", size=17, weight="700")
    for color, n, w, title, desc, who in steps:
        x = cx - w / 2
        s.rect(x, y, w, h, color["fill"], color["stroke"], rx=16, sw=2)
        s.number(x + 30, y + h / 2, n, color["stroke"], r=14)
        s.text(cx + 14, y + 40, title, size=19, weight="700", anchor="middle")
        s.text(cx + 14, y + 66, desc, size=14, fill=MUTED, anchor="middle")
        s.rect(880, y + 6, 280, h - 12, "#FFFFFF", "#E5E7EB", rx=12)
        s.rect(880, y + 6, 6, h - 12, color["stroke"], rx=3)
        s.lines(902, y + h / 2 + 5 - (len(who) - 1) * 9.5, who, size=14, gap=19, fill=INK)
        s.add(f'<line x1="{cx + w / 2 + 8}" y1="{y + h / 2}" x2="872" y2="{y + h / 2}" stroke="#E5E7EB" stroke-width="2" stroke-dasharray="4 5"/>')
        if n < len(steps):
            s.arrow(cx, y + h + 2, cx, y + h + gap - 3)
        y += h + gap
    s.arrow(cx, y - gap + 2, cx, y - 3)
    s.rect(cx - 170, y, 340, 72, INK, rx=36)
    s.text(cx, y + 45, "What appears on screen", size=20, weight="700", fill="#FFFFFF", anchor="middle")
    s.lines(40, y + 112, [
        "Step 2 is a hard ceiling: nothing below it can reach a library the organization does not own or hold.",
        "A browse-only plan shows the catalog but never plays anything.",
    ], size=14, gap=22)
    return s


def lifecycle():
    s = Svg(1200, 790, "How an organization starts, runs and ends",
            "Five steps from a storefront sale to people watching, an alternative where a platform "
            "administrator creates the organization by hand, and the organization's four states.")
    s.header("How an organization starts, runs and ends",
             "Direct customers are accounts in the default organization. A reseller gets an organization of its own.")
    o, a, g, w = TIERS["org"], TIERS["account"], TIERS["grey"], TIERS["warn"]
    steps = [
        ("A reseller buys", "a block of seats", ["On the storefront", "(Bloem Garden): a club,", "business or reseller", "with its own members."], None),
        ("The storefront asks", "Bloem to create it", ["With its number of", "seats and shared video", "conversions."], ("Getting ready", g)),
        ("Bloem gives it", "starting libraries", ["The platform's starting", "set of libraries and", "plugins is granted."], None),
        ("The first admin", "account is added", ["The organization's", "own administrator."], ("Active", a)),
        ("People are added", "and watch", ["The organization's admin", "adds them. Each takes a", "seat and gets the", "plan's limits."], None),
    ]
    x0, y0, cw, ch, gap = 40, 124, 204, 196, 26
    for i, (t1, t2, body, state) in enumerate(steps):
        x = x0 + i * (cw + gap)
        s.rect(x, y0, cw, ch, o["fill"], o["stroke"], rx=14)
        s.number(x + 26, y0 + 28, i + 1, o["stroke"])
        s.text(x + 16, y0 + 70, t1, size=16, weight="700")
        s.text(x + 16, y0 + 91, t2, size=16, weight="700")
        s.lines(x + 16, y0 + 118, body, size=13.5, gap=18)
        if state:
            s.badge(x + cw - 14, y0 + 14, state[0], state[1], size=12.5, anchor="end")
        if i < len(steps) - 1:
            s.arrow(x + cw + 3, y0 + ch / 2, x + cw + gap - 3, y0 + ch / 2)

    # Alternative entry by hand.
    s.rect(40, 352, 434, 70, "#FFFFFF", LINE, rx=12, dash="6 5")
    s.lines(60, 381, ["Or a platform administrator creates an organization by",
                      "hand in the admin console, with its owner. It starts active."],
            size=14, gap=20, fill=INK)
    s.path(f"M 474 387 L 602 387 L 602 {y0 + ch + 4}", dash="6 5")

    # States.
    s.text(40, 474, "Its states", size=19, weight="700")
    states = [
        (40, "Getting ready", g, ["Sold, but no admin yet."]),
        (300, "Active", a, ["Members sign in and watch."]),
        (700, "Suspended", w, ["Members are refused until", "it is active again."]),
        (980, "Retired", g, ["Members removed. Accounts", "with no other home are", "deleted. The record stays."]),
    ]
    sy, sw_, sh = 548, 180, 56
    for x, label, color, rows in states:
        s.rect(x, sy, sw_, sh, color["fill"], color["stroke"], rx=28, sw=2)
        s.text(x + sw_ / 2, sy + 36, label, size=18, weight="700", fill=color["label"], anchor="middle")
        s.lines(x + 4, sy + sh + 32, rows, size=14, gap=20, fill=MUTED)
    s.arrow(223, sy + sh / 2, 296, sy + sh / 2)
    s.lines(260, sy + sh / 2 - 12, ["first admin"], size=12.5, fill=SOFT, anchor="middle")
    s.path(f"M 482 {sy + 14} C 540 {sy - 36}, 640 {sy - 36}, 696 {sy + 14}")
    s.lines(590, 498, ["Too many people for its seats, or stopped",
                       "by an admin or the storefront (e.g. unpaid)"], size=12.5, fill=SOFT, gap=16, anchor="middle")
    s.path(f"M 698 {sy + sh - 12} C 640 {sy + sh + 34}, 540 {sy + sh + 34}, 486 {sy + sh - 12}")
    s.lines(592, sy + sh + 46, ["Back under its seats by itself,", "or lifted by hand"], size=12.5, fill=SOFT, gap=16, anchor="middle")
    s.arrow(883, sy + sh / 2, 976, sy + sh / 2)
    s.lines(930, sy - 14, ["storefront ends it", "(from any state)"], size=12.5, fill=SOFT, gap=16, anchor="middle")
    s.lines(40, 756, ["An organization is never deleted outright. A retired one stays on record as the owner of anything it had."],
            size=14, fill=INK)
    return s


def press_play():
    s = Svg(1200, 860, "What happens when someone presses play",
            "Five checks run in order: who is watching, whether the organization is active, whether the "
            "profile may see the title, whether a screen is free, and whether there is room to convert "
            "the video. Each refusal is shown beside its check.")
    s.header("What happens when someone presses play",
             "Bloem runs these checks in a fraction of a second. Any “no” stops the video.")
    o, a, f, st, wa = TIERS["org"], TIERS["account"], TIERS["profile"], TIERS["stop"], TIERS["warn"]
    checks = [
        (a, "Who is watching?", ["The app has signed in to an account and picked a profile.",
                                 "Bloem looks up the account's one organization."], None),
        (o, "Is the organization active?", ["A suspended organization's members are refused."],
         (st, ["Refused: the organization", "is suspended."])),
        (f, "May this profile see this title?", ["The organization, plan and profile limits",
                                                 "from “What can this person watch?”"],
         (st, ["The title is not shown", "at all."])),
        (a, "Is a screen free?", ["The plan sets how many videos the account",
                                  "can play at the same time."],
         (wa, ["Refused. If switched on, the", "viewer also gets an upgrade", "offer in their inbox."])),
        (o, "If the video needs converting, is there room?",
         ["The account's own limit, and for a storefront group,",
          "the group's shared pool of conversions."],
         (st, ["Refused until a conversion", "frees up."])),
    ]
    x, y, w, h, gap = 40, 124, 640, 96, 34
    for i, (color, title, rows, no) in enumerate(checks):
        s.rect(x, y, w, h, color["fill"], color["stroke"], rx=14, sw=1.8)
        s.number(x + 28, y + 30, i + 1, color["stroke"])
        s.text(x + 52, y + 36, title, size=18, weight="700")
        s.lines(x + 52, y + 62, rows, size=14, gap=19)
        if no:
            color_no, out = no
            s.arrow(x + w + 4, y + h / 2, 776, y + h / 2)
            s.text(x + w + 22, y + h / 2 - 8, "No", size=14, weight="700", fill=color_no["label"])
            s.rect(780, y + 8, 380, h - 16, color_no["fill"], color_no["stroke"], rx=12, sw=1.5)
            s.lines(800, y + 40, out, size=15, gap=20, fill=INK)
        s.arrow(x + w / 2, y + h + 2, x + w / 2, y + h + gap - 3)
        s.text(x + w / 2 + 12, y + h + 22, "Yes" if no else "", size=13, weight="700", fill=TIERS["account"]["label"])
        y += h + gap
    s.rect(x + w / 2 - 150, y, 300, 66, INK, rx=33)
    s.text(x + w / 2, y + 42, "The video plays", size=21, weight="700", fill="#FFFFFF", anchor="middle")
    s.lines(780, y + 22, ["While it plays, Bloem checks library", "access again each time it sends video."],
            size=14, gap=20, fill=MUTED)
    return s


def main():
    out = sys.argv[1]
    os.makedirs(out, exist_ok=True)
    for name, fn in [("tiers", tiers), ("what-can-i-watch", funnel), ("lifecycle", lifecycle), ("press-play", press_play)]:
        with open(os.path.join(out, f"{name}.svg"), "w") as fh:
            fh.write(fn().render())
        print(name)


if __name__ == "__main__":
    main()
