"""Pictures for docs/bloem/overlays/auth-api.md."""
from xml.sax.saxutils import escape

from kit import INK, MUTED, TIERS, Svg, footer, no_mark, para

MONO = "'SFMono-Regular', Menlo, Consolas, 'Liberation Mono', monospace"


def _mono(s, x, y, text, size=13, fill=INK, anchor="start"):
    s.add(f'<text x="{x}" y="{y}" font-family="{MONO}" font-size="{size}" fill="{fill}" '
          f'text-anchor="{anchor}">{escape(text)}</text>')


def _panel(s, x, y, w, h, color, n, title):
    s.rect(x, y, w, h, "#FFFFFF", "#E5E7EB", rx=14)
    s.rect(x, y, 8, h, color["stroke"], rx=4)
    s.number(x + 34, y + 30, n, color["stroke"], r=12)
    s.text(x + 56, y + 36, title, size=17, weight="700")


def _chip(s, x, y, w, label, color, size=13.5, mono=False):
    s.rect(x, y, w, 34, color["fill"], color["stroke"], rx=10, sw=1.4)
    if mono:
        _mono(s, x + w / 2, y + 22, label, size=size - 0.5, anchor="middle")
    else:
        s.text(x + w / 2, y + 22, label, size=size, weight="600", anchor="middle")


def sign_in_additions():
    s = Svg(1200, 806, "What Bloem adds to signing in",
            "Four panels. One: a profile with its own email and password signs straight into that profile, "
            "and the session reaches only a short list of routes. Two: signing in on another organization's "
            "domain is refused with 409 wrong_organization_domain, naming the address to use. Three: a sign-in "
            "through the provider starts and finishes on the same organization domain, and sign-out returns to "
            "that domain's login page. Four: a sign-in that started before its organization revoked the account "
            "is ended with 401 session_invalid, and the app asks the person to sign in again.")
    s.header("What Bloem adds to signing in",
             "Silo's sign-in, plus profiles with their own sign-in and organizations with their own addresses.")
    f, st, o, w, a, g = (TIERS["profile"], TIERS["stop"], TIERS["org"], TIERS["warn"], TIERS["account"],
                         TIERS["grey"])
    pw, ph = 550, 284

    # 1. A profile with its own sign-in.
    x, y = 40, 120
    _panel(s, x, y, pw, ph, f, 1, "A profile with its own sign-in")
    para(s, x + 28, y + 66, "The household's main profile gives one profile an email and password of its own.",
         pw - 56, size=14, gap=19)
    _chip(s, x + 28, y + 122, 190, "profile's email", g)
    _chip(s, x + 28, y + 164, 190, "profile's password", g)
    s.arrow(x + 224, y + 158, x + 278, y + 158)
    s.rect(x + 282, y + 122, 238, 76, f["fill"], f["stroke"], rx=12, sw=1.6)
    s.text(x + 401, y + 154, "Straight into", size=15, weight="700", anchor="middle")
    s.text(x + 401, y + 176, "that one profile", size=15, weight="700", anchor="middle")
    para(s, x + 28, y + 236, "The session reaches only a short list of routes.", pw - 56, size=14, gap=19)

    # 2. Another organization's domain.
    x = 610
    _panel(s, x, y, pw, ph, st, 2, "Signing in on another organization's domain")
    para(s, x + 28, y + 66, "Sam's account belongs to Riverside Club. Sam signs in on another club's "
         "address.", pw - 56, size=14, gap=19)
    _chip(s, x + 28, y + 122, 210, "harbour.example", g, mono=True)
    s.arrow(x + 244, y + 139, x + 284, y + 139)
    s.rect(x + 288, y + 112, 234, 104, st["fill"], st["stroke"], rx=12, sw=1.6)
    no_mark(s, x + 312, y + 138)
    s.text(x + 334, y + 143, "Refused, 409", size=15, weight="700", fill=st["label"])
    s.lines(x + 306, y + 172, ["“This account signs in at", "https://riverside.example.”"], size=13, gap=18,
            fill=INK)
    para(s, x + 28, y + 246, "An organization without a domain signs in at the server's public address.",
         pw - 56, size=14, gap=19)

    # 3. Provider sign-in.
    x, y = 40, 430
    _panel(s, x, y, pw, ph, o, 3, "Through the sign-in provider")
    steps = [("riverside.example", "starts"), ("provider", "Authentik"), ("riverside.example", "finishes")]
    cx = x + 28
    for i, (label, note) in enumerate(steps):
        box_w = 160 if i != 1 else 120
        _chip(s, cx, y + 70, box_w, label, o if i != 1 else g, mono=(i != 1), size=13)
        s.text(cx + box_w / 2, y + 126, note, size=13, fill=MUTED, anchor="middle")
        if i < 2:
            s.arrow(cx + box_w + 3, y + 87, cx + box_w + 24, y + 87)
        cx += box_w + 28
    para(s, x + 28, y + 168, "The provider sends the browser back to the same domain, where its cookies are. "
         "Sign-out returns to that domain's login page.", pw - 56, size=14, gap=19)
    para(s, x + 28, y + 230, "So each domain's two callbacks and its /login are registered at the provider.",
         pw - 56, size=14, gap=19, fill=o["label"])

    # 4. Revoked sign-in.
    x = 610
    _panel(s, x, y, pw, ph, w, 4, "A sign-in its organization revoked")
    rows = [
        (a, "Monday", "Sam signs in on the web app."),
        (w, "Tuesday", "Riverside Club revokes Sam's account."),
        (st, "Next request", "401 session_invalid: the session ends."),
        (f, "Then", "The app asks Sam to sign in again."),
    ]
    ry = y + 64
    for c, when, what in rows:
        s.rect(x + 28, ry, 120, 34, c["fill"], c["stroke"], rx=10, sw=1.3)
        s.text(x + 88, ry + 22, when, size=13.5, weight="700", anchor="middle")
        s.text(x + 162, ry + 22, what, size=14, fill=INK)
        ry += 48

    footer(s, "The club names and addresses are made up. Every refusal happens before a session exists, except "
              "the last, which ends one. A domain decides how Bloem looks, never what someone may see.", y=772,
           fill=INK)
    return s


PICTURES = [("sign-in-additions", sign_in_additions)]
