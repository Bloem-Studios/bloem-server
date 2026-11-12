"""Pictures for docs/architecture/bloem-strm.md."""
from kit import SOFT, TIERS, Svg, box, footer, no_mark


def flow():
    s = Svg(1200, 620, "How a .strm file is scanned and played",
            "Top row, scanning: a .strm file in a library folder or a storage source; the scan reads its first "
            "line, checks the address and lets ffprobe read the video through Bloem; Bloem keeps the address in "
            "its own record, never in a path. Bottom row, playing: the app asks Bloem; Bloem passes the bytes on, "
            "rewraps or converts them; every connection and redirect goes through the address check; the remote "
            "server answers range requests. Below the check: addresses on the server's own network are refused "
            "unless a platform administrator allows them.")
    s.header("How a .strm file is scanned and played",
             "The address stays inside Bloem. Every connection to it passes the same check.")
    a, o, p, st = TIERS["account"], TIERS["org"], TIERS["platform"], TIERS["stop"]

    s.text(40, 140, "Scanning", size=16, weight="700", fill=SOFT)
    box(s, 40, 156, 340, 128, a, "A .strm file",
        "In a library folder or a storage source. Its first line is a web address.", size=14.5)
    box(s, 430, 156, 340, 128, o, "The scan",
        "Reads the address, checks it, and lets ffprobe read the video through Bloem.", size=14.5)
    box(s, 820, 156, 340, 128, o, "Bloem's record",
        "The address, kept apart. The catalog shows the file's own name and the video's details.", size=14.5)
    s.arrow(384, 220, 426, 220)
    s.arrow(774, 220, 816, 220)

    s.text(40, 334, "Playing", size=16, weight="700", fill=SOFT)
    box(s, 40, 350, 255, 128, a, "The app", "Asks Bloem, gets Bloem's addresses. Never the remote one.", size=14.5)
    box(s, 335, 350, 255, 128, o, "Bloem", "Passes the bytes on, rewraps them or converts them.", size=14.5)
    box(s, 630, 350, 255, 128, p, "Address check", "At every connection and every redirect.", size=14.5)
    box(s, 925, 350, 235, 128, a, "Remote server", "Answers range requests.", size=14.5)
    s.arrow(299, 414, 331, 414)
    s.arrow(594, 414, 626, 414)
    s.arrow(889, 414, 921, 414)

    s.rect(630, 498, 530, 70, st["fill"], st["stroke"], rx=14, sw=1.8)
    no_mark(s, 660, 533, r=12)
    s.text(684, 527, "The server's own network: refused", size=15.5, weight="700", fill=st["label"])
    s.text(684, 551, "unless a platform administrator allows private addresses", size=14, fill=st["label"])
    s.path("M 757 482 L 757 494")

    footer(s, "Content is never filtered. The one default protects the server's own network.", y=596)
    return s


PICTURES = [("flow", flow)]
