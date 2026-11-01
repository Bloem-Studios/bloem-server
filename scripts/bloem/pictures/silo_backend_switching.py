"""Pictures for docs/architecture/silo-backend-switching.md."""
from kit import INK, LINE, MUTED, SOFT, TIERS, Svg, box, footer, no_mark, para, wrap, yes_mark

SILO = {"fill": "#F0F9FF", "stroke": "#0284C7", "label": "#075985"}
BLOEM = {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"}


def silo_writes():
    s = Svg(1200, 780, "What happens to Silo's writes while switching is on",
            "Six things Silo can write, and what the database does with each while Silo switching is on. A new "
            "profile is placed in the default organization; a new account gets a membership when its transaction "
            "commits; an account moved to another group takes its profiles with it; a missing device is stored as "
            "empty. A second organization and a profile with its own sign-in are refused.")
    s.header("What happens to Silo's writes while switching is on",
             "Database rules fit Silo's writes into Bloem's model, and refuse what Silo could not honor.")
    a, st = TIERS["account"], TIERS["stop"]

    s.text(40, 136, "Silo writes", size=17, weight="700", fill=SILO["label"])
    s.text(500, 136, "The database", size=17, weight="700")
    rows = [
        (True, "A new profile, with no organization",
         "Placed in the default organization, with the account's membership and the account's access group."),
        (True, "A new account",
         "Gets its membership in the default organization when the transaction commits."),
        (True, "An account moved to another group",
         "The account's profiles on the old group move with it. Its role stays in step both ways."),
        (True, "A sign-in with no device",
         "The missing device is stored as an empty value instead of NULL."),
        (False, "A second organization",
         "Refused: Silo has no organizations, so it could not keep two apart."),
        (False, "A profile with its own sign-in",
         "Refused: Silo would treat that sign-in as the whole account."),
    ]
    y = 152
    for ok, what, result in rows:
        s.rect(40, y, 400, 82, SILO["fill"], SILO["stroke"], rx=12, sw=1.5)
        para(s, 60, y + 46, what, 360, size=15.5, fill=INK, weight="600")
        s.arrow(444, y + 41, 494, y + 41)
        color = a if ok else st
        s.rect(500, y, 660, 82, color["fill"], color["stroke"], rx=12, sw=1.5)
        if ok:
            yes_mark(s, 530, y + 41)
        else:
            no_mark(s, 530, y + 41)
        rows_y = y + 36 if len(wrap(result, 580, 14.5)) > 1 else y + 46
        para(s, 556, rows_y, result, 580, size=14.5, fill=INK, gap=20)
        y += 96
    footer(s, "The empty-device rule always applies. The others work only while public.bloem_silo_switching is "
              "on; switching it off lifts them, and Silo must not serve the database again.", y=744, fill=INK)
    return s


def commands():
    s = Svg(1200, 620, "Switching on, checking and switching off",
            "Three commands. silo silo-switching status reports whether the database is switchable. enable "
            "refuses unless there is exactly one organization and no profile has its own sign-in, then gives "
            "every account its default-organization membership, matches membership roles to the account's role, "
            "and sets the flag. disable clears the flag; after that, Silo must not serve the database.")
    s.header("Switching on, checking and switching off",
             "One flag in the database, set and cleared with the Bloem binary.")
    g, a, st, w = TIERS["grey"], TIERS["account"], TIERS["stop"], TIERS["warn"]

    box(s, 40, 128, 340, 130, g, "silo silo-switching status",
        "Reports whether the database is switchable. Changes nothing.", size=14.5, title_size=16)

    s.rect(420, 128, 360, 410, a["fill"], a["stroke"], rx=14, sw=1.8)
    s.text(440, 160, "silo silo-switching enable", size=16, weight="700")
    y = 182

    def step(n, t, y):
        s.number(456, y + 14, n, a["stroke"], r=12)
        rows = wrap(t, 270, 14.5)
        para(s, 478, y + 19, t, 270, size=14.5, fill=INK, gap=19)
        return y + 26 + 19 * len(rows)

    y = step(1, "Exactly one organization?", y)
    y = step(2, "No profile with its own sign-in?", y)
    s.rect(440, y, 320, 40, st["fill"], st["stroke"], rx=10, sw=1.2)
    s.text(456, y + 25, "If not: refused, nothing changes.", size=14, weight="600", fill=st["label"])
    y += 56
    y = step(3, "Give every account its default-organization membership.", y)
    y = step(4, "Match membership roles to users.role.", y)
    y = step(5, "Set the flag.", y)
    para(s, 440, y + 14, "Safe to run again: it only fills in what is missing.", 320, size=13.5, fill=MUTED)

    box(s, 820, 128, 340, 170, w, "silo silo-switching disable",
        "Clears the flag and lifts the rules. From then on, Silo must not serve the database: Bloem may add "
        "what Silo cannot read.", size=14.5, title_size=16)

    footer(s, "Every switching rule reads the flag through public.bloem_silo_switching_enabled().", y=584,
           fill=INK)
    return s


PICTURES = [("silo-writes", silo_writes), ("commands", commands)]
