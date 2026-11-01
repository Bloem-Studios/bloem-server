"""Pictures for docs/bloem/overlays/wiki/deployment/docker.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, card, footer, no_mark, para, yes_mark

WHITE = {"fill": "#FFFFFF", "stroke": "#FA6400", "label": "#C2410C"}


def stack():
    s = Svg(1200, 740, "What a Bloem installation runs",
            "People and apps reach a reverse proxy that holds the HTTPS certificates. The proxy passes them to "
            "the Bloem container, which has a main address on port 8090 and separate ports for Jellyfin apps "
            "(8096) and Audiobookshelf apps (13378). Bloem uses PostgreSQL, Redis, an optional Meilisearch, "
            "the media folder read-only and a data folder on the host.")
    s.header("What a Bloem installation runs",
             "One Compose file starts Bloem, PostgreSQL and Redis. Only the reverse proxy faces the internet.")
    p, o, a, g, w = TIERS["platform"], TIERS["org"], TIERS["account"], TIERS["grey"], TIERS["warn"]

    # People and apps.
    s.rect(40, 150, 220, 300, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(60, 184, "People and apps", size=17, weight="700")
    s.lines(60, 216, ["Web browser", "Bloem's apps", "Silo's apps", "Jellyfin apps", "Audiobookshelf apps"],
            size=15, fill=INK, gap=30)
    para(s, 60, 376, "Over the internet or the home network.", 180, size=13.5)
    s.arrow(264, 300, 296, 300)

    # Reverse proxy.
    box(s, 300, 206, 220, 188, o, "Reverse proxy",
        "Holds the HTTPS certificates. Passes on the domain each visitor asked for. Listed as a trusted "
        "proxy in Bloem.", size=14)
    s.arrow(524, 300, 556, 300)

    # The Bloem container.
    s.rect(560, 130, 300, 494, p["fill"], p["stroke"], rx=18, sw=2)
    s.text(710, 166, "Bloem container", size=20, weight="700", anchor="middle")
    s.text(710, 190, "MODE integrated", size=13.5, fill=SOFT, anchor="middle")
    doors = [(212, "Main address", "port 8090: web app and APIs"),
             (300, "Jellyfin apps", "port 8096, when allowed"),
             (388, "Audiobookshelf apps", "port 13378, when allowed")]
    for y, title, note in doors:
        s.rect(584, y, 252, 72, WHITE["fill"], WHITE["stroke"], rx=12, sw=1.6)
        s.text(710, y + 30, title, size=16, weight="700", anchor="middle")
        s.text(710, y + 54, note, size=13.5, fill=MUTED, anchor="middle")
    para(s, 584, 492, "Inside: the API, the web app, scanners, background workers and FFmpeg for video "
         "conversion.", 252, size=14)

    # What Bloem uses.
    right = [
        (96, a, "PostgreSQL 18", "Everything that must last. Host port on 127.0.0.1 only.", None),
        (96, a, "Redis", "Coordination and caches. Host port on 127.0.0.1 only.", None),
        (78, g, "Meilisearch", "Optional search engine.", "6 4"),
        (78, w, "Media folder", "Read-only inside the container.", None),
        (96, w, "Data folder", "Under /opt/silo unless you choose another place.", None),
    ]
    y = 130
    for h, color, title, body, dash in right:
        s.rect(900, y, 260, h, color["fill"], color["stroke"], rx=14, sw=1.6, dash=dash)
        s.text(918, y + 30, title, size=16, weight="700")
        para(s, 918, y + 54, body, 224, size=13.5, gap=18)
        s.arrow(864, y + h / 2, 896, y + h / 2)
        y += h + 12

    footer(s, "Keep SECRET_KEY secret and back it up apart from the database. It unlocks every password and "
              "key Bloem stores; without it they cannot be recovered.", y=690)
    return s


def organization_domain():
    s = Svg(1200, 690, "Serving an organization on its own domain",
            "Five numbered steps: point DNS at the reverse proxy, issue a certificate, forward the host through a "
            "trusted proxy, register the domain on the organization, and register the sign-in provider's "
            "callbacks if the organization signs in through Authentik. Below, what a domain decides "
            "(name, logo, colours, email links, where to sign in) and what it never decides (who belongs "
            "where and what they may watch), with an example of a refused sign-in.")
    s.header("Serving an organization on its own domain",
             "Five steps, in this order. A domain changes what people see, never what they may watch.")
    o, p, a, st = TIERS["org"], TIERS["platform"], TIERS["account"], TIERS["stop"]

    steps = [
        (o, "DNS", "Point the domain at your reverse proxy."),
        (o, "Certificate", "Issue an HTTPS certificate for the domain at the proxy."),
        (o, "Forward the host", "The proxy passes the domain on to Bloem. Its address is a trusted proxy."),
        (p, "Register it", "A platform administrator adds it under Organizations, or the storefront does."),
        (a, "Provider callbacks", "Only for Authentik sign-in: the domain's two callbacks and its /login."),
    ]
    w, gap = 208, 20
    for i, (color, title, body) in enumerate(steps):
        x = 40 + i * (w + gap)
        card(s, x, 124, w, 156, color, title, body, n=i + 1, size=14, title_size=15.5)
        if i < len(steps) - 1:
            s.arrow(x + w + 2, 202, x + w + gap - 2, 202)

    # What it decides.
    s.rect(40, 312, 540, 184, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(64, 348, "What a domain decides", size=18, weight="700")
    rows = ["The organization's name, logo and colours", "The address in its emails (its primary domain)",
            "Which address the sign-in page sends people to"]
    for i, row in enumerate(rows):
        yes_mark(s, 78, 384 + i * 42, r=11)
        s.text(102, 389 + i * 42, row, size=15)
    s.rect(620, 312, 540, 184, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(644, 348, "What it never decides", size=18, weight="700")
    rows = ["Which organization an account belongs to", "Which libraries a profile may see",
            "Whether a plan may play, download or convert"]
    for i, row in enumerate(rows):
        no_mark(s, 658, 384 + i * 42, r=11)
        s.text(682, 389 + i * 42, row, size=15)

    # Example of a refusal.
    s.rect(40, 528, 1120, 100, st["fill"], st["stroke"], rx=14, sw=1.6)
    s.text(64, 560, "Signing in on another organization's domain", size=17, weight="700")
    para(s, 64, 588, "Sam's account belongs to Riverside Club. Sam opens watch.harbour.example, which belongs to "
         "another organization, and signs in. Bloem refuses before any session exists and says: \"This account "
         "signs in at https://watch.riverside.example.\"", 1072, size=14.5)

    footer(s, "An organization without a domain signs in at the server's public address. This example is made up.",
           y=660, size=13.5, fill=MUTED)
    return s


def updating():
    s = Svg(1200, 520, "Updating Bloem safely",
            "Seven numbered steps: note the running image, dump the database and check the dump, back up .env "
            "and SECRET_KEY, read the release notes, pull and start the new image, wait while migrations run "
            "before Bloem answers, then check health and ready. A warning says that starting the old image again "
            "does not undo migrations.")
    s.header("Updating Bloem safely",
             "Prepare a way back first. Then let the new image finish its database changes before you check it.")
    a, p, w, st = TIERS["account"], TIERS["platform"], TIERS["warn"], TIERS["stop"]

    first = [
        (a, "Note the image", "docker compose images silo. \"latest\" will not tell you later which one it was."),
        (a, "Dump the database", "pg_dump, then pg_restore --list to check the dump can be read."),
        (a, "Back up .env", "Above all SECRET_KEY, kept apart from the dump."),
        (w, "Read the notes", "Look for migrations that need a stop, extra disk or a longer time limit."),
    ]
    w4, gap = 262, 24
    for i, (color, title, body) in enumerate(first):
        x = 40 + i * (w4 + gap)
        card(s, x, 120, w4, 136, color, title, body, n=i + 1, size=14, title_size=16.5)
        if i < len(first) - 1:
            s.arrow(x + w4 + 2, 188, x + w4 + gap - 2, 188)
    s.path("M1100,260 L1100,274 L171,274 L171,290")

    second = [
        (p, "Pull and start", "docker compose pull silo, then up -d --no-deps silo."),
        (p, "Let migrations finish", "Bloem changes the database before it answers. Do not restart it meanwhile."),
        (a, "Check it", "/api/v1/health says it runs; /api/v1/ready says its database and storage answer."),
    ]
    for i, (color, title, body) in enumerate(second):
        x = 40 + i * (w4 + gap)
        card(s, x, 294, w4, 136, color, title, body, n=i + 5, size=14, title_size=16.5)
        if i < len(second) - 1:
            s.arrow(x + w4 + 2, 362, x + w4 + gap - 2, 362)
    box(s, 40 + 3 * (w4 + gap), 294, w4, 136, st, "No undo",
        "Starting the old image again does not reverse migrations. Plan any way back before you update.",
        size=14, title_size=16.5)

    footer(s, "Migrations stop after 20 minutes by default. For a very large library, raise SILO_MIGRATE_TIMEOUT "
              "and the health check's start period together.", y=476)
    return s


PICTURES = [("stack", stack), ("organization-domain", organization_domain), ("updating", updating)]
