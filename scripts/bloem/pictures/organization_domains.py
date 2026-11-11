"""Pictures for docs/architecture/bloem-organization-domains.md."""
from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

# Accent colours of the made-up organizations in these pictures.
SERVER = "#FA6400"
RIVERSIDE = "#6366F1"
HARBOUR = "#14B8A6"


def browser(s, x, y, w, h, address, name, sub, accent, dashed=False):
    """A small browser window showing a sign-in page in one organization's branding."""
    s.rect(x, y, w, h, "#FFFFFF", LINE if dashed else "#D1D5DB", rx=12, sw=1.5, dash="6 5" if dashed else None)
    s.add(f'<path d="M{x + 1},{y + 32} L{x + w - 1},{y + 32}" stroke="#E5E7EB" stroke-width="1.5"/>')
    for i, dot in enumerate(["#F87171", "#FBBF24", "#34D399"]):
        s.add(f'<circle cx="{x + 18 + i * 14}" cy="{y + 16}" r="4.5" fill="{dot}"/>')
    s.rect(x + 66, y + 6, w - 82, 20, "#F3F4F6", rx=10, sw=0)
    s.text(x + 78, y + 21, address, size=13, fill=INK, weight="600")
    if accent:
        s.add(f'<circle cx="{x + 34}" cy="{y + 66}" r="17" fill="{accent}"/>')
        s.text(x + 34, y + 72, name[0], size=17, weight="700", fill="#FFFFFF", anchor="middle")
        s.text(x + 62, y + 63, name, size=16, weight="700")
        s.text(x + 62, y + 83, sub, size=13, fill=MUTED)
        s.rect(x + w - 104, y + 52, 88, 30, accent, rx=8, sw=0)
        s.text(x + w - 60, y + 72, "Sign in", size=13.5, weight="700", fill="#FFFFFF", anchor="middle")
    else:
        s.text(x + 18, y + 62, name, size=15, weight="700")
        s.text(x + 18, y + 82, sub, size=13, fill=MUTED)


def domains():
    s = Svg(1200, 760, "One server, several addresses",
            "Four browser windows reach one Bloem server: the server's public address shows the default "
            "organization's branding, two organizations' own addresses show their own names, logos and colours, "
            "and an unregistered address such as an IP address shows the server's branding. Inside the server, a "
            "list maps each registered address to its organization, which picks the branding and the address "
            "emails link to.")
    s.header("One server, several addresses",
             "Each organization can have addresses of its own, with its own name, logo and colours.")
    p, o, pl, g = TIERS["platform"], TIERS["org"], TIERS["plan"], TIERS["grey"]

    windows = [
        (124, "bloem.example", "Bloem", "the server's public address", SERVER, False),
        (254, "watch.riverside.example", "Riverside Club TV", "Riverside Club's own address", RIVERSIDE, False),
        (384, "tv.harbour.example", "Harbour Hotel", "Harbour Hotel's own address", HARBOUR, False),
        (514, "an IP address or network name", "Any other address", "one that no organization registered", None, True),
    ]
    for y, address, name, sub, accent, dashed in windows:
        browser(s, 40, y, 420, 106, address, name, sub, accent, dashed)
        s.arrow(466, y + 53, 524, y + 53, dash="6 5" if dashed else None)

    # The server.
    s.rect(530, 116, 630, 520, p["fill"], p["stroke"], rx=18, sw=2)
    s.text(554, 150, "Bloem server", size=20, weight="700")
    s.text(554, 174, "1. Which organization is this address for?", size=15, weight="700", fill=p["label"])
    cols = [554, 776, 930]
    for x, head in zip(cols, ["Address", "Organization", "Shows"]):
        s.text(x, 204, head, size=12.5, weight="700", fill=SOFT, spacing="0.5")
    rows = [
        ("bloem.example", "Default", "the server's brand", SERVER, True),
        ("watch.riverside.example", "Riverside Club", "its own brand", RIVERSIDE, True),
        ("riverside.example", "Riverside Club", "its own brand", RIVERSIDE, False),
        ("tv.harbour.example", "Harbour Hotel", "its own brand", HARBOUR, True),
    ]
    y = 214
    for host, org, shows, accent, primary in rows:
        s.rect(546, y, 598, 40, "#FFFFFF", "#E5E7EB", rx=8, sw=1)
        s.rect(546, y, 6, 40, accent, rx=3, sw=0)
        s.text(cols[0] + 8, y + 26, host, size=14, weight="600")
        s.text(cols[1], y + 26, org, size=14)
        s.text(cols[2], y + 26, shows, size=14, fill=MUTED)
        if primary:
            s.badge(1136, y + 8, "primary", {"fill": "#FFFFFF", "stroke": accent, "label": INK}, size=11, anchor="end")
        y += 48
    s.rect(546, y, 598, 40, "#FFFFFF", LINE, rx=8, sw=1, dash="6 5")
    s.text(cols[0] + 8, y + 26, "anything else", size=14, weight="600", fill=SOFT)
    s.text(cols[1], y + 26, "none", size=14, fill=SOFT)
    s.text(cols[2], y + 26, "the server's brand", size=14, fill=MUTED)

    s.text(554, 486, "2. That organization's look and links", size=15, weight="700", fill=p["label"])
    para(s, 554, 512, "Its name, logos, browser icon, accent colour, sign-in subtitle and background, and "
         "default theme. Its invitation and password-reset emails link to its primary address. A value it has "
         "not set is Bloem's built-in default, never the server's brand.", 580, size=14, gap=20)
    s.text(554, 612, "Each server keeps this list in memory and reads it again within 30 seconds of a change.",
           size=13, fill=SOFT)

    s.rect(40, 660, 1120, 64, o["fill"], o["stroke"], rx=14, sw=1.6)
    s.text(64, 688, "An address decides what people see, never what they can reach.", size=16, weight="700")
    s.text(64, 710, "An account only ever reaches its own organization, whichever address it uses.", size=14,
           fill=MUTED)
    return s


def wrong_domain():
    s = Svg(1200, 650, "Signing in at the wrong address",
            "Sam, a Riverside Club member, signs in at Harbour Hotel's address. Bloem sees that the address belongs "
            "to Harbour Hotel and Sam's account to Riverside Club, and refuses before any sign-in exists, naming "
            "Riverside Club's address. Sam signs in there. A panel lists the cases that are never refused.")
    s.header("Signing in at the wrong address",
             "The refusal comes before any sign-in exists, and says where to go instead.")
    a, o, st, pl = TIERS["account"], TIERS["org"], TIERS["stop"], TIERS["plan"]

    steps = [
        (pl, "Sam signs in at", "tv.harbour.example",
         "Sam is a member of Riverside Club. This is Harbour Hotel's address."),
        (o, "Bloem compares", "two organizations",
         "The address is Harbour Hotel's. Sam's account is Riverside Club's. They differ."),
        (st, "Refused, with", "the right address",
         "“This account signs in at https://watch.riverside.example.”"),
        (a, "Sam signs in at", "watch.riverside.example",
         "The sign-in works, in Riverside Club's own branding."),
    ]
    x, y, w, h, gap = 40, 124, 262, 180, 24
    for i, (c, t1, t2, body) in enumerate(steps):
        bx = x + i * (w + gap)
        s.rect(bx, y, w, h, c["fill"], c["stroke"], rx=14, sw=1.8)
        s.number(bx + 26, y + 28, i + 1, c["stroke"])
        s.text(bx + 18, y + 72, t1, size=16, weight="700")
        s.text(bx + 18, y + 94, t2, size=16 if i != 0 and i != 3 else 14.5, weight="700")
        para(s, bx + 18, y + 124, body, w - 36, size=14, gap=19)
        if i < len(steps) - 1:
            s.arrow(bx + w + 2, y + h / 2, bx + w + gap - 2, y + h / 2)
    no_mark(s, 40 + 2 * (w + gap) + w - 24, y + 28)
    yes_mark(s, 40 + 3 * (w + gap) + w - 24, y + 28)

    s.text(40, 344, "The answer, for developers", size=13, weight="700", fill=SOFT, spacing="0.5")
    s.rect(40, 356, 1120, 48, "#FFFFFF", "#E5E7EB", rx=10)
    s.text(60, 386, "409 wrong_organization_domain  ·  /api/v2 renders it as conflict, and the sign-in page "
                    "shows the message", size=14.5, weight="600")

    s.text(40, 450, "Never refused", size=17, weight="700")
    cases = [
        ("An address no organization registered,",
         "such as the server's IP address or a home-network name, so an administrator can always get in."),
        ("An organization without an address of its own,",
         "at the server's public address: that is where it signs in, even when the default organization registered it."),
        ("An administrator acting as someone else",
         "(impersonation). That is not a sign-in."),
    ]
    yy = 470
    for title, body in cases:
        yes_mark(s, 56, yy + 14, r=11)
        s.text(80, yy + 19, title, size=15, weight="700")
        s.text(80, yy + 40, body, size=14, fill=MUTED)
        yy += 62
    return s


def provider_sign_in():
    s = Svg(1200, 846, "Signing in with the provider stays on the address",
            "A sequence between the browser on Riverside Club's address, the Bloem server and the sign-in provider "
            "(Authentik). Bloem asks the provider to return to that address's callback; the provider returns there; "
            "the sign-in finishes on that address where the browser's cookies are; a sign-out returns to that "
            "address's login page. A box lists the two addresses to register at the provider for each domain.")
    s.header("Signing in with the provider stays on the address",
             "A sign-in or sign-out through Authentik ends on the address where it started.")
    a, o, p, g = TIERS["account"], TIERS["org"], TIERS["platform"], TIERS["grey"]

    lanes = [(210, "Browser", "on watch.riverside.example", o), (600, "Bloem server", "", p),
             (990, "Sign-in provider", "Authentik", a)]
    top, bottom = 120, 640
    for cx, title, sub, c in lanes:
        s.rect(cx - 150, top, 300, 62, c["fill"], c["stroke"], rx=14, sw=1.8)
        s.text(cx, top + 28 if sub else top + 38, title, size=17, weight="700", anchor="middle")
        if sub:
            s.text(cx, top + 49, sub, size=13.5, fill=MUTED, anchor="middle")
        s.add(f'<line x1="{cx}" y1="{top + 64}" x2="{cx}" y2="{bottom}" stroke="#E5E7EB" stroke-width="3"/>')

    def step(n, y, x1, x2, label, sub=None, dash=None):
        s.arrow(x1, y, x2, y, dash=dash)
        # Labels of arrows that cross the server's lifeline sit between the
        # server and the provider, so no line runs through them.
        mid = (x1 + x2) / 2 if abs(x2 - x1) < 500 else 795
        s.number(min(x1, x2) + 26, y - 18, n, INK, r=12)
        s.text(mid + 10, y - 13, label, size=14.5, weight="600", anchor="middle")
        if sub:
            s.text(mid + 10, y + 21, sub, size=13, fill=MUTED, anchor="middle")

    step(1, 232, 214, 596, "“Sign in with Authentik”")
    step(2, 300, 596, 214, "Off to Authentik, with a return address", "watch.riverside.example/…/callback")
    s.arrow(214, 352, 986, 352)
    s.text(805, 342, "(the browser follows)", size=13, fill=SOFT, anchor="middle")
    step(3, 420, 986, 214, "Sam is checked; back to that callback", "on watch.riverside.example")
    step(4, 488, 214, 596, "The callback reaches Bloem", "Bloem finishes on that address's page, where the cookies are")
    s.rect(60, 530, 300, 34, a["fill"], a["stroke"], rx=17, sw=1.4)
    s.text(210, 552, "Signed in, on the same address", size=14, weight="700", fill=a["label"], anchor="middle")
    step(5, 610, 986, 214, "Signing out: Authentik sends the browser back to", "watch.riverside.example/login", dash="6 5")

    s.rect(40, 670, 1120, 144, "#FFFFFF", "#E5E7EB", rx=14)
    s.rect(40, 670, 8, 144, p["stroke"], rx=4)
    s.text(66, 702, "Register these at the provider for each address", size=17, weight="700")
    urls = ["https://<address>/api/v2/auth/oauth/<installation>/callback",
            "https://<address>/login   (post-logout redirect, when provider sign-out is on)"]
    for i, u in enumerate(urls):
        s.text(66, 732 + i * 24, u, size=14.5, weight="600", fill=INK)
    s.text(66, 796, "Apps on phones and TVs, and any address no organization registered, keep using the server's "
                    "public address.", size=13.5, fill=MUTED)
    return s


PICTURES = [("domains", domains), ("wrong-domain", wrong_domain), ("provider-sign-in", provider_sign_in)]
