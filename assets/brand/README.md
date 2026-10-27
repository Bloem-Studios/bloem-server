# Bloem brand asset set

This directory holds the vector sources of Bloem's own visual identity, added on
August 25, 2026 (`4982bda25`, with the brighter "vivid" bloom in `bf016c37c` and
the classic one kept alongside it in `48343fd17`). They are original Bloem work,
not derived from Silo's reserved logo or wordmark; see [`NOTICE`](NOTICE) and
[fork provenance](../../FORK.md#ongoing-merge-cost). Silo's own README and
trademark rules require a fork to replace Silo's brand assets, which is why these
exist.

Everything here is generated from vector sources, so any size can be re-rendered
cleanly. The tulip geometry was taken from a storyboard animation (`bloem_animation.svg`)
with the animation removed, and the wordmark was redrawn as vector from a raster
lockup (`Bloem Black.png`). Neither of those two source files is in this
repository.

## Palette

| Role | Hex | Notes |
|---|---|---|
| Accent | `#FA6400` | midpoint of the bloom gradient (`#FA3C00` deep → `#FA8200` amber) |
| Plate | `#101827` | app-icon / badge background, inherited from the existing icon set |
| Ink on dark | `#FFFFFF` | leaves, stem, lettering |
| Ink on light | `#101827` | leaves, stem, lettering |

## Vector sources

| File | Use |
|---|---|
| `bloem-mark-detail-{dark,light}.svg` | full mark, gradient bloom + 11 dissolve pixels — 64px and up |
| `bloem-mark-simple-{dark,light}.svg` | heavier stroke, flat accent, 4 pixels — 24–48px |
| `bloem-mark-micro-{dark,light}.svg` | no dissolve pixels, heaviest stroke — 16–20px |
| `bloem-mark-mono-{white,dark}.svg` | single colour, no accent — watermarks, stencils, one-colour print |
| `bloem-wordmark-{dark,light}.svg` | BLOEM lettering only |
| `bloem-lockup-{dark,light}.svg` | mark + wordmark |

"dark" means *for use on dark backgrounds* (light ink); "light" is the inverse.

### Why three mark weights

The mark is thin-stroked and carries eleven small dissolve pixels. Rendered at
32px those pixels collapse into noise and the strokes drop below a pixel, so a
single asset cannot serve both a 1024px app icon and a 16px favicon. Each weight
is the same drawing with the detail budget matched to the size.

### Gradient variants: classic vs vivid

Two bloom treatments are kept, and both are maintained:

- **classic** (`bloem-mark-detail-*`, `bloem-lockup-{dark,light}.svg`) — the
  original storyboard gradient, which passes through a dark maroon (`#8f1918`)
  at mid-petal on its way from the pale base to the amber tip.
- **vivid** (`*-vivid.svg`) — the same drawing with that stop replaced by
  saturated `#FA6400` and the pale base shortened from 38% to 26%, so the
  petals hold colour over more of their length. This is what the shipped icons
  render from, because the maroon band read as muddy at sidebar and favicon
  sizes.

`-tight` marks the viewBox cropped to the ink; use those for anything composited
onto an icon plate, and the untrimmed originals where the artwork needs its own
breathing room.

## Rendered output

There is no `png/` directory here. The rendered files are committed where they
are used: the web app's icons in `web/public/`, and the repository artwork in
`assets/`. Several `web/public/` files keep the file names of the Silo icons they
replace, so they are declared seams in `contracts/seams.txt`; Silo's
`silo-icon-1024.png` and `silo-wordmark-sidebar.png` are deleted.

App icons sit on an opaque `#101827` rounded plate rather than shipping
transparent: a transparent mark disappears against either a light tab strip or a
dark launcher, and the plate is what the existing Silo/Bloem icons used.

| File | Size | Mark weight |
|---|---|---|
| `web/public/bloem-icon-1024.png` | 1024 | detail |
| `web/public/web-app-icon-512.png` | 512 | detail |
| `web/public/web-app-icon-192.png` | 192 | detail |
| `web/public/apple-touch-icon.png` | 180 | detail |
| `web/public/maskable-icon-512.png` | 512 | detail, glyph at 55% for the maskable safe zone |
| `web/public/favicon.ico` | 64/48/32/16 | detail / simple / simple / micro |
| `web/public/bloem-wordmark-sidebar.png` | 382×112 | simple (badge) + vector wordmark |
| `assets/icon.png` | 256 | detail |
| `assets/bloem-banner.png` | 860×220 | lockup on an opaque panel for READMEs |
| `assets/bloem-social.png` | 1200×630 | Open Graph / link previews |
| `web/public/bloem-login-bg.png` | 1920×1080 | login background; motif is offset right, form area left is clear |

The banner and social card are drawn on opaque panels deliberately — GitHub and
most link-preview surfaces render on both light and dark backgrounds, and the
lettering is white.

## Re-rendering

```sh
rsvg-convert -w 512 -h 512 bloem-mark-detail-dark.svg -o out.png
```
