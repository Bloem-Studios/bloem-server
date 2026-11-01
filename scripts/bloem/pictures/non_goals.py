"""Pictures for docs/bloem/overlays/non-goals.md."""
from kit import INK, MUTED, TIERS, Svg, footer, no_mark, para, yes_mark


def boundary():
    s = Svg(1200, 500, "What Bloem plays, and what it never will",
            "Left, in scope: Live TV from sources an administrator set up (a tuner on the network, Dispatcharr, an "
            "Xtream provider's live channels), with a programme guide and recordings, and media files that Bloem "
            "only reads. Right, permanently out of scope: .strm files and other shortcuts to web addresses, "
            "fetching or passing on any other remote stream, an Xtream provider's films and series or acting as an "
            "Xtream server, and deleting, moving or renaming media files.")
    s.header("What Bloem plays, and what it never will",
             "Bloem adds Live TV to Silo. Everything else on Silo's list of non-goals stays closed.")
    a, st = TIERS["account"], TIERS["stop"]

    def column(x, color, title, rows, mark):
        s.rect(x, 120, 540, 314, color["fill"], color["stroke"], rx=16, sw=1.8)
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
        ("Your media files, read only",
         "Bloem lists and plays them. The next scan shows changes made elsewhere."),
    ], yes_mark)
    column(620, st, "Never, however well built", [
        (".strm files and other shortcuts to a web address",
         "A file whose content is a remote media address."),
        ("Fetching or passing on any other remote stream",
         "No pasted addresses, no Xtream films or series, no Xtream server for other apps."),
        ("Deleting, moving or renaming media files",
         "Not from any app, admin page, plugin or automatic clean-up."),
    ], no_mark)

    footer(s, "The line keeps Bloem a media server for sources someone reviewed, not a fetcher for arbitrary "
              "streams or a file manager.", y=470)
    return s


PICTURES = [("boundary", boundary)]
