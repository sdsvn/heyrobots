#!/usr/bin/env python3
"""Draws Bit's app icon and renders every platform's PNGs.

Writes bit.svg (the source glyph) next to this file, then renders the web,
macOS and Android icons with rsvg-convert. Run from anywhere:

    python3 app/assets/icon/render.py
"""
import os
import subprocess
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
APP = os.path.normpath(os.path.join(HERE, '..', '..'))

INK = '#2A2540'
CARD = '#FFFFFF'
BLUSH = '#FFC9D4'
PEACH = '#FFB38A'


def face(bold=False):
    """Bit on the app's 80x80 grid; bold thickens it for tiny sizes."""
    w = 4.4 if bold else 3.2
    eye_w, eye_h = (11, 14) if bold else (9.5, 12.5)
    cheeks = '' if bold else (
        f'<circle cx="23" cy="49" r="4.2" fill="{BLUSH}"/><circle cx="57" cy="49" r="4.2" fill="{BLUSH}"/>')
    shine = '' if bold else (
        f'<circle cx="33" cy="35.5" r="1.7" fill="{CARD}"/><circle cx="51" cy="35.5" r="1.7" fill="{CARD}"/>')
    return f'''
    <line x1="40" y1="18" x2="40" y2="9.5" stroke="{INK}" stroke-width="{w}" stroke-linecap="round"/>
    <circle cx="40" cy="8" r="{6 if bold else 5.2}" fill="{PEACH}" stroke="{INK}" stroke-width="{w * .7:.2f}"/>
    <rect x="14" y="18" width="52" height="44" rx="18" fill="{CARD}" stroke="{INK}" stroke-width="{w}"/>
    <ellipse cx="31" cy="38" rx="{eye_w / 2}" ry="{eye_h / 2}" fill="{INK}"/>
    <ellipse cx="49" cy="38" rx="{eye_w / 2}" ry="{eye_h / 2}" fill="{INK}"/>
    {shine}{cheeks}
    <path d="M34.5 48.5 Q40 54.5 45.5 48.5" fill="none" stroke="{INK}" stroke-width="{w * .95:.2f}" stroke-linecap="round"/>'''


def svg(size=1024, tile=None, radius=0, glyph=0.62, bold=False, shadow=False):
    """A square icon: a violet tile (inset by `tile` px on each side, or full
    bleed) with Bit centred, its 58-unit-tall body `glyph` of the tile high."""
    inset = tile or 0
    t = size - 2 * inset
    k = glyph * t / 58  # Bit spans y 3..62 on its grid
    # Centre Bit's body (x 14..66, y 3..62) on the tile.
    tx = inset + t / 2 - 40 * k
    ty = inset + t / 2 - 32.5 * k
    drop = (f'<rect x="{inset}" y="{inset + t * .012:.1f}" width="{t}" height="{t}" rx="{radius}" fill="#000" '
            f'opacity=".28" filter="url(#blur)"/>') if shadow else ''
    return f'''<svg xmlns="http://www.w3.org/2000/svg" width="{size}" height="{size}" viewBox="0 0 {size} {size}">
  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#6E5FF2"/>
      <stop offset="1" stop-color="#5040D6"/>
    </linearGradient>
    <filter id="blur" x="-10%" y="-10%" width="120%" height="120%"><feGaussianBlur stdDeviation="{t * .012:.1f}"/></filter>
  </defs>
  {drop}
  <rect x="{inset}" y="{inset}" width="{t}" height="{t}" rx="{radius}" fill="url(#bg)"/>
  <g transform="translate({tx:.2f} {ty:.2f}) scale({k:.4f})">{face(bold)}
  </g>
</svg>
'''


def render(src, out, px):
    os.makedirs(os.path.dirname(out), exist_ok=True)
    subprocess.run(['rsvg-convert', '-w', str(px), '-h', str(px), '-o', out, src], check=True)


def main():
    source = svg(radius=230)
    with open(os.path.join(HERE, 'bit.svg'), 'w') as f:
        f.write(source)
    with open(os.path.join(APP, 'web', 'favicon.svg'), 'w') as f:
        f.write(svg(radius=230, glyph=.74))

    tmp = tempfile.mkdtemp()

    def variant(name, **kw):
        path = os.path.join(tmp, name + '.svg')
        with open(path, 'w') as f:
            f.write(svg(**kw))
        return path

    rounded = variant('rounded', radius=230)
    bleed = variant('bleed')
    maskable = variant('maskable', glyph=.5)
    small = variant('small', radius=200, glyph=.8, bold=True)
    mac = variant('mac', tile=100, radius=185, shadow=True)
    mac_small = variant('mac_small', tile=100, radius=185, glyph=.74, bold=True)

    web = os.path.join(APP, 'web')
    render(small, os.path.join(web, 'favicon.png'), 32)
    render(bleed, os.path.join(web, 'icons', 'Icon-192.png'), 192)
    render(bleed, os.path.join(web, 'icons', 'Icon-512.png'), 512)
    render(maskable, os.path.join(web, 'icons', 'Icon-maskable-192.png'), 192)
    render(maskable, os.path.join(web, 'icons', 'Icon-maskable-512.png'), 512)

    icons = os.path.join(APP, 'macos', 'Runner', 'Assets.xcassets', 'AppIcon.appiconset')
    for px in (16, 32, 64, 128, 256, 512, 1024):
        render(mac_small if px <= 32 else mac, os.path.join(icons, f'app_icon_{px}.png'), px)

    res = os.path.join(APP, 'android', 'app', 'src', 'main', 'res')
    for dpi, px in (('mdpi', 48), ('hdpi', 72), ('xhdpi', 96), ('xxhdpi', 144), ('xxxhdpi', 192)):
        render(rounded, os.path.join(res, f'mipmap-{dpi}', 'ic_launcher.png'), px)


if __name__ == '__main__':
    main()
