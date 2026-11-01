"""Pictures for docs/architecture/bloem-storage-sources.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

SOURCE = {"fill": "#F5F3FF", "stroke": "#8B5CF6", "label": "#6D28D9"}


def where_files_live():
    s = Svg(1200, 730, "Where a library's files can live",
            "A library can read its files from three places at once: folders on the server's own disk, S3 buckets "
            "and the Bookwarehouse book service. S3 and Bookwarehouse are reached through approved storage "
            "plugins. The library holds ebooks, movies or audiobooks.")
    s.header("Where a library's files can live",
             "Silo reads files from the server's disk. Bloem can also read them from cloud storage.")
    p, a, g, w = TIERS["platform"], TIERS["account"], TIERS["grey"], TIERS["warn"]

    s.text(40, 136, "Where the files are", size=17, weight="700")
    srcs = [
        (g, "A folder on the server", "The usual way, as in Silo: a path on the server's disk or a mounted share."),
        (SOURCE, "An S3 bucket", "Backblaze B2, Wasabi, Cloudflare R2, Amazon S3, DigitalOcean Spaces, Hetzner "
         "or another S3 service. One source can cover a bucket, or every bucket its keys can see."),
        (SOURCE, "Bookwarehouse", "A book service. Bloem browses its shelves like folders."),
    ]
    y = 152
    for color, title, body in srcs:
        h = 150 if title == "An S3 bucket" else 112
        box(s, 40, y, 360, h, color, title, body, size=14)
        if color is SOURCE:
            s.rect(424, y + h / 2 - 22, 136, 44, "#FFFFFF", SOURCE["stroke"], rx=22, sw=1.4)
            s.text(492, y + h / 2 + 5, "storage plugin", size=13.5, weight="600", fill=SOURCE["label"], anchor="middle")
            s.arrow(402, y + h / 2, 420, y + h / 2)
            s.arrow(562, y + h / 2, 606, 340)
        else:
            s.path(f"M 402 {y + h / 2} L 492 {y + h / 2} L 606 300")
        y += h + 18

    s.rect(610, 152, 250, 384, p["fill"], p["stroke"], rx=18, sw=2)
    s.text(735, 192, "One library", size=20, weight="700", anchor="middle")
    para(s, 735, 222, "It can mix folders and several sources, and pick only some of a source's folders.",
         210, size=14, anchor="middle")
    for i, (name, what) in enumerate([("Ebooks", "read"), ("Movies", "watch"), ("Audiobooks", "listen")]):
        yy = 318 + i * 68
        s.rect(634, yy, 202, 52, "#FFFFFF", "#FDBA74", rx=12, sw=1.4)
        s.text(652, yy + 32, name, size=16, weight="700")
        s.text(818, yy + 32, what, size=14, fill=MUTED, anchor="end")
    s.arrow(864, 344, 916, 344)

    s.rect(920, 152, 240, 384, a["fill"], a["stroke"], rx=16, sw=1.8)
    s.text(940, 188, "What viewers get", size=17, weight="700")
    yes_mark(s, 952, 226, r=11)
    para(s, 972, 231, "Read, watch and listen, like any other title.", 170, size=14, fill=INK)
    no_mark(s, 952, 330, r=11)
    para(s, 972, 335, "No downloads of these files.", 170, size=14, fill=INK)
    no_mark(s, 952, 400, r=11)
    para(s, 972, 405, "Not through Jellyfin apps.", 170, size=14, fill=INK)

    s.rect(40, 596, 1120, 94, w["fill"], w["stroke"], rx=14, sw=1.5)
    s.text(62, 628, "Experimental", size=16, weight="700", fill=w["label"])
    para(s, 62, 654, "It works, but no storage service is certified yet. A source keeps its settings even when its "
         "plugin is removed; its keys are stored encrypted and never shown again.", 1080, size=14, fill=INK)
    return s


def add_a_source():
    s = Svg(1200, 590, "From a bucket to the screen",
            "Five steps: add a source with its keys, choose its folders, give them to a library, scan, and the "
            "titles appear for viewers to watch, read or listen to.")
    s.header("From a bucket to the screen",
             "A platform administrator, or an organization's own administrator, can set this up in the admin "
             "console.")
    p, a, o = TIERS["plan"], TIERS["account"], TIERS["org"]
    steps = [
        (SOURCE, "Add a source", "Choose the service and region, and enter the access keys. Bloem works out the "
         "address."),
        (SOURCE, "Choose folders", "Browse its buckets and folders. Pick one or several, or the whole source."),
        (o, "Give them to a library", "A new library, or another location for an existing one, beside its "
         "ordinary folders."),
        (o, "Scan", "Bloem lists the files and reads names the way Silo reads folders. It skips samples and "
         "extras, and reads only what it needs."),
        (a, "Watch, read, listen", "The titles appear like any others, with their covers, for everyone the "
         "library is open to."),
    ]
    x0, y0, cw, ch, gap = 40, 128, 204, 240, 25
    for i, (c, t, b) in enumerate(steps):
        x = x0 + i * (cw + gap)
        s.rect(x, y0, cw, ch, c["fill"], c["stroke"], rx=14)
        s.number(x + 26, y0 + 28, i + 1, c["stroke"])
        s.text(x + 16, y0 + 74, t, size=16.5, weight="700")
        para(s, x + 16, y0 + 102, b, cw - 30, size=13.5, gap=18)
        if i < len(steps) - 1:
            s.arrow(x + cw + 3, y0 + ch / 2, x + cw + gap - 3, y0 + ch / 2)
    s.text(40, 424, "Good to know", size=17, weight="700")
    notes = [
        "Keys can be replaced later without touching the libraries. They are never shown again.",
        "An organization's administrator can add sources only for their own organization, and only at public "
        "internet addresses.",
        "If one source fails during a scan, the library's other folders and sources are still scanned.",
    ]
    y = 444
    for n in notes:
        s.add(f'<circle cx="52" cy="{y + 14}" r="4" fill="{SOFT}"/>')
        para(s, 66, y + 19, n, 1080, size=14.5, fill=INK)
        y += 38
    return s


PICTURES = [("where-files-live", where_files_live), ("add-a-source", add_a_source)]
