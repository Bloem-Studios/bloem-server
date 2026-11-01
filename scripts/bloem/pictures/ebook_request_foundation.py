"""Pictures for docs/architecture/bloem-ebook-request-foundation.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, footer, no_mark, para, wrap, yes_mark

BOOK = {"fill": "#FFF7ED", "stroke": "#EA580C", "label": "#9A3412"}


def journey():
    s = Svg(1200, 636, "An ebook request, from ISBN to book",
            "Six steps: a profile in the default organization asks by ISBN, an administrator approves, Bloem hands the "
            "request to Bookwarehouse through a connection linked to a library, Bookwarehouse prepares it while Bloem "
            "checks every five minutes, the request waits for the library scan, and it is done when an EPUB or PDF "
            "with that ISBN appears in the linked library. Below, what completes a request and what never does.")
    s.header("An ebook request, from ISBN to book",
             "Off until a platform administrator switches it on. Only the default organization can ask.")
    o, a, w, g, f = TIERS["org"], TIERS["account"], TIERS["warn"], TIERS["grey"], TIERS["profile"]
    steps = [
        (f, "Ask", "A profile in the default organization gives an ISBN."),
        (w, "Approve", "Automatically, or by an administrator of the default organization."),
        (o, "Hand over", "Bloem sends it to Bookwarehouse through a connection linked to one library. No "
         "connection: it waits."),
        (BOOK, "Prepare", "Bookwarehouse works on the order. Bloem asks how it is going every five minutes."),
        (g, "Wait for the scan", "Bookwarehouse says it is done. The request waits for the library scan."),
        (a, "Done", "An EPUB or PDF with that ISBN is in the linked library. The requester gets a notice."),
    ]
    x0, y0, cw, ch, gap = 40, 128, 170, 226, 20
    for i, (c, t, b) in enumerate(steps):
        x = x0 + i * (cw + gap)
        s.rect(x, y0, cw, ch, c["fill"], c["stroke"], rx=14)
        s.number(x + 26, y0 + 28, i + 1, c["stroke"])
        s.text(x + 16, y0 + 74, t, size=16, weight="700")
        para(s, x + 16, y0 + 102, b, cw - 40, size=13.5, gap=18)
        if i < len(steps) - 1:
            s.arrow(x + cw + 2, y0 + ch / 2, x + cw + gap - 2, y0 + ch / 2)

    # What completes it, and what never does.
    s.rect(40, 388, 520, 196, a["fill"], a["stroke"], rx=14, sw=1.6)
    yes_mark(s, 70, 422, r=13)
    s.text(94, 428, "This completes the request", size=17, weight="700")
    rows = ["An ebook with the same ISBN,", "an EPUB or PDF file,", "in the linked library,",
            "read from that library's Bookwarehouse store."]
    s.lines(64, 466, rows, size=15, fill=INK, gap=24)

    s.rect(600, 388, 560, 196, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(626, 428, "These never do", size=17, weight="700")
    nos = ["A similar title", "An audiobook with the same ISBN", "A book in another library",
           "Marking it fulfilled by hand"]
    for i, n in enumerate(nos):
        y = 462 + i * 30
        no_mark(s, 638, y - 5, r=10)
        s.text(660, y, n, size=15, fill=INK)
    footer(s, "Withdrawing a request, or switching the lane off, only affects Bloem. Bookwarehouse may finish "
              "work it already started.", y=616, size=14, fill=MUTED)
    return s


def not_sure():
    s = Svg(1200, 716, "When Bloem is not sure an order reached Bookwarehouse",
            "Bloem hands an order to Bookwarehouse. If it is not sure the order arrived, it only asks Bookwarehouse "
            "what happened and never sends it again. If it knows nothing was sent, it sends again later. After five "
            "failures the request stops as failed, and an administrator's retry checks or sends again accordingly.")
    s.header("When Bloem is not sure an order arrived",
             "Bookwarehouse cannot tell a repeated order from a new one, so Bloem never guesses.")
    o, a, w, st = TIERS["org"], TIERS["account"], TIERS["warn"], TIERS["stop"]

    s.rect(400, 120, 400, 70, o["fill"], o["stroke"], rx=14, sw=1.8)
    s.text(600, 162, "Bloem hands an order to Bookwarehouse", size=17, weight="700", anchor="middle")
    s.path("M520,194 C520,230 300,220 300,252")
    s.path("M680,194 C680,230 900,220 900,252")

    branches = [
        (40, w, "Not sure it arrived", "A timeout, or Bloem restarted at the wrong moment.",
         "Only asks Bookwarehouse what happened. Never sends again.",
         "Submission outcome is unknown; checking the backend without submitting again."),
        (640, a, "Sure nothing was sent", "Bookwarehouse refused before taking it, or the plugin was not running.",
         "Sends the order again later.",
         "The submission did not reach the backend; it will be submitted again."),
    ]
    for x, c, title, why, does, quote in branches:
        s.rect(x, 256, 520, 262, c["fill"], c["stroke"], rx=16, sw=1.8)
        s.text(x + 24, 292, title, size=19, weight="700")
        para(s, x + 24, 320, why, 472, size=14.5, gap=20)
        s.text(x + 24, 372, "Bloem", size=12.5, weight="700", fill=c["label"], spacing="1")
        para(s, x + 24, 396, does, 472, size=15.5, fill=INK, weight="600", gap=21)
        s.rect(x + 16, 432, 488, 70, "#FFFFFF", "#E5E7EB", rx=10)
        s.text(x + 30, 452, "THE REQUESTER SEES", size=11, weight="700", fill=SOFT, spacing="1")
        rows = wrap(quote, 430, 13.5)
        s.lines(x + 30, 474, rows, size=13.5, fill=INK, gap=18)

    s.path("M300,522 C300,548 380,546 420,560")
    s.path("M900,522 C900,548 820,546 780,560")
    s.rect(240, 564, 720, 96, st["fill"], st["stroke"], rx=14, sw=1.6)
    s.text(600, 598, "After five failures the request stops as failed", size=17, weight="700", anchor="middle")
    s.text(600, 626, "An administrator's retry checks the existing order if one might have arrived,",
           size=14.5, fill=INK, anchor="middle")
    s.text(600, 646, "or sends again if nothing was ever sent.", size=14.5, fill=INK, anchor="middle")
    footer(s, "Uncertain checks wait 30 seconds at first, doubling up to 15 minutes.", y=696, size=13.5, fill=MUTED)
    return s


PICTURES = [("journey", journey), ("not-sure", not_sure)]
