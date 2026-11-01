"""Pictures for docs/architecture/bloem-campaign-asset-storage.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark


def where_artwork_goes():
    s = Svg(1200, 716, "Where campaign and seasonal artwork goes",
            "An administrator uploads a picture. Bloem checks that it is a PNG, JPEG, WebP or GIF of at most "
            "8 MiB and names it after its content. It is stored in the first store available: the catalog Assets "
            "store on local disk or S3, else the public S3 bucket. With neither, uploads are refused but a web "
            "address can still be typed in. Anyone can then fetch the picture from the public artwork route.")
    s.header("Where campaign and seasonal artwork goes",
             "One upload path for campaign cards, seasonal banners and seasonal sprites.")
    p, a, w, st, g = TIERS["platform"], TIERS["account"], TIERS["warn"], TIERS["stop"], TIERS["grey"]

    box(s, 40, 132, 300, 116, g, "1. Upload", "A platform administrator or the storefront uploads a picture "
        "for a campaign card, a banner or a sprite.", size=14)
    s.arrow(344, 190, 396, 190)
    box(s, 400, 132, 340, 116, p, "2. Check and name", "Bloem reads the bytes, not the label: PNG, JPEG, WebP "
        "or GIF, at most 8 MiB. The name comes from its content.", size=14)
    s.arrow(744, 190, 796, 190)
    box(s, 800, 132, 360, 116, a, "3. Store", "In the first store the server has, under the folder "
        "ambience/ (below).", size=14)

    # Store choice.
    s.text(40, 296, "Which store", size=18, weight="700")
    rows = [
        (a, True, "The catalog Assets store", "Silo's store for catalog artwork and branding: local disk or "
         "S3 (there, the public bucket). Used whenever it is set up."),
        (w, True, "The public S3 bucket", "Only when no Assets store is set up."),
        (st, False, "Neither", "Uploads are refused (503) and the list says storage is unavailable. A web "
         "address starting https:// can still be typed in instead."),
    ]
    y = 314
    for color, ok, title, body in rows:
        s.rect(40, y, 700, 92, color["fill"], color["stroke"], rx=12, sw=1.5)
        (yes_mark if ok else no_mark)(s, 74, y + 46, r=14)
        s.text(104, y + 34, title, size=16, weight="700")
        para(s, 104, y + 58, body, 616, size=13.5, gap=18)
        y += 104

    # Serving.
    s.rect(780, 314, 380, 300, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(800, 348, "Showing it", size=18, weight="700")
    para(s, 800, 378, "Apps fetch the picture from the public artwork route, with no sign-in, because "
         "seasonal artwork can appear on the sign-in page.", 340, size=14, fill=INK, gap=20)
    s.text(800, 466, "/api/bloem/v1/ambience/assets/{ref}", size=13, weight="600", fill=p["label"])
    s.text(800, 488, "/api/v1/ambience/assets/{ref} (older links)", size=13, fill=SOFT)
    para(s, 800, 522, "The name never changes for the same bytes, so browsers keep a copy for a year.", 340,
         size=14, gap=20)

    footer(s, "A campaign or pack can be saved without an upload: saving one never proves its picture was "
              "uploaded.", y=680, fill=INK)
    return s


PICTURES = [("where-artwork-goes", where_artwork_goes)]
