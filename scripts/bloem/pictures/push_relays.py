"""Pictures for docs/architecture/bloem-push-relays.md."""
from kit import SOFT, TIERS, Svg, box, footer, no_mark


def routes():
    s = Svg(1200, 640, "Each app's push goes through its own relay",
            "Top row: a Silo app on a phone registers through Silo's push routes; Bloem's server sends its "
            "notifications with Silo's sender and Silo's relay credential to Silo's relay, which delivers them "
            "with Silo's Apple and Firebase credentials. Bottom row: the Bloem Admin app registers through "
            "Bloem's push routes; the server sends with Bloem's sender and Bloem's relay credential to Bloem's "
            "relay, which delivers with Bloem's credentials. Between the rows: neither sender can reach the "
            "other relay, and neither relay sees the other's tokens or credential.")
    s.header("Each app's push goes through its own relay",
             "The app that registered a phone decides the relay. Nothing crosses between the two rows.")
    silo, bloem, server, stop = TIERS["profile"], TIERS["platform"], TIERS["org"], TIERS["stop"]

    s.text(40, 140, "Silo's apps", size=16, weight="700", fill=SOFT)
    box(s, 40, 156, 250, 128, silo, "Silo app", "Registers with Silo's topic and Silo's Firebase project.", size=14.5)
    box(s, 330, 156, 260, 128, server, "Silo's sender", "Silo's push routes, Silo's settings and credential.", size=14.5)
    box(s, 630, 156, 260, 128, silo, "Silo's relay", "Silo's Apple and Firebase credentials.", size=14.5)
    box(s, 930, 156, 230, 128, silo, "The phone", "Fetches the text from the server.", size=14.5)
    s.arrow(294, 220, 326, 220)
    s.arrow(594, 220, 626, 220)
    s.arrow(894, 220, 926, 220)

    s.rect(330, 306, 560, 62, stop["fill"], stop["stroke"], rx=14, sw=1.8)
    no_mark(s, 360, 337, r=12)
    s.text(384, 331, "No token, credential or delivery crosses", size=15.5, weight="700", fill=stop["label"])
    s.text(384, 354, "Each sender reaches only its own relay", size=14, fill=stop["label"])

    s.text(40, 410, "Bloem's apps", size=16, weight="700", fill=SOFT)
    box(s, 40, 426, 250, 128, bloem, "Bloem Admin app", "Registers with Bloem's topic and Bloem's Firebase project.", size=14.5)
    box(s, 330, 426, 260, 128, server, "Bloem's sender", "Bloem's push routes, Bloem's settings and credential.", size=14.5)
    box(s, 630, 426, 260, 128, bloem, "Bloem's relay", "Bloem's Apple and Firebase credentials.", size=14.5)
    box(s, 930, 426, 230, 128, bloem, "The phone", "Fetches the text from the server.", size=14.5)
    s.arrow(294, 490, 326, 490)
    s.arrow(594, 490, 626, 490)
    s.arrow(894, 490, 926, 490)

    footer(s, "Neither relay ever sees a notification's text: only ids, and a token it can deliver to.", y=604)
    return s


PICTURES = [("routes", routes)]
