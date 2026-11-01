#!/usr/bin/env python3
"""Draw Shiori's logo, 栞 ("bookmark"), from Noto Sans CJK JP (SIL OFL 1.1).

    uv run --with fonttools --with resvg-py tools/logo/make_logo.py [font.ttc]

Writes assets/logo.svg (the app icon), assets/icon.png (1024x1024, what
mygo build packages and the window shows) and assets/mark.svg (the bare
glyph in currentColor, 24x24, for the sidebar). The glyph is outlines, so
the SVGs need no font.
"""
import os
import sys

from fontTools.pens.boundsPen import BoundsPen
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools import subset
from fontTools.ttLib import TTCollection
from fontTools.varLib import instancer

CHAR = "栞"
ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..")
FONTS = [
    "/usr/share/fonts/opentype/noto-sans-cjk/NotoSansCJK-VF.otf.ttc",
    os.path.expanduser("~/.nix-profile/share/fonts/opentype/noto-cjk/NotoSansCJK-VF.otf.ttc"),
]


def japanese_face(path):
    for font in TTCollection(path).fonts:
        if font["name"].getDebugName(1) == "Noto Sans CJK JP":
            return font
    raise SystemExit(f"{path}: no Noto Sans CJK JP")


def just_the_glyph(font):
    """The font cut down to the one glyph: instancing all of CJK takes minutes."""
    sub = subset.Subsetter(subset.Options(name_IDs=["*"], notdef_outline=False, layout_features=[]))
    sub.populate(text=CHAR)
    sub.subset(font)
    return font


def glyph_path(font, weight, box):
    """The glyph's outline as SVG path data, fit and centered in box (x, y, size)."""
    if "fvar" in font:
        font = instancer.instantiateVariableFont(font, {"wght": weight})
    name = font.getBestCmap()[ord(CHAR)]
    gs = font.getGlyphSet()
    bp = BoundsPen(gs)
    gs[name].draw(bp)
    x0, y0, x1, y1 = bp.bounds
    bx, by, size = box
    scale = size / max(x1 - x0, y1 - y0)
    # Font units are y-up; SVG is y-down.
    dx = bx + (size - (x1 - x0) * scale) / 2 - x0 * scale
    dy = by + (size - (y1 - y0) * scale) / 2 + y1 * scale
    pen = SVGPathPen(gs, ntos=lambda v: f"{v:.2f}".rstrip("0").rstrip("."))
    gs[name].draw(TransformPen(pen, (scale, 0, 0, -scale, dx, dy)))
    return pen.getCommands()


def main():
    path = sys.argv[1] if len(sys.argv) > 1 else next((p for p in FONTS if os.path.exists(p)), None)
    if not path:
        raise SystemExit("Noto Sans CJK not found; pass the .ttc")
    res = os.path.join(ROOT, "assets")
    os.makedirs(res, exist_ok=True)

    icon_glyph = glyph_path(just_the_glyph(japanese_face(path)), 700, (282, 314, 460))
    # A squircle-ish tile of ink, a vermilion ribbon hanging from its top
    # edge as a bookmark's, and the character in paper white.
    logo = f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024">
  <defs>
    <linearGradient id="ink" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#2e2943"/>
      <stop offset="1" stop-color="#17141f"/>
    </linearGradient>
    <linearGradient id="ribbon" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#c8402d"/>
      <stop offset="1" stop-color="#ec6a4f"/>
    </linearGradient>
    <clipPath id="tile"><rect x="64" y="64" width="896" height="896" rx="200"/></clipPath>
  </defs>
  <rect x="64" y="64" width="896" height="896" rx="200" fill="url(#ink)"/>
  <g clip-path="url(#tile)">
    <path d="M682 40h88v214l-44-36l-44 36z" fill="url(#ribbon)"/>
  </g>
  <path d="{icon_glyph}" fill="#f5eedf"/>
</svg>
"""
    with open(os.path.join(res, "logo.svg"), "w") as f:
        f.write(logo)

    mark = glyph_path(just_the_glyph(japanese_face(path)), 600, (1, 1, 22))
    with open(os.path.join(res, "mark.svg"), "w") as f:
        f.write(f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="{mark}" fill="currentColor"/></svg>\n')

    import resvg_py
    with open(os.path.join(res, "icon.png"), "wb") as f:
        f.write(bytes(resvg_py.svg_to_bytes(svg_string=logo, width=1024, height=1024)))
    print("wrote assets/logo.svg, assets/icon.png, assets/mark.svg")


if __name__ == "__main__":
    main()
