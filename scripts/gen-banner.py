#!/usr/bin/env python3
"""Generate assets/banner.svg from the CLI wordmark.

The glyph rows below are the same 5x7 bitmap drawn by `orbit about`
(internal/cli/about.go, logoArt). A Go test (TestAboutLogoMatchesBanner)
re-renders the SVG back into a bitmap and fails if the two ever diverge, so this
script and the Go slice must be edited together.

The poster is deliberately plain: flat background, one ink colour, square pixels,
no gradients, no shadows, no glow. It should read like a bitmap, not an
illustration.

    python3 scripts/gen-banner.py          # rewrite assets/banner.svg
"""
import pathlib

GLYPH = [
    " ###  ####  ####  ##### #####",
    "#   # #   # #   #   #     #  ",
    "#   # #   # #   #   #     #  ",
    "#   # ####  ####    #     #  ",
    "#   # # #   #   #   #     #  ",
    "#   # #  #  #   #   #     #  ",
    " ###  #   # ####  #####   #  ",
]

W, H = 1200, 300
PIXEL, GAP = 24, 5
ADVANCE = PIXEL + GAP

MARK_W = len(GLYPH[0]) * ADVANCE - GAP
MARK_H = len(GLYPH) * ADVANCE - GAP
LEFT = (W - MARK_W) // 2
TOP = 30

BG = "#0B0F16"       # flat, near-black
INK = "#E6EAF2"      # single ink colour for every pixel
RULE = "#222A36"     # hairline separator
TEXT = "#78839A"     # muted caption
EDGE = "#1B212B"     # 1px frame so the dark block reads as intentional

pixels = [
    (LEFT + col * ADVANCE, TOP + row * ADVANCE)
    for row, line in enumerate(GLYPH)
    for col, ch in enumerate(line)
    if ch == "#"
]

# Square pixels with a visible gap: the grid is the point, so the cells must read
# as separate dots rather than merging into slabs.
rects = "\n".join(
    f'    <rect x="{x}" y="{y}" width="{PIXEL}" height="{PIXEL}"/>'
    for x, y in pixels
)

RULE_Y = TOP + MARK_H + 20
RULE_X1 = W // 2 - 220
RULE_X2 = W // 2 + 220
TEXT_Y = RULE_Y + 29

SVG = f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" role="img" aria-label="Orbit — cloud certificate orchestration">
  <title>Orbit — cloud certificate orchestration</title>

  <rect width="{W}" height="{H}" fill="{BG}"/>
  <rect x="0.5" y="0.5" width="{W - 1}" height="{H - 1}" fill="none" stroke="{EDGE}" stroke-width="1"/>

  <g id="wordmark" fill="{INK}">
{rects}
  </g>

  <rect x="{RULE_X1}" y="{RULE_Y}" width="{RULE_X2 - RULE_X1}" height="1" fill="{RULE}"/>

  <text x="{W / 2:.0f}" y="{TEXT_Y}" text-anchor="middle" fill="{TEXT}"
        font-family="ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"
        font-size="17">cloud certificate orchestration</text>
</svg>
'''

target = pathlib.Path(__file__).resolve().parent.parent / "assets" / "banner.svg"
target.parent.mkdir(parents=True, exist_ok=True)
target.write_text(SVG, encoding="utf-8")
print(f"wrote {target} ({len(SVG)} bytes, {len(pixels)} pixels)")
