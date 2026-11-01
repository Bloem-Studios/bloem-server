"""Shared drawing kit for the explainer pictures in docs/architecture/images/.

Every picture is a plain SVG with a white card background, the same fonts and the
same colors, so the pages read as one set. Drawing code lives in one module per
page (organizations.py, live_tv.py, ...); scripts/bloem/draw-pictures.py runs them.
"""
from xml.sax.saxutils import escape

FONT = "'Segoe UI', 'Helvetica Neue', Helvetica, Arial, sans-serif"
INK = "#101827"
MUTED = "#4B5563"
SOFT = "#6B7280"
LINE = "#9CA3AF"

TIERS = {
    "platform": {"fill": "#FFF4EC", "stroke": "#FA6400", "label": "#C2410C"},
    "org": {"fill": "#EEF2FF", "stroke": "#6366F1", "label": "#4338CA"},
    "account": {"fill": "#ECFDF5", "stroke": "#10B981", "label": "#047857"},
    "profile": {"fill": "#F0F9FF", "stroke": "#0EA5E9", "label": "#0369A1"},
    "plan": {"fill": "#F0FDFA", "stroke": "#14B8A6", "label": "#0F766E"},
    "warn": {"fill": "#FFFBEB", "stroke": "#F59E0B", "label": "#B45309"},
    "stop": {"fill": "#FEF2F2", "stroke": "#EF4444", "label": "#B91C1C"},
    "grey": {"fill": "#F3F4F6", "stroke": "#9CA3AF", "label": "#374151"},
}


class Svg:
    def __init__(self, width, height, title, desc):
        self.w, self.h = width, height
        self.parts = []
        self.title, self.desc = title, desc

    def add(self, s):
        self.parts.append(s)

    def rect(self, x, y, w, h, fill, stroke="none", rx=12, sw=1.5, dash=None):
        d = f' stroke-dasharray="{dash}"' if dash else ""
        self.add(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{rx}" fill="{fill}" stroke="{stroke}" stroke-width="{sw}"{d}/>')

    def text(self, x, y, s, size=15, weight="normal", fill=INK, anchor="start", spacing=None, italic=False):
        extra = f' letter-spacing="{spacing}"' if spacing else ""
        if italic:
            extra += ' font-style="italic"'
        self.add(f'<text x="{x}" y="{y}" font-size="{size}" font-weight="{weight}" fill="{fill}" text-anchor="{anchor}"{extra}>{escape(s)}</text>')

    def lines(self, x, y, rows, size=14, fill=MUTED, gap=None, weight="normal", anchor="start"):
        gap = gap or round(size * 1.4)
        for i, row in enumerate(rows):
            self.text(x, y + i * gap, row, size=size, fill=fill, weight=weight, anchor=anchor)

    def badge(self, x, y, label, color, size=13, anchor="start"):
        """Pill with a label. x is the left edge, or the right edge with anchor="end"."""
        w = round(len(label) * size * 0.58 + 22)
        left = x - w if anchor == "end" else x
        self.rect(left, y, w, size + 13, color["fill"], color["stroke"], rx=(size + 13) / 2, sw=1.2)
        self.text(left + w / 2, y + (size + 13) / 2 + size * 0.36, label, size=size, weight="600", fill=color["label"], anchor="middle")
        return w

    def number(self, cx, cy, n, color, r=13):
        self.add(f'<circle cx="{cx}" cy="{cy}" r="{r}" fill="{color}"/>')
        self.text(cx, cy + 5, str(n), size=14, weight="700", fill="#FFFFFF", anchor="middle")

    def arrow(self, x1, y1, x2, y2, color=LINE, dash=None, width=2):
        d = f' stroke-dasharray="{dash}"' if dash else ""
        self.add(f'<line x1="{x1}" y1="{y1}" x2="{x2}" y2="{y2}" stroke="{color}" stroke-width="{width}"{d} marker-end="url(#arrow)"/>')

    def path(self, d, color=LINE, dash=None, width=2):
        da = f' stroke-dasharray="{dash}"' if dash else ""
        self.add(f'<path d="{d}" fill="none" stroke="{color}" stroke-width="{width}"{da} marker-end="url(#arrow)"/>')

    def header(self, title, subtitle):
        self.text(40, 56, title, size=28, weight="700")
        self.text(40, 88, subtitle, size=17, fill=MUTED)

    def render(self):
        head = (
            f'<svg xmlns="http://www.w3.org/2000/svg" width="{self.w}" height="{self.h}" '
            f'viewBox="0 0 {self.w} {self.h}" role="img" aria-labelledby="t d" '
            f'font-family="{FONT}">\n'
            f'<title id="t">{escape(self.title)}</title>\n<desc id="d">{escape(self.desc)}</desc>\n'
            '<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" '
            f'orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="{LINE}"/></marker></defs>\n'
            f'<rect x="0.5" y="0.5" width="{self.w - 1}" height="{self.h - 1}" rx="16" fill="#FFFFFF" stroke="#E5E7EB"/>\n'
        )
        return head + "\n".join(self.parts) + "\n</svg>\n"



# Average glyph widths, as a share of the font size, for the fonts above. Used
# only to wrap text and size boxes; generous so text never touches a border.
_CHAR = {"normal": 0.5, "600": 0.53, "700": 0.55}


def text_width(s, size, weight="normal"):
    return len(s) * size * _CHAR.get(weight, 0.5)


def wrap(s, width, size, weight="normal"):
    """Split s into lines no wider than width pixels at this font size."""
    words, rows, row = s.split(), [], ""
    for word in words:
        trial = f"{row} {word}".strip()
        if row and text_width(trial, size, weight) > width:
            rows.append(row)
            row = word
        else:
            row = trial
    if row:
        rows.append(row)
    return rows


def para(svg, x, y, s, width, size=14, fill=MUTED, gap=None, weight="normal", anchor="start"):
    """Draw s wrapped to width; return the y below the last line."""
    gap = gap or round(size * 1.4)
    rows = wrap(s, width, size, weight)
    svg.lines(x, y, rows, size=size, fill=fill, gap=gap, weight=weight, anchor=anchor)
    return y + gap * len(rows)


def card(svg, x, y, w, h, color, title, body, n=None, size=14, title_size=17):
    """A white card with a colored edge, an optional number, a title and wrapped body text."""
    svg.rect(x, y, w, h, "#FFFFFF", "#E5E7EB", rx=12)
    svg.rect(x, y, 8, h, color["stroke"], rx=4)
    tx = x + 24
    if n is not None:
        svg.number(x + 34, y + 28, n, color["stroke"], r=12)
        tx = x + 56
    svg.text(tx, y + 34, title, size=title_size, weight="700")
    return para(svg, x + 24, y + 62, body, w - 44, size=size)


def box(svg, x, y, w, h, color, title, body=None, size=14, title_size=17, sw=1.8, center=False):
    """A tinted box with a bold title and wrapped body text."""
    svg.rect(x, y, w, h, color["fill"], color["stroke"], rx=14, sw=sw)
    if center:
        svg.text(x + w / 2, y + 32, title, size=title_size, weight="700", anchor="middle")
        if body:
            para(svg, x + w / 2, y + 56, body, w - 28, size=size, anchor="middle")
        return
    svg.text(x + 18, y + 32, title, size=title_size, weight="700")
    if body:
        para(svg, x + 18, y + 56, body, w - 36, size=size)


def no_mark(svg, cx, cy, r=12, color="#EF4444"):
    """A red circle with a cross: "not this way"."""
    svg.add(f'<circle cx="{cx}" cy="{cy}" r="{r}" fill="#FFFFFF" stroke="{color}" stroke-width="2.5"/>')
    d = r * 0.45
    svg.add(f'<path d="M{cx - d},{cy - d} L{cx + d},{cy + d} M{cx + d},{cy - d} L{cx - d},{cy + d}" '
            f'stroke="{color}" stroke-width="2.5" stroke-linecap="round"/>')


def yes_mark(svg, cx, cy, r=12, color="#10B981"):
    """A green circle with a tick."""
    svg.add(f'<circle cx="{cx}" cy="{cy}" r="{r}" fill="{color}"/>')
    svg.add(f'<path d="M{cx - r * 0.45},{cy + r * 0.02} L{cx - r * 0.1},{cy + r * 0.38} L{cx + r * 0.5},{cy - r * 0.35}" '
            f'fill="none" stroke="#FFFFFF" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"/>')


def footer(svg, s, y=None, size=14, fill=INK):
    """One or more wrapped lines along the bottom of the picture."""
    y = y or svg.h - 34
    rows = wrap(s, svg.w - 80, size)
    svg.lines(40, y - (len(rows) - 1) * round(size * 1.4), rows, size=size, fill=fill)
