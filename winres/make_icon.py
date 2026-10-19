# Generates winres/icon.png (256px) and winres/icon16.png. Usage: python3 winres/make_icon.py winres
import sys
from PIL import Image, ImageDraw, ImageFont

out = sys.argv[1]
S = 1024

def gradient(size, top, bottom):
    g = Image.new("RGBA", (size, size))
    d = ImageDraw.Draw(g)
    for y in range(size):
        t = y / (size - 1)
        d.line([(0, y), (size, y)], fill=tuple(int(top[i] + (bottom[i] - top[i]) * t) for i in range(3)) + (255,))
    return g

def draw_icon(size, with_text):
    k = size / S
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    mask = Image.new("L", (size, size), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, size - 1, size - 1], radius=int(220 * k), fill=255)
    img.paste(gradient(size, (38, 166, 154), (11, 79, 86)), (0, 0), mask)
    d = ImageDraw.Draw(img)
    white = (255, 255, 255, 255)
    gold = (247, 214, 140, 255)

    cy = 310 if with_text else 512
    sc = 1.0 if with_text else 1.25
    def P(x, y):  # point relative to speaker centre
        return (int((x * sc + (180 if with_text else 120)) * k), int((y * sc + cy) * k))

    # speaker body + cone
    d.rounded_rectangle([P(0, -80), P(110, 80)], radius=int(18 * k * sc), fill=white)
    d.polygon([P(100, -80), P(270, -220), P(270, 220), P(100, 80)], fill=white)

    # sound waves
    waves = [(150, 30), (250, 30), (350, 30)] if with_text else [(170, 46), (290, 46)]
    for r, w in waves:
        cx, cyy = P(300, 0)
        rr = int(r * k * sc)
        d.arc([cx - rr, cyy - rr, cx + rr, cyy + rr], start=-48, end=48,
              fill=gold if r == waves[-1][0] else white, width=max(2, int(w * k * sc)))

    if with_text:
        font = ImageFont.truetype("/System/Library/Fonts/Avenir Next.ttc", int(250 * k), index=2)
        text = "SPA"
        tw = d.textlength(text, font=font)
        d.text(((size - tw) / 2, 560 * k), text, font=font, fill=white)
        # small lotus under the text
        lotus = Image.new("RGBA", (size, size), (0, 0, 0, 0))
        for angle, w, h in [(0, 30, 72), (40, 26, 60), (-40, 26, 60), (75, 22, 48), (-75, 22, 48)]:
            petal = Image.new("RGBA", (size, size), (0, 0, 0, 0))
            cx, base = size / 2, 965 * k
            ImageDraw.Draw(petal).ellipse([cx - w * k, base - 2 * h * k, cx + w * k, base], fill=gold)
            lotus.alpha_composite(petal.rotate(angle, center=(cx, base), resample=Image.BICUBIC))
        img.alpha_composite(lotus.resize((size, size)))
    return img

big = draw_icon(S, True).resize((256, 256), Image.LANCZOS)
big.save(out + "/icon.png")
small = draw_icon(S, False).resize((16, 16), Image.LANCZOS)
small.save(out + "/icon16.png")
