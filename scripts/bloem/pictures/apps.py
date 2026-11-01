"""Pictures for docs/architecture/bloem-apps.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

DOOR = {"fill": "#FFFFFF", "stroke": "#FA6400", "label": "#C2410C"}


def doors():
    s = Svg(1200, 760, "Which apps connect, and where",
            "One Bloem server with three entrances: the main address for the web app, Bloem's apps and Silo's "
            "apps; a Jellyfin entrance for Jellyfin apps such as Infuse; and an Audiobookshelf entrance. Every "
            "entrance shows a session only its own organization's libraries.")
    s.header("Which apps connect, and where",
             "One server, three entrances. Each kind of app uses the entrance it was built for.")
    p, a, o, g = TIERS["platform"], TIERS["account"], TIERS["org"], TIERS["grey"]

    # The server and its entrances.
    s.rect(450, 124, 300, 540, p["fill"], p["stroke"], rx=18, sw=2)
    s.text(600, 160, "Bloem server", size=20, weight="700", anchor="middle")
    entrances = [
        (180, 220, "Main address", ["Silo's API, plus", "Bloem's own extras"]),
        (424, 100, "Jellyfin entrance", ["port 8096"]),
        (544, 100, "Audiobookshelf", ["port 13378"]),
    ]
    for y, h, title, rows in entrances:
        s.rect(474, y, 252, h, DOOR["fill"], DOOR["stroke"], rx=12, sw=1.6)
        s.text(600, y + 34, title, size=16.5, weight="700", anchor="middle")
        s.lines(600, y + 60, rows, size=14, fill=MUTED, anchor="middle")
    s.text(600, 330, "/api/v1  /api/v2", size=13, fill=SOFT, anchor="middle")
    s.text(600, 350, "/api/bloem/v1", size=13, fill=SOFT, anchor="middle")
    s.text(600, 376, "Port 8090 in Bloem's Docker setup", size=12.5, fill=SOFT, anchor="middle")

    # Left: apps on the main address.
    box(s, 40, 152, 360, 124, a, "The web app and Bloem's apps",
        "They use Silo's API, plus Bloem's own extras built for them: Live TV, profile sign-ins, richer title "
        "pages and server-side search.", size=14)
    s.arrow(404, 214, 470, 250)
    box(s, 40, 300, 360, 108, g, "Silo's own apps",
        "Work as they do with Silo. They see everything Silo has, and none of Bloem's extras.", size=14)
    s.arrow(404, 354, 470, 330)

    # Right: compatibility apps.
    box(s, 800, 400, 360, 124, o, "Jellyfin apps",
        "Infuse, Findroid, Swiftfin and others. No Live TV, and no files from storage sources.", size=14)
    s.arrow(796, 462, 730, 474)
    box(s, 800, 540, 360, 124, o, "Audiobookshelf apps",
        "For listening to audiobooks.", size=14)
    s.arrow(796, 600, 730, 594)

    # Finding the server.
    s.rect(800, 152, 360, 156, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(820, 186, "Finding the server", size=17, weight="700")
    para(s, 820, 214, "Type its address into the app. On a home network, Bloem can also announce itself so "
         "Bloem's apps find it on their own; that is off until an administrator turns it on.", 320, size=14,
         gap=19)

    footer(s, "Whichever entrance an app uses, a session sees only its own organization's libraries, with its "
              "plan's and profile's limits.", y=716, fill=INK)
    return s


def sign_in():
    s = Svg(1200, 660, "How people sign in",
            "Two rows. Bloem's apps, Silo's apps and the web app: email and password, then pick a profile, then "
            "the profile's PIN if it has one. Jellyfin and Audiobookshelf apps: one username made of the account "
            "and the profile, and the password with the PIN added.")
    s.header("How people sign in",
             "Every way in ends in one profile of one account. Settings → Connect Apps shows what to type.")
    a, f, o, g = TIERS["account"], TIERS["profile"], TIERS["org"], TIERS["grey"]

    s.text(40, 140, "Bloem's apps, Silo's apps and the web app", size=17, weight="700")
    steps = [
        (a, "Email and password", "The account's sign-in."),
        (f, "Pick a profile", "Who is watching: Anna, Tom, Kids."),
        (f, "PIN, if the profile has one", "A code that keeps the profile private."),
    ]
    x = 40
    for i, (c, t, b) in enumerate(steps):
        box(s, x, 156, 268, 104, c, t, b, size=14, title_size=16)
        s.arrow(x + 272, 208, x + 296, 208)
        x += 300
    s.rect(940, 166, 220, 84, INK, rx=42)
    s.text(1050, 214, "Watching", size=19, weight="700", fill="#FFFFFF", anchor="middle")
    para(s, 40, 298, "A profile can also have a sign-in of its own, set up by the household's main profile under "
         "Settings \u2192 Profiles. Apps that support it then sign straight into that one profile. The browser "
         "always signs in to the account.", 1120, size=14)

    s.text(40, 378, "Jellyfin and Audiobookshelf apps", size=17, weight="700")
    s.rect(40, 394, 560, 150, o["fill"], o["stroke"], rx=14, sw=1.8)
    s.text(62, 428, "Username", size=13, weight="700", fill=o["label"], spacing="1")
    s.rect(62, 438, 240, 40, "#FFFFFF", "#D1D5DB", rx=8)
    s.text(78, 464, "sam#Kids", size=16, weight="600")
    s.text(320, 428, "Password", size=13, weight="700", fill=o["label"], spacing="1")
    s.rect(320, 438, 258, 40, "#FFFFFF", "#D1D5DB", rx=8)
    s.text(336, 464, "account password#1234", size=16, weight="600")
    para(s, 62, 508, "The account, a #, and the profile's name. A PIN goes after the password, after another #.",
         520, size=13.5, gap=18)
    s.arrow(604, 469, 656, 469)
    s.rect(660, 412, 260, 114, f["fill"], f["stroke"], rx=14, sw=1.8)
    s.text(790, 456, "Straight into", size=16, weight="700", anchor="middle")
    s.text(790, 480, "Sam's Kids profile", size=16, weight="700", anchor="middle")
    s.text(790, 504, "with its limits", size=14, fill=MUTED, anchor="middle")
    s.arrow(924, 469, 976, 469)
    s.rect(980, 427, 180, 84, INK, rx=42)
    s.text(1070, 475, "Watching", size=19, weight="700", fill="#FFFFFF", anchor="middle")

    footer(s, "An administrator decides whether Jellyfin and Audiobookshelf apps may connect at all. Both are "
              "allowed by default.", y=610, fill=INK)
    return s


PICTURES = [("doors", doors), ("sign-in", sign_in)]
