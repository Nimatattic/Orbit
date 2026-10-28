#!/usr/bin/env python3
"""Generate assets/banner.svg from the CLI wordmark.

The glyph rows below are the same 5x7 bitmap drawn by `orbit about`
(internal/cli/about.go, logoArt). A Go test (TestAboutLogoMatchesBanner)
re-renders the SVG back into a bitmap and fails if the two ever diverge, so this
script and the Go slice must be edited together.

Style: pixel + terminal. A cool charcoal terminal window, a monochrome bitmap
wordmark, one prompt line and one status line in monospace. The colour palette is
the TUI's own dark theme, so the poster and the screenshots read as one product: a
single blue accent marks the shell prompt, the spinner glyph and the cursor, and
nothing else is coloured. Flat fills only: no gradients, no shadows, no rounded
pixels.

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

# --- canvas and terminal window ------------------------------------------------
W, H = 1200, 360
WIN_X, WIN_Y = 60, 24
WIN_W, WIN_H = 1080, 312
TITLE_H = 38

# Palette mirrors the TUI's dark theme (internal/render/tui/layout.go):
# accent #5FAFFF, ok #5FD75F, warn #FFC857, dim #8A8A8A, border #4A4A4A.
PAGE = "#0B0E13"   # page behind the window, one step darker than the body
BG = "#12161E"     # terminal body
EDGE = "#4A4A4A"   # window frame — the TUI's panel border colour
RULE = "#232A36"   # title-bar separator
DOT = "#3E4A5A"    # window dots
INK = "#E8ECF2"    # the bitmap, cool off-white
DIM = "#8A8A8A"    # secondary text — the TUI's dim
ACCENT = "#5FAFFF" # the TUI's accent, used for the prompt, spinner and cursor

# --- pixel grid ----------------------------------------------------------------
PIXEL, GAP = 20, 4
ADVANCE = PIXEL + GAP

MARK_W = len(GLYPH[0]) * ADVANCE - GAP
MARK_H = len(GLYPH) * ADVANCE - GAP
LEFT = WIN_X + (WIN_W - MARK_W) // 2
TOP = WIN_Y + TITLE_H + 30

pixels = [
    (LEFT + col * ADVANCE, TOP + row * ADVANCE)
    for row, line in enumerate(GLYPH)
    for col, ch in enumerate(line)
    if ch == "#"
]
rects = "\n".join(
    f'    <rect x="{x}" y="{y}" width="{PIXEL}" height="{PIXEL}"/>' for x, y in pixels
)

# --- terminal lines ------------------------------------------------------------
PROMPT = "orbit about"
STATUS = "cloud certificate orchestration"
PROMPT_X = LEFT
PROMPT_Y = TOP + MARK_H + 40
STATUS_Y = PROMPT_Y + 30
FONT = 'font-family="ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"'
CHAR_W = 10.9  # approximate advance of the 18px monospace stack

SVG = f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" role="img" aria-label="Orbit — a pixel wordmark in a terminal window">
  <title>Orbit — cloud certificate orchestration</title>

  <rect width="{W}" height="{H}" fill="{PAGE}"/>

  <!-- terminal window -->
  <rect x="{WIN_X}" y="{WIN_Y}" width="{WIN_W}" height="{WIN_H}" rx="12" fill="{BG}" stroke="{EDGE}" stroke-width="1"/>
  <line x1="{WIN_X + 1}" y1="{WIN_Y + TITLE_H}" x2="{WIN_X + WIN_W - 1}" y2="{WIN_Y + TITLE_H}" stroke="{RULE}" stroke-width="1"/>

  <g fill="{DOT}">
    <circle cx="{WIN_X + 22}" cy="{WIN_Y + 19}" r="4"/>
    <circle cx="{WIN_X + 40}" cy="{WIN_Y + 19}" r="4"/>
    <circle cx="{WIN_X + 58}" cy="{WIN_Y + 19}" r="4"/>
  </g>
  <text x="{WIN_X + WIN_W - 22}" y="{WIN_Y + 24}" text-anchor="end" fill="{DIM}" {FONT} font-size="13" letter-spacing="1">~/orbit</text>

  <!-- wordmark: one ink colour, square pixels -->
  <g id="wordmark" data-pixel="{PIXEL}" data-advance="{ADVANCE}" fill="{INK}">
{rects}
  </g>

  <!-- shell prompt + block cursor -->
  <text x="{PROMPT_X}" y="{PROMPT_Y}" fill="{ACCENT}" {FONT} font-size="18">$</text>
  <text x="{PROMPT_X + int(2 * CHAR_W)}" y="{PROMPT_Y}" fill="{INK}" {FONT} font-size="18">{PROMPT}</text>
  <rect x="{PROMPT_X + int((len(PROMPT) + 3) * CHAR_W) + 6}" y="{PROMPT_Y - 14}" width="10" height="18" fill="{ACCENT}"/>

  <!-- status line: spinner glyph in the accent, the rest dim -->
  <text x="{PROMPT_X}" y="{STATUS_Y}" fill="{ACCENT}" {FONT} font-size="18">✻</text>
  <text x="{PROMPT_X + int(2 * CHAR_W)}" y="{STATUS_Y}" fill="{DIM}" {FONT} font-size="18">{STATUS}</text>
</svg>
'''

target = pathlib.Path(__file__).resolve().parent.parent / "assets" / "banner.svg"
target.parent.mkdir(parents=True, exist_ok=True)
target.write_text(SVG, encoding="utf-8")
print(f"wrote {target} ({len(SVG)} bytes, {len(pixels)} pixels of {PIXEL}px, advance {ADVANCE})")
