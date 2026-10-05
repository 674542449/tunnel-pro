"""Generate tunnelX SVG, PNG and multi-resolution Windows icons from one geometry.

Requires Pillow. No network, fonts or third-party artwork are used.
"""
from pathlib import Path
from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parents[1]
TOP, BOTTOM = (32, 73, 117), (17, 34, 54)
PATHS = [((76, 72, 180, 184), '#f5fbff'),
         ((180, 72, 146, 109), '#62e4d1'),
         ((110, 147, 76, 184), '#62e4d1')]
STROKE = 24


def raster(size):
    scale = size * 4 / 256
    side = size * 4
    image = Image.new('RGBA', (side, side))
    draw = ImageDraw.Draw(image)
    for y in range(side):
        ratio = max(0, min(1, (y / scale - 16) / 224))
        color = tuple(round(a + (b - a) * ratio) for a, b in zip(TOP, BOTTOM))
        draw.line((0, y, side, y), fill=color + (255,))
    mask = Image.new('L', (side, side))
    ImageDraw.Draw(mask).rounded_rectangle(
        tuple(round(v * scale) for v in (16, 16, 240, 240)),
        radius=round(54 * scale), fill=255)
    image.putalpha(mask)
    radius = STROKE * scale / 2
    for coords, color in PATHS:
        points = tuple(round(v * scale) for v in coords)
        draw.line(points, fill=color, width=round(STROKE * scale))
        for x, y in (points[:2], points[2:]):
            draw.ellipse((round(x-radius), round(y-radius), round(x+radius), round(y+radius)), fill=color)
    return image.resize((size, size), Image.Resampling.LANCZOS)


def main():
    paths = '\n'.join(f'  <path d="M{x1} {y1} L{x2} {y2}" stroke="{color}"/>'
                      for (x1, y1, x2, y2), color in PATHS)
    svg = f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256" fill="none">
  <title>tunnelX</title>
  <defs><linearGradient id="tile" x1="128" y1="16" x2="128" y2="240" gradientUnits="userSpaceOnUse"><stop stop-color="#204975"/><stop offset="1" stop-color="#112236"/></linearGradient></defs>
  <rect x="16" y="16" width="224" height="224" rx="54" fill="url(#tile)"/>
  <g stroke-width="{STROKE}" stroke-linecap="round">
{paths}
  </g>
</svg>
'''
    (ROOT / 'desktop/ui/logo.svg').write_text(svg, encoding='utf-8', newline='\n')
    raster(512).save(ROOT / 'desktop/build/appicon.png')
    sizes = (16, 20, 24, 32, 40, 48, 64, 128, 256)
    images = [raster(size) for size in sizes]
    icon = ROOT / 'desktop/build/windows/icon.ico'
    images[-1].save(icon, format='ICO', sizes=[(n, n) for n in sizes], append_images=images[:-1])
    (ROOT / 'desktop/build/windows/tray.ico').write_bytes(icon.read_bytes())
    print('Generated tunnelX SVG, 512px PNG and nine-size EXE/tray ICO.')


if __name__ == '__main__':
    main()
