"""Primitive app icon: balanced overlapping translucent triangles."""
import random
from PIL import Image, ImageDraw

S = 2048
OUT = 1024
MARGIN = int(S * 0.085)
RADIUS = int(S * 0.235)

# Warm dark background, matching the app's own palette.
grad = Image.new("RGB", (1, S))
for y in range(S):
    t = y / (S - 1)
    grad.putpixel((0, y), (int(18 + 26 * t), int(17 + 20 * t), int(24 + 30 * t)))
grad = grad.resize((S, S), Image.BILINEAR)

mask = Image.new("L", (S, S), 0)
ImageDraw.Draw(mask).rounded_rectangle(
    [MARGIN, MARGIN, S - MARGIN, S - MARGIN], radius=RADIUS, fill=255
)
icon = Image.new("RGBA", (S, S), (0, 0, 0, 0))
icon.paste(grad, (0, 0), mask)

# On-brand warm palette with a couple of cool accents.
PALETTE = [
    (255, 122, 89),   # accent orange
    (255, 92, 105),   # coral red
    (255, 158, 74),   # amber
    (214, 74, 130),   # magenta
    (122, 92, 255),   # violet
    (61, 220, 151),   # mint
    (255, 226, 196),  # cream
]

N = 5
span = S - 2 * MARGIN
pts = []
for i in range(N + 1):
    for j in range(N + 1):
        pts.append((MARGIN + i * span / N, MARGIN + j * span / N))


def area(t):
    (x1, y1), (x2, y2), (x3, y3) = t
    return abs((x2 - x1) * (y3 - y1) - (x3 - x1) * (y2 - y1)) / 2


random.seed(11)
span_area = span * span
overlay = Image.new("RGBA", (S, S), (0, 0, 0, 0))
od = ImageDraw.Draw(overlay, "RGBA")

drawn = 0
guard = 0
while drawn < 24 and guard < 4000:
    guard += 1
    tri = random.sample(pts, 3)
    a = area(tri)
    # Keep every shape to a modest fraction of the canvas so none dominates.
    if a < span_area * 0.035 or a > span_area * 0.16:
        continue
    colour = random.choice(PALETTE) + (random.randint(120, 190),)
    od.polygon(list(tri), fill=colour)
    drawn += 1

clipped = Image.new("RGBA", (S, S), (0, 0, 0, 0))
clipped.paste(overlay, (0, 0), mask)
icon = Image.alpha_composite(icon, clipped)

edge = Image.new("RGBA", (S, S), (0, 0, 0, 0))
ImageDraw.Draw(edge).rounded_rectangle(
    [MARGIN, MARGIN, S - MARGIN, S - MARGIN], radius=RADIUS,
    outline=(255, 255, 255, 30), width=max(1, S // 340),
)
icon = Image.alpha_composite(icon, edge)

icon.resize((OUT, OUT), Image.LANCZOS).save("/tmp/primitive-icon2.png")
print("triangles drawn:", drawn)
