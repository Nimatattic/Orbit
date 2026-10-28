#!/usr/bin/env python3
"""Generate assets/banner.svg from the CLI wordmark.

The glyph rows below are the same 5x7 bitmap drawn by `orbit about`
(internal/cli/about.go, logoArt). A Go test (TestAboutLogoMatchesBanner)
re-renders the SVG back into a bitmap and fails if the two ever diverge, so this
script and the Go slice must be edited together.

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

W, H = 1200, 340
PIXEL, GAP = 26, 3
ADVANCE = PIXEL + GAP

MARK_W = len(GLYPH[0]) * ADVANCE - GAP
MARK_H = len(GLYPH) * ADVANCE - GAP
LEFT = (W - MARK_W) // 2
TOP = 56

pixels = [
    (LEFT + col * ADVANCE, TOP + row * ADVANCE)
    for row, line in enumerate(GLYPH)
    for col, ch in enumerate(line)
    if ch == "#"
]

# Satellite with a motion trail, parked in the right-hand margin so it never
# touches the letters (verified by the layout test).
SAT = (1086, 40, 14)
TRAIL = [(1066, 50, 9, 0.72), (1051, 58, 6.5, 0.5), (1040, 64, 4.5, 0.3)]

def rect(x, y, size, fill, rx, opacity=None):
    extra = f' fill-opacity="{opacity}"' if opacity is not None else ""
    return (
        f'  <rect x="{x - size / 2:.1f}" y="{y - size / 2:.1f}" '
        f'width="{size}" height="{size}" rx="{rx}" fill="{fill}"{extra}/>'
    )

shadow = "\n".join(
    f'    <rect x="{x}" y="{y + 7}" width="{PIXEL}" height="{PIXEL}" rx="2"/>'
    for x, y in pixels
)
ink = "\n".join(
    f'    <rect x="{x}" y="{y}" width="{PIXEL}" height="{PIXEL}" rx="2"/>'
    for x, y in pixels
)
trail = "\n".join(rect(x, y, size, "#FFC46B", 1.5, op) for x, y, size, op in TRAIL)
satellite = rect(*SAT, "#FFD79A", 2)

SVG = f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" role="img" aria-label="Orbit — cloud certificate orchestration">
  <title>Orbit — cloud certificate orchestration</title>
  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#060A14"/>
      <stop offset="0.55" stop-color="#0A1224"/>
      <stop offset="1" stop-color="#0D1830"/>
    </linearGradient>
    <linearGradient id="ink" x1="0" y1="0" x2="0.35" y2="1">
      <stop offset="0" stop-color="#8DF3FF"/>
      <stop offset="0.5" stop-color="#5BC8FF"/>
      <stop offset="1" stop-color="#4C7DFF"/>
    </linearGradient>
    <radialGradient id="glow" cx="0.5" cy="0.45" r="0.55">
      <stop offset="0" stop-color="#4C7DFF" stop-opacity="0.20"/>
      <stop offset="1" stop-color="#4C7DFF" stop-opacity="0"/>
    </radialGradient>
    <pattern id="dots" width="24" height="24" patternUnits="userSpaceOnUse">
      <rect width="2" height="2" fill="#FFFFFF" fill-opacity="0.05"/>
    </pattern>
  </defs>

  <rect width="{W}" height="{H}" rx="18" fill="url(#bg)"/>
  <rect width="{W}" height="{H}" rx="18" fill="url(#dots)"/>
  <rect x="1" y="1" width="{W - 2}" height="{H - 2}" rx="17" fill="none" stroke="#1C2A47" stroke-width="2"/>
  <rect width="{W}" height="{H}" rx="18" fill="url(#glow)"/>

{trail}
{satellite}

  <g fill="#050912" fill-opacity="0.7">
{shadow}
  </g>

  <g id="wordmark" fill="url(#ink)">
{ink}
  </g>

  <text x="{W / 2:.0f}" y="306" text-anchor="middle" fill="#93A7CC"
        font-family="ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"
        font-size="21" letter-spacing="8">CLOUD CERTIFICATE ORCHESTRATION</text>
  <text x="{W / 2:.0f}" y="332" text-anchor="middle" fill="#5B8DFF" fill-opacity="0.85"
        font-family="ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"
        font-size="12.5" letter-spacing="3.5">TENCENT CLOUD · CDN · EDGEONE</text>
</svg>
'''

target = pathlib.Path(__file__).resolve().parent.parent / "assets" / "banner.svg"
target.parent.mkdir(parents=True, exist_ok=True)
target.write_text(SVG, encoding="utf-8")
print(f"wrote {target} ({len(SVG)} bytes, {len(pixels)} pixels)")
