"""Pictures for docs/architecture/bloem-audiobook-requests.md."""
from kit import INK, MUTED, SOFT, TIERS, Svg, footer, no_mark, para, wrap, yes_mark

BOOK = {"fill": "#FFF7ED", "stroke": "#EA580C", "label": "#9A3412"}


def _step_row(s, steps, y0, ch, x0=40, cw=204, gap=25, size=13.5):
    for i, (c, t, b) in enumerate(steps):
        x = x0 + i * (cw + gap)
        s.rect(x, y0, cw, ch, c["fill"], c["stroke"], rx=14)
        s.number(x + 26, y0 + 28, i + 1, c["stroke"])
        para(s, x + 16, y0 + 74, t, cw - 30, size=16.5, fill=INK, weight="700", gap=21)
        top = y0 + 74 + 21 * len(wrap(t, cw - 30, 16.5, "700")) + 8
        para(s, x + 16, top, b, cw - 30, size=size, gap=18)
        if i < len(steps) - 1:
            s.arrow(x + cw + 3, y0 + ch / 2, x + cw + gap - 3, y0 + ch / 2)


def acquisition():
    s = Svg(1200, 750, "How an approved audiobook is fetched",
            "Five steps: the request is approved, Bloem hands it to the audiobook service, the service searches and "
            "downloads, Bloem checks on it every five minutes, and after the library scan Bloem matches the book. "
            "Six cards below show what can hold a request up and what happens then.")
    s.header("How an approved audiobook is fetched",
             "Bloem keeps the request and its count. The audiobook service does the searching and downloading.")
    o, a, w, g = TIERS["org"], TIERS["account"], TIERS["warn"], TIERS["grey"]
    steps = [
        (w, "Approve", "Automatically, or by an administrator of the organization."),
        (o, "Hand over", "Bloem notes the hand-over first, then passes the request on. It never sends the "
         "same request twice."),
        (BOOK, "Search and download", "The service finds a release, can ask an AI model to check it, and starts "
         "the download client."),
        (g, "Check on it", "Every five minutes Bloem asks the service how the download is going."),
        (a, "Match in the library", "After the library scan, Bloem matches the book by its ISBN or ASIN, or as the "
         "one new book in the download folder."),
    ]
    _step_row(s, steps, 128, 236)

    s.text(40, 412, "What can hold it up", size=18, weight="700")
    holds = [
        (g, "Nothing suitable found", "Searched again every six hours. It stops after seven days."),
        (g, "A source is down", "Tried again after 30 minutes."),
        (w, "A person needs to look", "Held for an administrator, who approves another candidate or accepts "
         "the download."),
        (w, "The download client is offline", "The request keeps waiting and is never reported lost. After an "
         "hour it shows as unreachable."),
        (TIERS["stop"], "The download has gone", "After five uncertain checks it is reported lost. A retry "
         "starts a new search."),
        (g, "Withdrawn in Bloem", "The request ends in Bloem. A download that already started is not "
         "cancelled."),
    ]
    cw, chh = 360, 122
    for i, (c, t, b) in enumerate(holds):
        x, y = 40 + (i % 3) * (cw + 20), 432 + (i // 3) * (chh + 16)
        s.rect(x, y, cw, chh, "#FFFFFF", "#E5E7EB", rx=12)
        s.rect(x, y, 6, chh, c["stroke"], rx=3)
        s.text(x + 24, y + 32, t, size=16, weight="700")
        para(s, x + 24, y + 58, b, cw - 44, size=14, gap=19)
    footer(s, "Without a connected service, an approved request waits for an administrator to add the book by hand.",
           y=722, size=14, fill=MUTED)
    return s


def _field(s, x, y, w, label, value, state):
    """A form field: a label above a box. state is 'same', 'new', 'blank' or 'empty'."""
    s.text(x, y, label, size=12.5, weight="700", fill=SOFT, spacing="0.5")
    colors = {"same": ("#FFFFFF", "#D1D5DB"), "new": ("#FFFBEB", "#F59E0B"),
              "blank": ("#FFFFFF", "#D1D5DB"), "empty": ("#F9FAFB", "#D1D5DB")}
    fill, stroke = colors[state]
    s.rect(x, y + 8, w, 36, fill, stroke, rx=8, sw=1.4, dash="5 4" if state == "empty" else None)
    s.text(x + 12, y + 32, value, size=14, fill=INK if state in ("same", "new") else SOFT,
           italic=state in ("blank", "empty"))


def passwords():
    s = Svg(1200, 596, "Editing an audiobook connection's passwords",
            "Four edits side by side. A new password is saved and tied to its address. A blank password is kept "
            "while its address is unchanged. A blank password with a changed address is refused. A cleared address "
            "drops the password tied to it.")
    s.header("Editing an audiobook connection's passwords",
             "Each password is tied to the address it was entered for. Bloem never sends it anywhere else.")
    a, st, w, g = TIERS["account"], TIERS["stop"], TIERS["warn"], TIERS["grey"]
    cases = [
        ("A new password", ("dl.home.example", "same"), ("typed: ••••••••", "new"),
         True, a, "Saved, and tied to this address."),
        ("Blank, same address", ("dl.home.example", "same"), ("left blank", "blank"),
         True, a, "The saved password is kept."),
        ("Blank, new address", ("dl2.home.example", "new"), ("left blank", "blank"),
         False, st, "Refused. Enter the password again for the new address."),
        ("Address cleared", ("cleared", "empty"), ("–", "empty"),
         True, g, "The password tied to that address is dropped."),
    ]
    cw, gap, y0 = 265, 20, 124
    for i, (title, addr, pw, ok, color, result) in enumerate(cases):
        x = 40 + i * (cw + gap)
        s.rect(x, y0, cw, 360, "#FFFFFF", "#E5E7EB", rx=14)
        s.text(x + 20, y0 + 36, title, size=17, weight="700")
        _field(s, x + 20, y0 + 74, cw - 40, "DOWNLOAD CLIENT ADDRESS", *addr)
        _field(s, x + 20, y0 + 150, cw - 40, "PASSWORD", *pw)
        s.arrow(x + cw / 2, y0 + 206, x + cw / 2, y0 + 236)
        s.rect(x + 16, y0 + 242, cw - 32, 102, color["fill"], color["stroke"], rx=12, sw=1.6)
        if ok:
            yes_mark(s, x + 42, y0 + 272, r=11, color=color["stroke"] if color is not a else "#10B981")
        else:
            no_mark(s, x + 42, y0 + 272, r=11)
        para(s, x + 62, y0 + 277, result, cw - 96, size=14.5, fill=INK, gap=20)
    footer(s, "An edit keeps the connection, its library and the downloads in progress. Changing the download "
              "client's address makes downloads already handed to the old client fail their checks, so the editor "
              "warns before saving.", y=546, size=14.5)
    return s


PICTURES = [("acquisition", acquisition), ("passwords", passwords)]
