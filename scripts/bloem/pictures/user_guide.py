"""Pictures for docs/wiki/user-guide.md."""
from kit import INK, MUTED, TIERS, Svg, box, footer, para


def getting_in():
    s = Svg(1200, 560, "Getting in",
            "Three ways in: an invitation link, an invite code, or a login someone made for you. Each leads to "
            "signing in, then to choosing who is watching, then to Home.")
    s.header("Getting in", "Whichever way you were invited, you end up choosing who is watching.")
    a, f = TIERS["account"], TIERS["profile"]
    ways = [
        ("An invitation link", "Open it and choose a password. It works once."),
        ("An invite code", "Choose Sign up on the sign-in page and enter the code."),
        ("A login made for you", "Open the server address and sign in."),
    ]
    for i, (title, body) in enumerate(ways):
        y = 124 + i * 120
        box(s, 40, y, 340, 104, a, title, body, size=14)
        s.arrow(384, y + 52, 452, 266 + i * 28)
    box(s, 456, 234, 236, 120, a, "Sign in", "Your email or username, and your password.", size=14)
    s.arrow(696, 294, 744, 294)
    box(s, 748, 234, 236, 120, f, "Who's watching?", "Pick your profile. Enter its PIN if it has one.", size=14)
    s.arrow(988, 294, 1022, 294)
    s.rect(1026, 259, 134, 70, INK, rx=35)
    s.text(1093, 302, "Home", size=20, weight="700", fill="#FFFFFF", anchor="middle")
    footer(s, "On an organization's own address, the sign-in page shows that organization's name and logo. "
              "Settings → Connect Apps shows what to type into an app.", y=522, fill=INK)
    return s


def sign_in_messages():
    s = Svg(1200, 630, "Two messages you may see at sign-in",
            "A made-up example in two rows. First: Sam's account belongs to Riverside Club; Sam signs in on "
            "Hillside's address, is told the account signs in at Riverside's address, and signs in there. "
            "Second: an organization ends an older sign-in; the app returns to the sign-in page and Sam signs in "
            "again.")
    s.header("Two messages you may see at sign-in",
             "Both mean the same thing: sign in again, in the right place. This example is made up.")
    o, a, st, w = TIERS["org"], TIERS["account"], TIERS["stop"], TIERS["warn"]

    def notice(x, y, w_, h, brand, message, color):
        s.rect(x, y, w_, h, "#FFFFFF", "#D1D5DB", rx=14, sw=1.5)
        s.text(x + 20, y + 32, brand, size=16, weight="700")
        s.rect(x + 20, y + 48, w_ - 40, h - 68, color["fill"], color["stroke"], rx=10, sw=1.2)
        para(s, x + 36, y + 74, message, w_ - 72, size=14, fill=color["label"], weight="600")

    s.text(40, 144, "1. You signed in at another organization's address", size=17, weight="700")
    box(s, 40, 160, 346, 150, o, "Sam opens watch.hillside.example",
        "Sam's account belongs to Riverside Club. Hillside is another organization on the same server.", size=14)
    s.arrow(390, 235, 423, 235)
    notice(427, 160, 346, 150, "Hillside's sign-in page",
           "This account signs in at https://watch.riverside.example.", st)
    s.arrow(777, 235, 810, 235)
    box(s, 814, 160, 346, 150, a, "Sam opens that address",
        "Riverside Club's own address, in its own colours. The same email and password work there.", size=14)

    s.text(40, 374, "2. Your organization ended an older sign-in", size=17, weight="700")
    box(s, 40, 390, 346, 150, w, "Your access changed",
        "For example, your organization's administrator signed everyone out or changed your access.", size=14)
    s.arrow(390, 465, 423, 465)
    notice(427, 390, 346, 150, "The app",
           "Your profiles stop loading, and the app takes you to the sign-in page.", w)
    s.arrow(777, 465, 810, 465)
    box(s, 814, 390, 346, 150, a, "Sign in again",
        "With your email and password. If that fails too, ask your organization's administrator.", size=14)

    footer(s, "An organization without an address of its own signs in at the server's public address. An address "
              "only changes what you see, never what you can watch.", y=594, fill=INK)
    return s


def how_it_plays():
    s = Svg(1200, 520, "How a video reaches your screen",
            "The file on the server meets one question: can your device play it as it is, over this connection? "
            "Yes: it is sent unchanged (direct play). No: the server converts it as it plays (transcoding). "
            "Quality set to Auto picks the best the connection can carry.")
    s.header("How a video reaches your screen", "The server decides for you each time you press play.")
    p, a, w, g = TIERS["platform"], TIERS["account"], TIERS["warn"], TIERS["grey"]
    box(s, 40, 170, 260, 130, p, "The file", "One film or episode, saved in whatever format it came in.", size=14)
    s.arrow(304, 235, 356, 235)
    s.rect(360, 160, 340, 150, g["fill"], g["stroke"], rx=14, sw=1.8)
    para(s, 530, 226, "Can your device play it as it is, over this connection?", 290, size=18, fill=INK,
         weight="700", anchor="middle")
    s.path("M 704 210 L 740 210 L 740 182 L 776 182")
    s.text(712, 196, "Yes", size=13, weight="700", fill=a["label"])
    s.path("M 704 262 L 740 262 L 740 318 L 776 318")
    s.text(712, 286, "No", size=13, weight="700", fill=TIERS["stop"]["label"])
    box(s, 780, 124, 380, 116, a, "Sent as it is", "Direct play: full quality, and little work for the server.",
        size=14)
    box(s, 780, 260, 380, 130, w, "Converted as it plays",
        "Transcoding: for an unusual format or a connection too slow for the original. Nothing for you to do.",
        size=14)
    s.rect(40, 420, 1120, 60, TIERS["plan"]["fill"], TIERS["plan"]["stroke"], rx=12, sw=1.5)
    para(s, 60, 455, "Quality → Auto picks the best your connection can carry. A film that looks softer away "
         "from home is the conversion adapting to the connection.", 1080, size=14.5, fill=INK)
    return s


PICTURES = [("getting-in", getting_in), ("sign-in-messages", sign_in_messages), ("how-it-plays", how_it_plays)]
