"""Pictures for internal/audiobooks/README.md."""
from kit import MUTED, SOFT, TIERS, Svg, box, footer, para

DOOR = {"fill": "#FFFFFF", "stroke": "#FA6400", "label": "#C2410C"}


def listener():
    s = Svg(1200, 560, "How Audiobookshelf apps reach Bloem",
            "An Audiobookshelf app connects to its own port, 13378, which listens only while the administrator "
            "allows Audiobookshelf apps. It signs in with the account and profile in the username and the PIN "
            "after the password. Silo's Audiobookshelf-compatible server answers, and Bloem's organization filter "
            "limits it to the libraries of the account's own organization. Bloem's own apps use the main address "
            "instead.")
    s.header("How Audiobookshelf apps reach Bloem",
             "Silo's Audiobookshelf-compatible server, on its own port, limited to one organization's libraries.")
    o, p, f, a = TIERS["org"], TIERS["platform"], TIERS["profile"], TIERS["account"]

    box(s, 40, 132, 260, 150, o, "Audiobookshelf app",
        "Any app made for Audiobookshelf, for listening to audiobooks and podcasts.", size=14)
    s.arrow(304, 206, 352, 206)

    s.rect(356, 124, 300, 166, DOOR["fill"], DOOR["stroke"], rx=14, sw=1.8)
    s.text(506, 160, "Its own port: 13378", size=17, weight="700", anchor="middle")
    para(s, 506, 188, "Listens only while “Allow Audiobookshelf apps to connect” is on. "
         "It is on unless an administrator turns it off.", 256, size=14, anchor="middle")
    s.arrow(660, 206, 708, 206)

    s.rect(712, 124, 448, 166, f["fill"], f["stroke"], rx=14, sw=1.8)
    s.text(736, 160, "Sign-in", size=17, weight="700")
    s.text(736, 192, "Username", size=12.5, weight="700", fill=f["label"], spacing="1")
    s.rect(736, 200, 190, 36, "#FFFFFF", "#D1D5DB", rx=8)
    s.text(750, 224, "sam#Kids", size=15, weight="600")
    s.text(944, 192, "Password", size=12.5, weight="700", fill=f["label"], spacing="1")
    s.rect(944, 200, 192, 36, "#FFFFFF", "#D1D5DB", rx=8)
    s.text(958, 224, "password#1234", size=15, weight="600")
    s.text(736, 266, "Account # profile, and the PIN after the password.", size=13.5, fill=MUTED)

    s.path("M936,294 L936,330 L506,330 L506,352")

    s.rect(356, 356, 300, 132, p["fill"], p["stroke"], rx=14, sw=1.8)
    s.text(506, 392, "Silo's server", size=17, weight="700", anchor="middle")
    para(s, 506, 418, "Silo's code in internal/audiobooks, with one Bloem change to how retries are counted.",
         256, size=14, anchor="middle")
    s.arrow(660, 422, 708, 422)
    box(s, 712, 356, 448, 132, a, "Bloem's organization filter",
        "The app sees only the libraries of the account's own organization, with its plan's and profile's "
        "limits.", size=14)

    footer(s, "Bloem's own apps and the web app play audiobooks through the main address instead.", y=528,
           size=14, fill=SOFT)
    return s


PICTURES = [("listener", listener)]
