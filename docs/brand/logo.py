"""Renders the cometbft-mcp icon: a validator set drawn as a ring of nodes,
one of them down (coral), with a pulse line through the middle — the
question the server answers: who is not signing? It sits on a dark disc over
the indigo-to-teal gradient used by the README demo.

    python3 docs/brand/logo.py docs/brand/logo.png docs/brand/mark.png      (needs Pillow)

draw_mark() is shared with social.py.
"""
import math
import sys

from PIL import Image, ImageDraw, ImageFilter

TOP_LEFT, BOTTOM_RIGHT = (67, 56, 202), (13, 148, 136)
PLATE = (15, 20, 36)
RING = (148, 163, 184)
MINT, CORAL = (110, 231, 183), (251, 113, 133)
VALIDATORS, DOWN = 8, 1                     # ring size, index of the missing validator


def lerp(a, b, t):
    return tuple(round(x + (y - x) * t) for x, y in zip(a, b))


def gradient(w, h):
    """Diagonal brand gradient, as RGBA."""
    bg = Image.new("RGB", (w, h))
    px = bg.load()
    for y in range(h):
        for x in range(w):
            px[x, y] = lerp(TOP_LEFT, BOTTOM_RIGHT, (x + y) / (w + h - 2))
    return bg.convert("RGBA")


def soft(img, draw, blur):
    """Composite a blurred shape (shadow or glow) under what comes next."""
    layer = Image.new("RGBA", img.size, (0, 0, 0, 0))
    draw(ImageDraw.Draw(layer))
    return Image.alpha_composite(img, layer.filter(ImageFilter.GaussianBlur(blur)))


def draw_mark(img, cx, cy, k):
    """Draws the plate, validator ring and pulse centered at (cx, cy); k is
    the size of the square the mark was designed for (its plate is 0.8 k)."""
    P = k * 0.40
    img = soft(img, lambda d: d.ellipse([cx - P, cy - P + k * 0.02, cx + P, cy + P + k * 0.02], fill=(0, 0, 0, 120)), k * 0.025)
    ImageDraw.Draw(img).ellipse([cx - P, cy - P, cx + P, cy + P], fill=PLATE)

    R, r = k * 0.29, k * 0.043
    ImageDraw.Draw(img).ellipse([cx - R, cy - R, cx + R, cy + R], outline=RING, width=round(k * 0.014))
    nodes = [(cx + R * math.cos(-math.pi / 2 + 2 * math.pi * i / VALIDATORS),
              cy + R * math.sin(-math.pi / 2 + 2 * math.pi * i / VALIDATORS)) for i in range(VALIDATORS)]
    dx, dy = nodes[DOWN]
    img = soft(img, lambda d: d.ellipse([dx - r * 2.2, dy - r * 2.2, dx + r * 2.2, dy + r * 2.2], fill=CORAL + (140,)), k * 0.018)
    d = ImageDraw.Draw(img)
    for i, (x, y) in enumerate(nodes):
        d.ellipse([x - r * 1.45, y - r * 1.45, x + r * 1.45, y + r * 1.45], fill=PLATE)   # gap in the ring
        d.ellipse([x - r, y - r, x + r, y + r], fill=CORAL if i == DOWN else MINT)

    u, w = k * 0.045, k * 0.032
    pulse = [(cx - k * 0.19, cy), (cx - u * 1.5, cy), (cx - u * 0.8, cy - u * 2.6), (cx + u * 0.2, cy + u * 2.3),
             (cx + u * 1.0, cy - u * 1.1), (cx + u * 1.6, cy), (cx + k * 0.19, cy)]
    img = soft(img, lambda d: d.line(pulse, fill=MINT + (110,), width=round(k * 0.05), joint="curve"), k * 0.014)
    d = ImageDraw.Draw(img)
    d.line(pulse, fill=MINT, width=round(w), joint="curve")
    for x, y in (pulse[0], pulse[-1]):
        d.ellipse([x - w / 2, y - w / 2, x + w / 2, y + w / 2], fill=MINT)
    return img


if __name__ == "__main__":
    # logo.py [square.png] [mark.png]: the square icon (directories, avatars)
    # and the round mark on a transparent background (README header).
    out = sys.argv[1] if len(sys.argv) > 1 else "logo.png"
    S = 2048                                # draw large, downsample for smooth edges
    img = draw_mark(gradient(S, S), S / 2, S / 2, S)
    img.convert("RGB").resize((512, 512), Image.LANCZOS).save(out, optimize=True)
    print("wrote", out)
    if len(sys.argv) > 2:
        mark = draw_mark(Image.new("RGBA", (S, S), (0, 0, 0, 0)), S / 2, S / 2, S)
        # A thin teal rim keeps the disc's edge visible on dark backgrounds.
        P, rim = S * 0.40, S * 0.012
        ImageDraw.Draw(mark).ellipse([S / 2 - P, S / 2 - P, S / 2 + P, S / 2 + P], outline=(45, 212, 191, 170), width=round(rim))
        m = S * 0.07                        # trim to the plate plus its shadow
        mark = mark.crop((round(m), round(m), round(S - m), round(S - m + S * 0.02)))
        mark.resize((512, round(512 * mark.height / mark.width)), Image.LANCZOS).save(sys.argv[2], optimize=True)
        print("wrote", sys.argv[2])
