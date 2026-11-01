"""Pictures for docs/wiki/admin-guide.md."""
from xml.sax.saxutils import escape

from kit import INK, MUTED, SOFT, TIERS, Svg, box, card, footer, para

MONO = "ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace"
WHITE_BADGE = {"fill": "#FFFFFF", "stroke": "#D1D5DB", "label": INK}


def mono(s, x, y, text, size=14, fill=INK, weight="normal"):
    s.add(f'<text x="{x}" y="{y}" font-size="{size}" font-weight="{weight}" fill="{fill}" '
          f'font-family="{MONO}">{escape(text)}</text>')


def badge_rows(s, x, y, width, labels, color, size=13, gap=10, row_gap=36):
    """Badges laid left to right, wrapping at width; return the y below the last row."""
    cx, cy = x, y
    for label in labels:
        w = round(len(label) * size * 0.58 + 22)
        if cx > x and cx + w > x + width:
            cx, cy = x, cy + row_gap
        s.badge(cx, cy, label, color, size=size)
        cx += w + gap
    return cy + row_gap


def install():
    s = Svg(1200, 600, "From a blank machine to watching",
            "Six steps in two rows: get the files, add two secrets, point at your media, start it, run the "
            "setup wizard, and invite people. Each step names the section of the guide that explains it.")
    s.header("From a blank machine to watching",
             "Part 1 in six steps. Each one has its own section below.")
    o, a = TIERS["org"], TIERS["account"]
    steps = [
        (o, "Get the files", "Clone the repository and copy Bloem's example settings, .env.bloem.example, to .env.", "1.3"),
        (o, "Add two secrets", "A database password, and SECRET_KEY, which encrypts every credential the server stores.", "1.3"),
        (o, "Point at your media", "Set MEDIA_ROOT to your media folder. Bloem reads it and never writes to it.", "1.3"),
        (o, "Start it", "docker compose up -d starts Bloem, PostgreSQL and Redis. The first start takes a few minutes.", "1.3"),
        (a, "Run the setup wizard", "Open port 8090 in a browser. Only the first step, the administrator account, is required.", "1.4"),
        (a, "Invite people", "Users → Invitations or Invite Codes. Each viewer gets an account with its own profiles.", "1.8"),
    ]
    w, h, gap = 346, 150, 41
    for i, (color, title, body, section) in enumerate(steps):
        col, row = i % 3, i // 3
        x, y = 40 + col * (w + gap), 124 + row * (h + 64)
        card(s, x, y, w, h, color, title, body, n=i + 1)
        s.badge(x + w - 14, y + 14, section, WHITE_BADGE, size=12, anchor="end")
        if col < 2:
            s.arrow(x + w + 4, y + h / 2, x + w + gap - 4, y + h / 2)
    s.path(f"M {40 + 2 * (w + gap) + w / 2} {124 + h + 4} L {40 + 2 * (w + gap) + w / 2} {124 + h + 32} "
           f"L {40 + w / 2} {124 + h + 32} L {40 + w / 2} {124 + h + 60}")
    footer(s, "A first scan of a large library takes a while. Watch it on Libraries and in Scheduled Tasks.",
           y=566, fill=INK)
    return s


def disk():
    s = Svg(1200, 520, "Where things live, and what to back up",
            "Three columns. Your media, mounted read-only and never written by Bloem. Bloem's own data under "
            "SILO_DATA_ROOT: the database, Redis, artwork, plugins, prepared downloads and transcode scratch. "
            "The .env file with SECRET_KEY, kept away from the server.")
    s.header("Where things live, and what to back up",
             "Your media and Bloem's own data are kept apart. Back up three things, and test a restore once.")
    g, p, w = TIERS["grey"], TIERS["platform"], TIERS["warn"]
    cols = [
        (40, g, "Your media", "MEDIA_ROOT on the host",
         ["Mounted read-only into the", "container at /mnt/media.", "Bloem never writes to it."],
         "Back it up as you already do", g),
        (421, p, "Bloem's own data", "SILO_DATA_ROOT (default /opt/silo)",
         ["The database (/opt/silo/postgres)", "Redis", "Downloaded artwork", "Plugins and compatibility assets",
          "Prepared downloads", "Transcode scratch"],
         "Back up: pg_dump and the folder", TIERS["account"]),
        (802, w, ".env and SECRET_KEY", "Your settings file",
         ["SECRET_KEY encrypts every", "stored credential. A database", "backup holds the encrypted", "values, not the key."],
         "Keep a copy off the server", w),
    ]
    for x, color, title, where, rows, keep, keep_color in cols:
        s.rect(x, 124, 357, 300, color["fill"], color["stroke"], rx=16, sw=1.8)
        s.text(x + 24, 162, title, size=20, weight="700")
        mono(s, x + 24, 192, where, size=13.5, fill=color["label"], weight="600")
        s.lines(x + 24, 232, rows, size=15, gap=24, fill=INK)
        s.badge(x + 24, 378, keep, keep_color, size=13)
    footer(s, "Never copy the database folder while it runs: dump it with pg_dump on a schedule. Losing artwork "
              "is slow to recover, because the server downloads it again; losing SECRET_KEY is not recoverable.",
           y=482, fill=INK)
    return s


def contexts():
    s = Svg(1200, 790, "Two ways to administer",
            "Choose context leads to two sidebars. Platform: the whole server, with Bloem's Platform pages and "
            "Silo's Monitor, Manage and Configure groups. An organization: nine pages for that organization only.")
    s.header("Two ways to administer",
             "Choose a context first. The sidebar then shows only the pages for that context.")
    p, o = TIERS["platform"], TIERS["org"]

    s.rect(480, 112, 240, 48, INK, rx=24)
    s.text(600, 143, "Choose context", size=18, weight="700", fill="#FFFFFF", anchor="middle")
    s.path("M 480 136 L 350 136 L 350 196")
    s.path("M 720 136 L 930 136 L 930 196")

    # Platform.
    s.rect(40, 200, 620, 500, p["fill"], p["stroke"], rx=18, sw=2)
    s.text(64, 236, "PLATFORM", size=14, weight="700", fill=p["label"], spacing="1")
    s.text(64, 262, "The whole server. For platform administrators.", size=15)
    groups = [
        ("Platform (Bloem's pages)", ["Organizations", "Entitlement Templates", "Campaigns & Seasonal Packs",
                                      "Direct Accounts", "Bulk Account Policies"]),
        ("Monitor", ["Dashboard", "Activity", "Logs"]),
        ("Manage", ["Libraries", "Autoscan", "Requests", "Live TV", "Users", "Playback", "Downloads"]),
        ("Configure", ["Server", "Sign-in & Security", "Integrations"]),
    ]
    y = 300
    for label, items in groups:
        s.text(64, y, label, size=14, weight="700", fill=SOFT)
        y = badge_rows(s, 64, y + 12, 572, items, {"fill": "#FFFFFF", "stroke": "#FDBA74", "label": INK}) + 14

    # Organization.
    s.rect(700, 200, 460, 500, o["fill"], o["stroke"], rx=18, sw=2)
    s.text(724, 236, "AN ORGANIZATION", size=14, weight="700", fill=o["label"], spacing="1")
    para(s, 724, 262, "One organization only. For its organization administrators.", 420, size=15, fill=INK)
    pages = ["Overview", "People", "Policy Cohorts", "Access Groups", "Libraries & Entitlements",
             "Storage sources", "Invitations", "Policy Decisions", "Activity & Audit"]
    for i, name in enumerate(pages):
        y = 304 + i * 42
        s.rect(724, y, 412, 34, "#FFFFFF", "#C7D2FE", rx=8, sw=1.2)
        s.text(742, y + 23, name, size=14.5, weight="600")

    footer(s, "Admin pages also need a selected profile. Being a household's main profile does not make an "
              "account a platform administrator, and an organization administrator cannot open platform pages.",
           y=754, fill=INK)
    return s


def new_domain():
    s = Svg(1200, 730, "Giving an organization its own address",
            "Four steps: DNS and a certificate, a route at a trusted proxy, the sign-in provider's addresses for "
            "the domain, and registering the domain in the Domains panel. Below them, the three addresses to "
            "register at the provider, and what visitors on the domain get: its own look, its own email links and "
            "its own sign-in. A made-up domain is used.")
    s.header("Giving an organization its own address",
             "Four steps. Then visitors on that address see the organization's own name, logo and colours.")
    o, a, st, g, w = TIERS["org"], TIERS["account"], TIERS["stop"], TIERS["grey"], TIERS["warn"]
    steps = [
        ("DNS, certificate", "Point the domain at your proxy and issue a certificate for it."),
        ("Proxy route", "A trusted proxy sends the domain to Bloem and passes on the visitor's host."),
        ("Sign-in provider", "Only for sign-in through Authentik: register the domain's addresses below."),
        ("Register it", "Platform → Organizations → the organization → Domains → Add domain."),
    ]
    cw, ch, gap = 260, 136, 26
    for i, (title, body) in enumerate(steps):
        x = 40 + i * (cw + gap)
        card(s, x, 124, cw, ch, o, title, body, n=i + 1, title_size=16.5)
        if i < len(steps) - 1:
            s.arrow(x + cw + 3, 124 + ch / 2, x + cw + gap - 3, 124 + ch / 2)

    # Provider addresses.
    s.rect(40, 290, 1120, 160, g["fill"], g["stroke"], rx=14, sw=1.5)
    s.text(64, 324, "At the provider, for each domain", size=17, weight="700")
    s.text(360, 324, "Made-up domain: watch.riverside.example", size=14, fill=MUTED)
    rows = [
        ("Redirect URI", "https://watch.riverside.example/api/v1/auth/oauth/<installation>/callback", ""),
        ("Redirect URI", "https://watch.riverside.example/api/v2/auth/oauth/<installation>/callback", ""),
        ("Post-logout", "https://watch.riverside.example/login", "when signing out at the provider is on"),
    ]
    for i, (label, url, note) in enumerate(rows):
        y = 362 + i * 32
        s.text(64, y, label, size=14, weight="700", fill=g["label"])
        mono(s, 196, y, url, size=14)
        if note:
            s.text(560, y, f"({note})", size=13.5, fill=MUTED)

    # Results.
    s.text(40, 494, "What visitors on the domain get", size=19, weight="700")
    results = [
        (a, "Its own look", "Name, login subtitle, accent colour and images from the Branding panel. "
                            "An empty field shows Bloem's default, never another brand."),
        (o, "Its own email links", "Invitation and password-reset emails link to the primary domain and "
                                   "carry the organization's name."),
        (st, "Its own sign-in", "An account of another organization is refused there and told the "
                                "address where it signs in."),
    ]
    for i, (color, title, body) in enumerate(results):
        box(s, 40 + i * 381, 510, 357, 136, color, title, body, size=14)

    footer(s, "A domain decides what visitors see, never what an account can reach. An organization without a "
              "domain signs in at the server's public address.", y=690, fill=INK)
    return s


PICTURES = [("install", install), ("disk", disk), ("contexts", contexts), ("new-domain", new_domain)]
