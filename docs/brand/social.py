"""Renders the GitHub social preview (1280x640): the mark, the name, a one-line
pitch and the main checks, on the brand gradient.

    python3 docs/brand/social.py docs/brand/social.png      (needs Pillow; macOS fonts)

Upload it in the repository's Settings → General → Social preview.
"""
import os
import sys

from PIL import Image, ImageDraw, ImageFont

sys.path.insert(0, os.path.dirname(__file__))
from logo import MINT, PLATE, draw_mark, gradient  # noqa: E402

out = sys.argv[1] if len(sys.argv) > 1 else "social.png"
X = 2                                       # draw at 2x, downsample
W, H = 1280 * X, 640 * X


def font(weight, size):
    f = ImageFont.truetype("/System/Library/Fonts/SFNS.ttf", size * X)
    f.set_variation_by_name(weight)
    return f


mono = ImageFont.truetype("/System/Library/Fonts/SFNSMono.ttf", 26 * X)

img = gradient(W, H)
img = draw_mark(img, W * 0.215, H * 0.5, H * 0.80)
d = ImageDraw.Draw(img)

left = W * 0.415
d.text((left, H * 0.20), "cometbft-mcp", font=font("Bold", 84), fill=(255, 255, 255))
for i, line in enumerate(["Read-only MCP server for", "CometBFT validator operators"]):
    d.text((left, H * 0.40 + i * 46 * X), line, font=font("Regular", 38), fill=(226, 232, 240))

# Pills for the main checks.
x, y = left, H * 0.62
pill = font("Semibold", 24)
for label in ["signing liveness", "jail risk", "halt diagnosis", "upgrade readiness"]:
    tw = d.textlength(label, font=pill)
    w, h = tw + 32 * X, 46 * X
    if x + w > W - 60 * X:
        x, y = left, y + h + 12 * X
    d.rounded_rectangle([x, y, x + w, y + h], radius=h / 2, fill=PLATE + (255,))
    d.text((x + 16 * X, y + h / 2), label, font=pill, fill=MINT, anchor="lm")
    x += w + 12 * X

d.text((left, H * 0.88), "keyless · GET-only · node IPs masked", font=mono, fill=(203, 213, 225))

img.convert("RGB").resize((1280, 640), Image.LANCZOS).save(out, optimize=True)
print("wrote", out)
