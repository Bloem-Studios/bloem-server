"""Pictures for docs/architecture/bloem-presentation-evaluators.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para

PROMO = {"fill": "#FDF4FF", "stroke": "#C026D3", "label": "#86198F"}


def host_and_evaluator():
    s = Svg(1200, 690, "Who decides what",
            "Four steps inside one Bloem server. The host loads only the campaigns or seasonal packs this viewer "
            "is allowed to see and fixes one moment in time. The evaluator, a pure function, picks what to show "
            "from those facts alone. The host checks every answer against what it loaded and rebuilds the "
            "result from its own copy. If the evaluator fails, the page shows nothing extra and carries on. "
            "Below, before and after 26 October 2026: two worker processes, then in-process.")
    s.header("Who decides what", "The server decides who may see something; the evaluator only picks what to show.")
    p, a, g, st = TIERS["platform"], TIERS["account"], TIERS["grey"], TIERS["stop"]

    s.rect(40, 124, 1120, 290, p["fill"], p["stroke"], rx=18, sw=2)
    s.text(64, 156, "ONE BLOEM SERVER", size=13, weight="700", fill=p["label"], spacing="1")
    steps = [
        (a, "1. Load what is allowed", "Only this organization's and this viewer's candidates. One moment "
         "in time for the whole request."),
        (PROMO, "2. Evaluator picks", "A pure function: dates, yearly windows, order. It gets plain facts, "
         "never the database, keys or settings."),
        (a, "3. Check the answer", "Every returned ID must be one the host loaded, in its order. Content is "
         "rebuilt from the host's copy."),
        (a, "4. Show it", "Home rows, title pages, the player and seasonal effects."),
    ]
    x, cw, gap = 64, 252, 22
    for i, (color, title, body) in enumerate(steps):
        cx = x + i * (cw + gap)
        box(s, cx, 176, cw, 140, color, title, body, size=14, title_size=16)
        if i < len(steps) - 1:
            s.arrow(cx + cw + 2, 246, cx + cw + gap - 2, 246)
    no_mark(s, 78, 364, r=12)
    para(s, 100, 358, "If the evaluator fails, campaigns or seasonal effects are left out. Browsing, sign-in and "
         "playback carry on, and the host never logs what the evaluator was given.", 1000, size=14, fill=INK,
         gap=20)

    # Before and after.
    s.text(40, 456, "Where the evaluator runs", size=18, weight="700")
    box(s, 40, 476, 540, 148, g, "5 October to 26 October 2026",
        "Two bundled worker processes behind a JSON-over-stdio bridge. They ran only Bloem's own reviewed code, "
        "with no sandbox or resource limits, so the extra process added start-up time and new ways to fail "
        "without isolating anything.", size=14)
    box(s, 620, 476, 540, 148, a, "Since 26 October 2026",
        "Inside the server, as plain Go code: promotions.Engine and ambience.Engine. Tests can swap in another "
        "evaluator to watch it or make it fail.", size=14)
    s.arrow(584, 550, 616, 550)
    footer(s, "Membership, profile and PIN, library access, dismissals and artwork access always stay with "
              "the host.", y=660, fill=INK)
    return s


PICTURES = [("host-and-evaluator", host_and_evaluator)]
