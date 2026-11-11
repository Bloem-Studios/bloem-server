"""Pictures for docs/architecture/seasonal-scheduling.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, box, footer, para

SEASON = {"fill": "#EFF6FF", "stroke": "#3B82F6", "label": "#1D4ED8"}


def from_calendar_to_screen():
    s = Svg(1200, 760, "From calendar to screen",
            "Left: the seasonal packs stored on the Bloem server, each with a window that may repeat every year, "
            "for everyone or for one organization. Middle: the server works out which packs are on right now and "
            "turns a yearly window into this year's exact start and end. Right: a signed-out page asks the "
            "public route and gets only packs for everyone; a signed-in Home asks the viewer route and also gets "
            "its own organization's packs.")
    s.header("From calendar to screen",
             "The server keeps the calendar. Apps only ever see this year's exact start and end.")
    p, o, a, g = TIERS["platform"], TIERS["org"], TIERS["account"], TIERS["grey"]

    # Stored packs.
    s.text(40, 140, "Stored on the server", size=17, weight="700")
    packs = [
        (SEASON, "Winter snow", "1 Dec to 7 Jan yearly, Amsterdam time", "for everyone"),
        (TIERS["plan"], "Spring banner", "20 Mar to 20 Apr, this year", "for everyone"),
        (o, "Club anniversary", "1 to 8 May, every year", "Riverside Club only"),
    ]
    y = 158
    for color, name, when, who in packs:
        s.rect(40, y, 330, 98, color["fill"], color["stroke"], rx=14, sw=1.6)
        s.text(60, y + 32, name, size=16, weight="700")
        s.text(60, y + 56, when, size=14, fill=MUTED)
        s.badge(60, y + 66, who, color, size=12)
        y += 112

    # Server resolves.
    s.rect(430, 158, 330, 322, p["fill"], p["stroke"], rx=18, sw=2)
    s.text(595, 192, "The server decides", size=17, weight="700", anchor="middle")
    steps = [
        "Which packs are on at this moment.",
        "For a yearly window, this year's start and end in its own time zone.",
        "Which packs this viewer may see: everyone's, plus their own organization's.",
    ]
    yy = 220
    for i, text in enumerate(steps):
        s.number(462, yy + 6, i + 1, p["stroke"], r=12)
        yy = para(s, 486, yy + 11, text, 252, size=14, fill=INK, gap=19) + 18
    s.arrow(374, 300, 426, 300)

    # Two readers.
    box(s, 830, 158, 330, 150, g, "Signed-out page", "The sign-in page asks the public route and gets only "
        "the packs for everyone.", size=14)
    s.text(848, 292, "/api/bloem/v2/ambience/public", size=12.5, fill=SOFT)
    box(s, 830, 330, 330, 150, a, "Signed-in Home", "Asks the viewer route with the chosen profile, and also "
        "gets its own organization's packs.", size=14)
    s.text(848, 464, "/api/bloem/v2/ambience", size=12.5, fill=SOFT)
    s.arrow(764, 270, 826, 234)
    s.arrow(764, 368, 826, 404)

    # What the app gets.
    s.rect(40, 520, 1120, 150, "#FFFFFF", "#E5E7EB", rx=14)
    s.text(64, 554, "What an app receives", size=17, weight="700")
    s.rect(64, 572, 500, 74, "#F9FAFB", "#E5E7EB", rx=10)
    s.text(84, 600, "Winter snow, this season", size=15, weight="700")
    s.text(84, 626, "from 2026-11-30 23:00 UTC to 2027-01-06 23:00 UTC", size=14, fill=MUTED)
    para(s, 600, 588, "No repeat rule and no time zone: just this year's start and end. Apps hide the effect "
         "by themselves when the end passes, even offline. The web app looks again every 30 seconds.", 536,
         size=14, fill=INK, gap=20)

    footer(s, "This example is made up. A pack is switched on and off by the server's clock, so it works when "
              "the storefront is offline.", y=718, fill=MUTED)
    return s


PICTURES = [("from-calendar-to-screen", from_calendar_to_screen)]
