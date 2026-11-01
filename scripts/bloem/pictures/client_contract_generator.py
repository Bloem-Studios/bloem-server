"""Pictures for docs/architecture/bloem-client-contract-generator.md."""
from kit import TIERS, Svg, box

SERVER = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}


def flow():
    s = Svg(1200, 700, "How the client contract generator works",
            "The server's own Go types, chosen by a registry, go through the generator, which writes Kotlin "
            "files for Android, Swift files for Apple and a fingerprint of the whole contract. CI checks that "
            "the committed files match the code. Each app copies the files from the server version it pins. "
            "Four rules keep older apps working with newer servers.")
    s.header("How the client contract generator works",
             "The server writes the message shapes its apps decode, so nobody copies them by hand.")
    g, a, p, w = TIERS["grey"], TIERS["account"], TIERS["plan"], TIERS["warn"]

    s.text(40, 140, "In the server repository", size=17, weight="700")
    box(s, 40, 156, 250, 150, SERVER, "Go wire types",
        "The structs the server already sends and accepts, in packages such as playback.", size=14)
    s.arrow(294, 231, 326, 231)
    box(s, 330, 156, 250, 150, g, "The registry",
        "registry.json names the root types; everything they contain follows.", size=14)
    s.arrow(584, 231, 616, 231)
    box(s, 620, 156, 250, 150, p, "The generator",
        "make client-dtos runs cmd/clientdtogen for each language.", size=14)
    s.arrow(874, 200, 906, 180)
    s.arrow(874, 262, 906, 282)
    box(s, 910, 140, 250, 82, a, "Kotlin and Swift files", None, size=14, title_size=16)
    box(s, 910, 240, 250, 82, w, "A fingerprint (digest)", None, size=14, title_size=16)

    s.path("M1035,326 L1035,372")
    box(s, 790, 376, 370, 120, g, "CI checks every change",
        "The committed files must match the code; a changed shape must also move the digest.", size=14)

    s.text(40, 400, "In the apps", size=17, weight="700")
    box(s, 40, 416, 330, 120, a, "Android copies the Kotlin",
        "From the one server version it pins, and decodes with the generated classes.", size=14)
    box(s, 400, 416, 330, 120, a, "Apple copies the Swift",
        "The same shapes, the same vocabulary names, the same pin.", size=14)
    s.path("M790,436 L760,436 L760,476 L734,476")

    rules = [
        "Every response field has a default, so an older app reads a newer server.",
        "Unknown values in a vocabulary survive decoding.",
        "A feature stays dormant until the server advertises it.",
        "Generated files hold shapes only; app logic stays hand-written.",
    ]
    y = 620
    for i, r in enumerate(rules):
        s.number(56 + (i % 2) * 580, y + (i // 2) * 40 - 5, i + 1, SERVER["stroke"], r=12)
        s.text(78 + (i % 2) * 580, y + (i // 2) * 40, r, size=14)
    s.text(40, 588, "Rules that keep apps safe", size=17, weight="700")
    return s


PICTURES = [("flow", flow)]
