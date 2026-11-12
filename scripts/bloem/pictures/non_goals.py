"""Pictures for docs/bloem/overlays/non-goals.md."""
from kit import INK, MUTED, TIERS, Svg, footer, no_mark, para, yes_mark


def boundary():
    s = Svg(1200, 580, "What Bloem plays, and what it never will",
            "Left, in scope: Live TV from sources an administrator set up (a tuner on the network, Dispatcharr, an "
            "Xtream provider's live channels), with a programme guide and recordings; .strm files that point at any "
            "web address, played through Bloem, with addresses on the server's private network refused unless a "
            "platform administrator allows them; and media files that Bloem only reads. Right, permanently out of "
            "scope: deleting, moving or renaming media files, and an Xtream provider's films and series or acting "
            "as an Xtream server.")
    s.header("What Bloem plays, and what it never will",
             "Bloem adds Live TV and .strm files to Silo. Silo's rule about media files stays closed.")
    a, st = TIERS["account"], TIERS["stop"]

    def column(x, color, title, rows, mark, height):
        s.rect(x, 120, 540, height, color["fill"], color["stroke"], rx=16, sw=1.8)
        s.text(x + 28, 160, title, size=19, weight="700", fill=color["label"])
        y = 204
        for head, body in rows:
            mark(s, x + 42, y - 5, r=12)
            s.text(x + 66, y, head, size=16, weight="700", fill=INK)
            y = para(s, x + 66, y + 24, body, 444, size=14, fill=MUTED, gap=19) + 18

    column(40, a, "In scope", [
        ("Live TV from sources an administrator set up",
         "A tuner on the network, Dispatcharr, or an Xtream provider's live channels only."),
        ("A programme guide and recordings",
         "Guides from the sources above. Recordings are new files in the recordings folder."),
        (".strm files that point at any web address",
         "Played through Bloem. Private network addresses are refused unless a platform "
         "administrator allows them."),
        ("Your media files, read only",
         "Bloem lists and plays them. The next scan shows changes made elsewhere."),
    ], yes_mark, 404)
    column(620, st, "Never, however well built", [
        ("Deleting, moving or renaming media files",
         "Not from any app, admin page, plugin or automatic clean-up."),
        ("An Xtream provider's films and series",
         "Xtream stays live-only, and Bloem is no Xtream server for other apps."),
    ], no_mark, 404)

    footer(s, "Bloem plays what its libraries point at, and never filters it. It keeps the server's own network "
              "out of reach by default, and it never manages files.", y=556)
    return s


PICTURES = [("boundary", boundary)]
