"""Pictures for docs/bloem/overlays/s3-storage-setup.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, footer, no_mark, para, yes_mark

SOURCE = {"fill": "#F5F3FF", "stroke": "#8B5CF6", "label": "#6D28D9"}


def two_uses():
    s = Svg(1200, 590, "Two different uses of S3",
            "On the left, Silo's Assets store: artwork and branding, organization logos, and campaign and seasonal "
            "pictures under ambience/. It is on the server's disk or in an S3 bucket, and Bloem writes to it and "
            "serves from it. On the right, storage sources: S3 buckets holding a library's movies, ebooks and "
            "audiobooks, which Bloem only reads, through a storage plugin. They are set up in different places.")
    s.header("Two different uses of S3",
             "They are set up in different places, and can use different buckets.")
    p, a = TIERS["platform"], TIERS["account"]

    def panel(x, color, title, subtitle, items, facts):
        s.rect(x, 128, 540, 360, color["fill"], color["stroke"], rx=18, sw=2)
        s.text(x + 24, 166, title, size=20, weight="700")
        s.text(x + 24, 192, subtitle, size=14.5, fill=MUTED)
        for i, item in enumerate(items):
            y = 218 + i * 52
            s.rect(x + 24, y, 492, 40, "#FFFFFF", color["stroke"], rx=10, sw=1.2)
            s.text(x + 42, y + 26, item, size=15, fill=INK)
        y = 218 + len(items) * 52 + 24
        for ok, fact in facts:
            (yes_mark if ok else no_mark)(s, x + 38, y - 5, r=10)
            s.text(x + 58, y, fact, size=14.5, fill=INK)
            y += 30

    panel(40, p, "The Assets store", "Silo's. This overlay is about it.",
          ["Catalog artwork and branding", "An organization's logo and branding pictures",
           "Campaign and seasonal pictures"],
          [(True, "On the server's disk, or an S3 bucket"), (True, "Bloem writes to it and serves from it"),
           (False, "Not the private avatar store")])
    panel(620, SOURCE, "Storage sources", "Bloem's. Set up with the libraries.",
          ["Movies", "Ebooks", "Audiobooks"],
          [(True, "S3 buckets, or the Bookwarehouse service"), (True, "Read only, through a storage plugin"),
           (False, "Bloem never writes to them")])

    footer(s, "Changing the Assets store is locked once it has its first file: move it with Silo's managed "
              "transition. Storage sources can be added and removed without touching it.", y=548)
    return s


PICTURES = [("two-uses", two_uses)]
