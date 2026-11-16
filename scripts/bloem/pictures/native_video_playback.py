"""Pictures for docs/architecture/bloem-native-video-playback.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

SOURCE = {"fill": "#F5F3FF", "stroke": "#8B5CF6", "label": "#6D28D9"}


def routes():
    s = Svg(1200, 700, "Three ways a stored movie plays",
            "On the left, the storage source and its plugin. In the middle, the API server reads the movie in "
            "checked one-mebibyte blocks and plays it by direct play, by repackaging it as MP4, or as HLS segments. "
            "Below, a transcode node with core source access can make the HLS segments too: it reads the bytes "
            "from the API server under a source grant, and a node that reads its own library folders gets no "
            "stored movie. On the right, the app gets ordinary playback addresses. Bytes "
            "leave only while the latest access check is less than a second old.")
    s.header("Three ways a stored movie plays",
             "Every route reads the same pinned revision and keeps checking the viewer; HLS can run on a node.")
    o, a, pl, p, g = TIERS["org"], TIERS["account"], TIERS["plan"], TIERS["platform"], TIERS["grey"]

    # Storage on the left.
    box(s, 40, 150, 230, 140, SOURCE, "Storage source", "A bucket or book service, read through its approved "
        "plugin.", size=14.5)

    # API server in the middle.
    s.rect(330, 128, 520, 380, p["fill"], p["stroke"], rx=18, sw=2)
    s.text(354, 164, "API server", size=19, weight="700")
    s.rect(354, 182, 472, 64, "#FFFFFF", pl["stroke"], rx=12, sw=1.5)
    s.text(374, 210, "Read ahead in 1 MiB blocks", size=15.5, weight="700")
    s.text(374, 233, "Bytes leave only while the last check is under a second old.", size=13.5, fill=MUTED)
    s.arrow(274, 214, 350, 214)
    routes = [
        ("1", "Direct play", "The bytes as they are, in ranges."),
        ("2", "Repackaging", "ffmpeg rewraps the video as MP4."),
        ("3", "HLS here", "ffmpeg makes short segments."),
    ]
    for i, (n, t, b) in enumerate(routes):
        y = 268 + i * 74
        s.rect(354, y, 472, 62, "#FFFFFF", "#FDBA74", rx=12, sw=1.4)
        s.number(380, y + 31, n, p["stroke"], r=13)
        s.text(404, y + 27, t, size=15.5, weight="700")
        s.text(404, y + 49, b, size=13.5, fill=MUTED)
        s.arrow(830, y + 31, 896, y + 31)

    # A core-access transcode node below.
    box(s, 330, 540, 520, 112, pl, "Transcode node, core source access", "HLS on the GPU: the node reads the "
        "bytes from the API server under a source grant. Local-access nodes get no stored movie.", size=14.5)
    yes_mark(s, 820, 566, r=12)
    s.arrow(590, 512, 590, 536)

    # The app on the right.
    box(s, 900, 254, 260, 222, a, "The app", "Gets Silo's usual playback plan with ordinary addresses on "
        "Bloem. Never a storage address, key or header.", size=14.5)

    s.rect(900, 500, 260, 168, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(920, 530, "Not yet", size=16, weight="700")
    for i, t in enumerate(["Jellyfin HLS on a node", "Series from storage", "Switching files mid-play"]):
        y = 564 + i * 28
        no_mark(s, 932, y - 5, r=10)
        s.text(952, y, t, size=14.5, fill=INK)
    return s


PICTURES = [("routes", routes)]
