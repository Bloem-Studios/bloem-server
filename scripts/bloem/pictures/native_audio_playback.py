"""Pictures for docs/architecture/bloem-native-audio-playback.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, para, no_mark, yes_mark

SOURCE = {"fill": "#F5F3FF", "stroke": "#8B5CF6", "label": "#6D28D9"}


def book():
    s = Svg(1200, 660, "How a folder of tracks becomes one audiobook",
            "Four steps from left to right: Bloem lists the whole folder of tracks; learns each track from the "
            "listing or by looking inside it once, keeping the answer; keeps the tracks waiting until the whole "
            "folder is listed; then publishes one audiobook with its tracks as parts in order. Below: a track that "
            "cannot be read holds back only its own book, and the book plays by direct play, one track at a time.")
    s.header("How a folder of tracks becomes one audiobook",
             "Each folder that holds tracks is one book. Nothing is published until the whole folder is in.")
    o, a, pl, w, st = TIERS["org"], TIERS["account"], TIERS["plan"], TIERS["warn"], TIERS["stop"]

    # 1. The folder.
    s.rect(40, 128, 250, 260, SOURCE["fill"], SOURCE["stroke"], rx=14)
    s.number(66, 156, 1, SOURCE["stroke"])
    s.text(56, 202, "List the folder", size=17, weight="700")
    s.text(56, 226, "Always in full.", size=14, fill=MUTED)
    for i in range(3):
        y = 250 + i * 40
        s.rect(56, y, 218, 30, "#FFFFFF", "#C4B5FD", rx=8, sw=1.2)
        s.text(70, y + 20, f"Track {i + 1}", size=14, fill=INK)
    s.arrow(294, 258, 318, 258)

    # 2. Learn each track.
    s.rect(322, 128, 270, 260, o["fill"], o["stroke"], rx=14)
    s.number(348, 156, 2, o["stroke"])
    s.text(338, 202, "Learn each track", size=17, weight="700")
    para(s, 338, 230, "From the listing, when it gives the format and length. Otherwise Bloem looks inside "
         "once and keeps the answer for next time.", 236, size=14, gap=19)
    s.arrow(596, 258, 620, 258)

    # 3. Wait.
    s.rect(624, 128, 250, 260, pl["fill"], pl["stroke"], rx=14)
    s.number(650, 156, 3, pl["stroke"])
    s.text(640, 202, "Wait", size=17, weight="700")
    para(s, 640, 230, "The tracks wait until every page of the folder is listed. A scan that stops keeps "
         "them.", 216, size=14, gap=19)
    s.arrow(878, 258, 902, 258)

    # 4. One book.
    s.rect(906, 128, 254, 260, a["fill"], a["stroke"], rx=14)
    s.number(932, 156, 4, a["stroke"])
    s.text(922, 202, "One audiobook", size=17, weight="700")
    s.text(922, 226, "Parts in disc and track order.", size=14, fill=MUTED)
    for i in range(3):
        y = 250 + i * 40
        s.rect(922, y, 222, 30, "#FFFFFF", "#6EE7B7", rx=8, sw=1.2)
        s.text(936, y + 20, f"Part {i + 1}", size=14, fill=INK)

    # Below: failures and playback.
    s.rect(40, 420, 552, 196, w["fill"], w["stroke"], rx=14, sw=1.5)
    s.text(62, 454, "When a track cannot be read", size=16, weight="700", fill=w["label"])
    para(s, 62, 482, "Only its own book is held back. The scan goes on, and a later scan lists that book again.",
         510, size=14.5, fill=INK, gap=20)
    para(s, 62, 548, "If 32 books in a row have a track Bloem cannot read, the storage looks gone: the scan stops "
         "and tries again later.", 510, size=14.5, fill=INK, gap=20)

    s.rect(624, 420, 536, 196, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(646, 454, "How it plays", size=16, weight="700")
    rows = [(True, "Direct play, one track at a time"), (True, "The app picks the track and start position"),
            (False, "No converting or repackaging"), (False, "Not in Jellyfin or Audiobookshelf apps")]
    for i, (ok, t) in enumerate(rows):
        y = 492 + i * 32
        (yes_mark if ok else no_mark)(s, 658, y - 5, r=10)
        s.text(678, y, t, size=14.5, fill=INK)
    return s


PICTURES = [("book", book)]
