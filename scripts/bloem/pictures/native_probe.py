"""Pictures for docs/architecture/bloem-native-probe.md (also used by the scan reader page)."""
from kit import INK, MUTED, SOFT, TIERS, Svg, para

SOURCE = {"fill": "#F5F3FF", "stroke": "#8B5CF6", "label": "#6D28D9"}


def chain():
    s = Svg(1200, 560, "How a scan looks inside a stored file",
            "Five stages from left to right: the storage plugin supplies bytes; the scan reader opens only a file "
            "the scan has just listed and checks access before and after each read; a private bridge on the same "
            "machine serves that one file within a byte budget; ffprobe reads only the chosen format; Bloem checks "
            "its answer before the scan adds the title to the catalog.")
    s.header("How a scan looks inside a stored file",
             "ffprobe never sees a path or a storage address, only a private address for one file.")
    o, a, pl, g = TIERS["org"], TIERS["account"], TIERS["plan"], TIERS["grey"]
    stages = [
        (SOURCE, "Storage plugin", "Supplies the exact bytes asked for, pinned to the listed revision."),
        (o, "Scan reader", "Opens only a file the scan has just listed. Checks access before and after each "
         "read."),
        (pl, "Private bridge", "An address on this machine with a secret in it. One file, within a byte budget."),
        (g, "ffprobe", "Reads only the chosen format, from the bridge. No proxies, no report files."),
        (a, "Catalog", "Bloem checks the answer: one document, with a format and at least one stream."),
    ]
    x0, y0, cw, ch, gap = 40, 128, 204, 220, 25
    for i, (c, t, b) in enumerate(stages):
        x = x0 + i * (cw + gap)
        s.rect(x, y0, cw, ch, c["fill"], c["stroke"], rx=14)
        s.number(x + 26, y0 + 28, i + 1, c["stroke"])
        s.text(x + 16, y0 + 74, t, size=17, weight="700")
        para(s, x + 16, y0 + 102, b, cw - 30, size=14, gap=19)
        if i < len(stages) - 1:
            s.arrow(x + cw + 3, y0 + ch / 2, x + cw + gap - 3, y0 + ch / 2)

    s.rect(40, 384, 1120, 128, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(62, 418, "Limits for one probe", size=16, weight="700")
    limits = [("128 MiB", "read from the storage"), ("60 s", "to finish"), ("2", "reads at once"),
              ("4 MiB", "for ffprobe's answer")]
    for i, (big, small) in enumerate(limits):
        x = 62 + i * 272
        s.text(x, 462, big, size=24, weight="700", fill=o["label"])
        s.text(x, 488, small, size=14, fill=MUTED)
    return s


PICTURES = [("chain", chain)]
