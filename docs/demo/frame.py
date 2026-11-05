"""Wraps the agg-rendered GIF in a macOS-style window on a gradient backdrop.

    python3 docs/demo/frame.py /tmp/demo-raw.gif docs/demo.gif   (needs Pillow)
"""
import sys
from PIL import Image, ImageDraw, ImageFont, ImageSequence

src, dst = sys.argv[1], sys.argv[2]
BG = (23, 27, 33)                  # github-dark terminal background
BAR = (36, 41, 49)
MARGIN, TITLE_H, RADIUS, PAD = 36, 34, 12, 14
TOP, BOTTOM = (49, 46, 129), (17, 94, 89)   # indigo -> teal

raw = Image.open(src)
w, h = raw.size
win_w, win_h = w + 2 * PAD, h + TITLE_H + PAD
W, H = win_w + 2 * MARGIN, win_h + 2 * MARGIN

backdrop = Image.new("RGB", (W, H))
draw = ImageDraw.Draw(backdrop)
for y in range(H):
    t = y / (H - 1)
    draw.line([(0, y), (W, y)], fill=tuple(round(a + (b - a) * t) for a, b in zip(TOP, BOTTOM)))

window = Image.new("RGB", (win_w, win_h), BG)
wd = ImageDraw.Draw(window)
wd.rectangle([0, 0, win_w, TITLE_H], fill=BAR)
for i, c in enumerate([(255, 95, 87), (254, 188, 46), (40, 200, 64)]):
    cx, cy = 20 + i * 20, TITLE_H // 2
    wd.ellipse([cx - 6, cy - 6, cx + 6, cy + 6], fill=c)
try:
    font = ImageFont.truetype("/System/Library/Fonts/Menlo.ttc", 13)
except OSError:
    font = ImageFont.load_default()
title = "cometbft-mcp — demo"
tw = wd.textlength(title, font=font)
wd.text(((win_w - tw) / 2, TITLE_H / 2), title, fill=(139, 148, 158), font=font, anchor="lm")

mask = Image.new("L", (win_w, win_h), 0)
ImageDraw.Draw(mask).rounded_rectangle([0, 0, win_w - 1, win_h - 1], RADIUS, fill=255)
shadow = Image.new("RGBA", (W, H), (0, 0, 0, 0))

frames, durations = [], []
for frame in ImageSequence.Iterator(raw):
    canvas = backdrop.copy()
    win = window.copy()
    win.paste(frame.convert("RGB"), (PAD, TITLE_H))
    canvas.paste(win, (MARGIN, MARGIN), mask)
    frames.append(canvas.quantize(colors=256, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE))
    durations.append(frame.info.get("duration", 50))

frames[0].save(dst, save_all=True, append_images=frames[1:], duration=durations, loop=0, optimize=True, disposal=1)
print(f"{dst}: {W}x{H}, {len(frames)} frames, {sum(durations) / 1000:.1f}s")
