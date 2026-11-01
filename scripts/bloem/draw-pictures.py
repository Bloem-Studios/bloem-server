#!/usr/bin/env python3
"""Draw the explainer pictures under docs/architecture/images/ as SVG files.

Each page's pictures are drawn by one module in scripts/bloem/pictures/ and land
in docs/architecture/images/<page>/. Redraw them here when what they show
changes, never by hand.

Usage (from the repository root):
  python3 scripts/bloem/draw-pictures.py            # every page
  python3 scripts/bloem/draw-pictures.py live-tv    # one page
  python3 scripts/bloem/draw-pictures.py --check    # fail if a file is out of date
"""
import importlib
import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "pictures"))

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
IMAGES = os.path.join(ROOT, "docs", "architecture", "images")

# Image directory -> drawing module.
PAGES = {
    "organizations": "organizations",
    "live-tv": "live_tv",
    "plans": "plans",
    "storage": "storage",
    "requests": "requests",
    "messages": "messages",
    "apps": "apps",
    "silo-switching": "silo_switching",
    "following-silo": "following_silo",
}


def main(argv):
    check = "--check" in argv
    names = [a for a in argv if not a.startswith("--")] or list(PAGES)
    stale = []
    for name in names:
        if name not in PAGES:
            sys.exit(f"unknown page {name!r}; one of: {', '.join(PAGES)}")
        module = importlib.import_module(PAGES[name])
        out = os.path.join(IMAGES, name)
        os.makedirs(out, exist_ok=True)
        for picture, draw in module.PICTURES:
            path = os.path.join(out, f"{picture}.svg")
            svg = draw().render()
            if check:
                current = open(path).read() if os.path.exists(path) else None
                if current != svg:
                    stale.append(os.path.relpath(path, ROOT))
                continue
            with open(path, "w") as fh:
                fh.write(svg)
            print(os.path.relpath(path, ROOT))
    if stale:
        sys.exit("out of date, run python3 scripts/bloem/draw-pictures.py:\n  " + "\n  ".join(stale))


if __name__ == "__main__":
    main(sys.argv[1:])
