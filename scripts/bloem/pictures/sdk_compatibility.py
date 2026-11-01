"""Pictures for docs/architecture/bloem-sdk-compatibility.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, footer, para

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}


def _panel(s, x, y, w, h, color, kicker, title, rows):
    s.rect(x, y, w, h, color["fill"], color["stroke"], rx=16, sw=1.8)
    s.text(x + 22, y + 32, kicker, size=12.5, weight="700", fill=color["label"], spacing="1")
    s.text(x + 22, y + 60, title, size=18, weight="700")
    ty = y + 92
    for head, body in rows:
        s.text(x + 22, ty, head, size=14.5, weight="700")
        ty = para(s, x + 22, ty + 22, body, w - 44, size=13.5, gap=18) + 14
    return ty


def releases():
    s = Svg(1200, 700, "Three things that ship on their own",
            "Three panels. The Bloem server pins Silo's public plugin SDK and owns the storage protocol. The Bloem "
            "plugin SDK, for plugin authors, carries a byte-identical copy of that protocol. The client contracts "
            "are generated from the server's wire types into Kotlin and Swift with a digest, and each app adopts "
            "them on purpose. A release of one proves nothing about the others.")
    s.header("Three things that ship on their own",
             "The server, the plugin SDK and the app contracts each have their own version. One shipping proves "
             "nothing about the others.")
    a, p = TIERS["account"], TIERS["plan"]
    _panel(s, 40, 130, 360, 350, BLOEM, "BLOEM SERVER", "This repository", [
        ("Silo's plugin SDK", "The public github.com/Silo-Server/silo-plugin-sdk, pinned at v0.31.0 in go.mod."),
        ("Storage protocol", "bloem.plugin.v1.StorageProvider: proto/bloem/plugin/v1, with generated bindings "
                             "under internal/storageproto."),
        ("In-process only", "Campaigns and seasonal packs are worked out inside the server, not by plugins."),
    ])
    _panel(s, 420, 130, 360, 350, p, "PLUGIN SDK", "For plugin authors", [
        ("Its own releases", "Bloem-Studios/bloem-plugin-sdk, v0.27.0. The server does not import it."),
        ("Same storage protocol", "storage_provider.proto is byte for byte the server's copy, checked by a test."),
        ("Real plugin tests", "The server's tests download that release and run its example storage plugin."),
    ])
    _panel(s, 800, 130, 360, 350, a, "APP CONTRACTS", "For Bloem's apps", [
        ("Generated", "registry.json picks the server's wire types; a generator writes Kotlin and Swift, and a "
                      "digest."),
        ("Pinned", "contracts/client/v1/digest.txt. The files carry the server revision they came from."),
        ("Adopted on purpose", "Android and Apple check the revision and digest before they take a new copy."),
    ])
    s.path("M240,484 L240,530 L600,530 L600,488")
    s.text(420, 520, "The same storage protocol, byte for byte", size=13.5, weight="600", fill=MUTED, anchor="middle")
    s.path("M150,484 L150,590 L980,590 L980,488")
    s.text(565, 580, "Generated from the server's wire types", size=13.5, weight="600", fill=MUTED, anchor="middle")
    footer(s, "A healthy server or a freshly copied file proves none of the others has shipped. Check each one on "
              "its own.", y=654, fill=INK)
    return s


PICTURES = [("releases", releases)]
