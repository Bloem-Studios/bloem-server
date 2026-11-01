# Bloem's brand assets

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`. These files are Bloem's own visual identity, a Bloem addition:
> Silo's name, logo and wordmark are Silo's trademarks, and a fork must replace them. The
> technical rules are in the [For developers](#for-developers) section.

This folder holds the drawings Bloem's logo and name are made from. This page walks
through the pieces, which one to use at which size, the colours, and where the finished
icons live.

## The pieces

| Piece | What it is | Example |
| --- | --- | --- |
| **Mark** | The tulip on its own | ![The Bloem tulip mark: white petals turning orange at the top, with small square pixels drifting off one side](bloem-mark-detail-light-tight-vivid.svg) |
| **Wordmark** | The word BLOEM in Bloem's lettering | ![The word BLOEM in bold geometric capitals](bloem-wordmark-light.svg) |
| **Lockup** | The mark and the wordmark side by side | ![The tulip mark beside the word BLOEM](bloem-lockup-light-vivid.svg) |

Every piece comes in two colourways. **"dark"** means *for use on a dark background*, so
its leaves, stem and lettering are white. **"light"** is the opposite, with dark ink for a
light background.

All of them are **vector** drawings: they can be drawn at any size without going blurry.

## Which mark at which size

![The same tulip at three weights: the detailed mark, the simple mark with a heavier line and four pixels, and the micro mark with the heaviest line and none](bloem-mark-detail-light-tight-vivid.svg) ![Simple mark](bloem-mark-simple-light.svg) ![Micro mark](bloem-mark-micro-light.svg)

One drawing cannot serve every size. The full mark has thin lines and eleven small
"dissolve" pixels drifting off it. At 32 pixels those pixels turn to noise and the lines
grow thinner than a pixel. So there are three weights of the same drawing:

1. **Detail**, for 64 pixels and up: the full drawing with all eleven pixels.
2. **Simple**, for 24 to 48 pixels: a heavier line, one flat orange, and four pixels.
3. **Micro**, for 16 to 20 pixels: the heaviest line and no pixels.

There is also a **mono** mark in a single colour, for watermarks, stencils and
one-colour print.

## Two kinds of bloom: classic and vivid

The detailed mark's petals fade from white at the base to orange at the tip. That fade
comes in two versions, and both are kept:

- **Classic**, the original, passes through a dark red at mid-petal.
- **Vivid** replaces that dark red with Bloem's orange and starts the colour lower on the
  petals. The dark band looked muddy at small sizes, so **every icon Bloem ships is drawn
  from vivid**.

## Colours

| Role | Colour |
| --- | --- |
| Accent | Bloem orange, `#FA6400` |
| Icon plate | Deep navy, `#101827` |
| Ink on dark | White, `#FFFFFF` |
| Ink on light | Deep navy, `#101827` |

App icons sit on a **plate**: an opaque, rounded navy square. A see-through mark vanishes
against a light tab bar or a dark launcher; the plate keeps it visible on both.

## Where the finished icons live

This folder holds only the drawings. The finished images that apps and pages show are
kept where they are used: the web app's icons in `web/public/`, and the repository's
artwork in `assets/`. The banner and the link-preview card are drawn on solid panels on
purpose: GitHub and most link previews may show them on a light or a dark page, and the
lettering is white.

## Who can change what

| Who | What they manage |
| --- | --- |
| Bloem's maintainers | The drawings here and every image rendered from them. |
| Platform administrator | Replacing the server's own logo, wordmark, icon and sign-in background under **Configure → Server → Appearance**, without changing these files. |
| Platform administrator, for an organization | An organization's own name, logo and colours on its domains ([organization domains](../../docs/architecture/bloem-organization-domains.md)). |

## Words used in the code

| On this page | In the repository |
| --- | --- |
| Mark, detail / simple / micro / mono | `bloem-mark-detail-*.svg`, `bloem-mark-simple-*.svg`, `bloem-mark-micro-*.svg`, `bloem-mark-mono-*.svg` |
| Wordmark | `bloem-wordmark-{dark,light}.svg` |
| Lockup | `bloem-lockup-{dark,light}.svg` (classic) and `bloem-lockup-{dark,light}-vivid.svg` |
| Vivid bloom | the `-vivid` file suffix |
| Cropped to the drawing | the `-tight` file suffix |
| Finished icons | `web/public/*.png`, `web/public/favicon.ico`, `assets/*.png` |
| Swapping Silo's images for Bloem's in the web app | `web/bloem-brand-plugin.ts` |
| The server's replaceable branding | `branding.*` settings (`internal/branding`) |

## For developers

### Where these came from

The vector sources were added on August 25, 2026 (`4982bda25`, with the brighter "vivid"
bloom in `bf016c37c` and the classic one kept alongside it in `48343fd17`). They are
original Bloem work, not derived from Silo's reserved logo or wordmark; see
[`NOTICE`](NOTICE) and [fork provenance](../../FORK.md#ongoing-merge-cost). Silo's own
README and trademark rules require a fork to replace Silo's brand assets, which is why
these exist. The notice also says the vector sources are not included in the AGPL
source-code licence grant.

Everything here is generated from vector sources, so any size can be re-rendered
cleanly. The tulip geometry was taken from a storyboard animation
(`bloem_animation.svg`) with the animation removed, and the wordmark was redrawn as
vector from a raster lockup (`Bloem Black.png`). Neither of those two source files is in
this repository.

### Palette

| Role | Hex | Notes |
|---|---|---|
| Accent | `#FA6400` | the simple and micro marks' flat accent and the vivid bloom's mid-petal stop; described as the midpoint of the storyboard's bloom gradient (`#FA3C00` deep → `#FA8200` amber) |
| Plate | `#101827` | app-icon / badge background, inherited from the existing icon set |
| Ink on dark | `#FFFFFF` | leaves, stem, lettering |
| Ink on light | `#101827` | leaves, stem, lettering |

### Vector sources

| File | Use |
|---|---|
| `bloem-mark-detail-{dark,light}.svg` | full mark, gradient bloom + 11 dissolve pixels — 64px and up (classic; `-vivid` variants too) |
| `bloem-mark-simple-{dark,light}.svg` | heavier stroke (13 against the detail mark's 7.5), flat `#FA6400` accent, 4 pixels — 24–48px |
| `bloem-mark-micro-{dark,light}.svg` | no dissolve pixels, heaviest stroke (20) — 16–20px |
| `bloem-mark-mono-{white,dark}.svg` | single colour, no accent — watermarks, stencils, one-colour print |
| `bloem-wordmark-{dark,light}.svg` | BLOEM lettering only |
| `bloem-lockup-{dark,light}.svg` | mark + wordmark (classic; `-vivid` variants too) |

"dark" means *for use on dark backgrounds* (light ink); "light" is the inverse.

### Gradient variants: classic vs vivid

- **classic** (`bloem-mark-detail-*` without `-vivid`, `bloem-lockup-{dark,light}.svg`) —
  the original storyboard gradient: `#F2F2F2` to 38%, then the dark maroon `#8f1918` at
  60%, `#ff3515` at 78% and `#ff9f08` at the tip.
- **vivid** (`*-vivid.svg`) — the same drawing with the maroon stop replaced by saturated
  `#FA6400` and the pale base shortened from 38% to 26%: `#F2F2F2` to 26%, `#FA6400` at
  46%, `#FF5A0F` at 72% and `#FFA608` at the tip, so the petals hold colour over more of
  their length. This is what the shipped icons render from.

`-tight` marks the viewBox cropped to the ink (the detail mark's `0 0 416 416` becomes
`50 56 336 306`); use those for anything composited onto an icon plate, and the untrimmed
originals where the artwork needs its own breathing room. Only the detail marks have
`-tight` variants.

### Rendered output

There is no `png/` directory here. The rendered files are committed where they are used.
Several keep the file names of the Silo images they replace, so they are declared seams
in [`contracts/seams.txt`](../../contracts/seams.txt): `assets/icon.png`,
`web/public/apple-touch-icon.png`, `web/public/favicon.ico`,
`web/public/maskable-icon-512.png`, `web/public/web-app-icon-192.png` and
`web/public/web-app-icon-512.png`. Silo's `silo-icon-1024.png` and
`silo-wordmark-sidebar.png` are deleted seams; `web/bloem-brand-plugin.ts` rewrites
`SiloBrand.tsx`'s `/silo-icon-1024.png` and `/silo-wordmark-sidebar.png` to the
`/bloem-` files at build time.

App icons sit on an opaque `#101827` rounded plate (transparent outside the rounded
corners; the maskable icon fills the square) rather than shipping transparent.

| File | Size | Mark weight | Used by |
|---|---|---|---|
| `web/public/bloem-icon-1024.png` | 1024 | detail | the web app's brand component |
| `web/public/web-app-icon-512.png` | 512 | detail | `site.webmanifest` |
| `web/public/web-app-icon-192.png` | 192 | detail | `site.webmanifest` |
| `web/public/apple-touch-icon.png` | 180 | detail | `index.html` |
| `web/public/maskable-icon-512.png` | 512 | detail, glyph at 55% for the maskable safe zone | `site.webmanifest` |
| `web/public/favicon.ico` | 64/48/32/16 | detail / simple / simple / micro | `index.html` |
| `web/public/bloem-wordmark-sidebar.png` | 382×112 | simple (badge) + vector wordmark | the web app's brand component |
| `assets/icon.png` | 256 | detail | repository artwork |
| `assets/bloem-banner.png` | 860×220 | lockup on an opaque panel for READMEs | `.github/README.md` |
| `assets/bloem-social.png` | 1200×630 | Open Graph / link previews | nothing references it yet |
| `web/public/bloem-login-bg.png` | 1920×1080 | login background; motif is offset right, form area left is clear | nothing loads it by itself; upload it to the `login_bg` branding slot |

### Re-rendering

```sh
rsvg-convert -w 512 -h 512 bloem-mark-detail-dark.svg -o out.png
```

Technical pages:

- [Fork provenance](../../FORK.md).
- [Organization domains and branding](../../docs/architecture/bloem-organization-domains.md):
  an organization's own branding on its domains.
- Index: [Bloem documentation](../../docs/bloem/README.md).

The pictures on this page are the brand files themselves, shown as they are.
