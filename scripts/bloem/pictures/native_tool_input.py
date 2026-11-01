"""Pictures for docs/architecture/bloem-native-tool-input.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, no_mark, para

SOURCE = {"fill": "#F5F3FF", "stroke": "#8B5CF6", "label": "#6D28D9"}


def bridge():
    s = Svg(1200, 640, "The tool input bridge",
            "A trusted tool such as ffprobe or ffmpeg reads from a private address on the same machine. The bridge "
            "waits for a free slot, checks access, counts each piece against a byte budget, reads it pinned to the "
            "file's revision and checks again before handing it over. It reads from one file Bloem has already "
            "opened and checked. Other machines, the public API, logs and responses never see the address.")
    s.header("The tool input bridge",
             "A private address for one stored file, served to one trusted tool on the same machine.")
    o, a, pl, g, st = TIERS["org"], TIERS["account"], TIERS["plan"], TIERS["grey"], TIERS["stop"]

    box(s, 40, 128, 250, 150, g, "The tool", "ffprobe or ffmpeg, started by Bloem. It asks for the whole file "
        "or one range.", size=14.5)
    s.arrow(294, 203, 350, 203)
    s.text(322, 190, "", size=12)

    s.rect(354, 128, 470, 330, pl["fill"], pl["stroke"], rx=16, sw=1.8)
    s.text(378, 162, "The bridge", size=18, weight="700")
    s.text(378, 188, "http://127.0.0.1:<port>/<secret>", size=14, fill=pl["label"])
    steps = ["Wait for a free slot", "Check the file may still be read", "Count the piece against the budget",
             "Read the piece, pinned to its revision", "Check again, then hand it over"]
    for i, t in enumerate(steps):
        y = 228 + i * 44
        s.number(392, y, i + 1, pl["stroke"], r=12)
        s.text(416, y + 5, t, size=15, fill=INK)

    s.arrow(828, 203, 884, 203)
    box(s, 888, 128, 272, 150, o, "The opened file", "Opened and checked by Bloem before the bridge started.",
        size=14.5)
    s.arrow(1024, 282, 1024, 318)
    box(s, 888, 322, 272, 136, SOURCE, "Storage plugin", "Supplies the bytes from the bucket or book service.",
        size=14.5)

    s.text(40, 508, "Never", size=17, weight="700")
    nevers = [
        ("Other machines", "It listens on this machine only."),
        ("The public API", "The bridge is not part of it."),
        ("Logs and replies", "The address is never written down."),
    ]
    for i, (t, b) in enumerate(nevers):
        x = 40 + i * 380
        no_mark(s, x + 14, 552, r=12)
        s.text(x + 38, 550, t, size=15, weight="700")
        s.text(x + 38, 574, b, size=14, fill=MUTED)
    return s


PICTURES = [("bridge", bridge)]
