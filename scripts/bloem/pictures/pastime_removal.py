"""Pictures for docs/architecture/bloem-pastime-removal.md."""
from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, yes_mark

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}


def kept_and_dropped():
    s = Svg(1200, 690, "What the Pastime removal dropped and kept",
            "Left, what the removal dropped: the triggers Bloem had put on Silo's tables, their functions, and "
            "Bloem's managed-tracking, companion and list tables. Right, what it kept: watch-sync connections and "
            "everything under them, collections a list binding published, now manual, and every profile's "
            "favorites, watchlist, ratings, progress and history. Below, the way back: an ordinary plugin on Silo's "
            "watch sync.")
    s.header("What the Pastime removal dropped and kept",
             "Bloem's private machinery went. Everything that belongs to people and to Silo stayed.")
    st, a = TIERS["stop"], TIERS["account"]

    s.rect(40, 124, 540, 400, st["fill"], st["stroke"], rx=16, sw=1.8)
    no_mark(s, 70, 160)
    s.text(92, 166, "Dropped", size=20, weight="700")
    dropped = [
        ("17 triggers", "15 on tables Silo writes, 2 on organizations and memberships."),
        ("14 functions", "The code those triggers ran."),
        ("Bloem's own tables", "Managed tracking, companion and list tables, with their sequences."),
        ("The broker", "The private service a Pastime plugin called on the host."),
    ]
    y = 196
    for t, b in dropped:
        s.text(64, y + 20, t, size=16, weight="700")
        para(s, 64, y + 44, b, 490, size=14.5, gap=19)
        y += 78

    s.rect(620, 124, 540, 400, a["fill"], a["stroke"], rx=16, sw=1.8)
    yes_mark(s, 650, 160)
    s.text(672, 166, "Kept", size=20, weight="700")
    kept = [
        ("Watch-sync connections", "Every connection, and every row under it."),
        ("Published collections", "They keep their items and become ordinary manual collections."),
        ("What people did", "Favorites, watchlists, ratings, progress and history."),
        ("The old migrations", "Unchanged and still runnable, so history stays readable."),
    ]
    y = 196
    for t, b in kept:
        s.text(644, y + 20, t, size=16, weight="700")
        para(s, 644, y + 44, b, 490, size=14.5, gap=19)
        y += 78

    s.rect(40, 548, 1120, 112, "#FFFFFF", "#E5E7EB", rx=14)
    s.rect(40, 548, 8, 112, SILO["stroke"], rx=4)
    s.text(66, 584, "The way back", size=17, weight="700")
    para(s, 66, 612, "Pastime may return only as an ordinary plugin on Silo's public watch sync, with the same "
         "plugin and capability IDs so its connections carry over. Until then its installation stays disabled, "
         "not uninstalled.", 1070, size=14.5, fill=INK, gap=20)
    return s


PICTURES = [("kept-and-dropped", kept_and_dropped)]
