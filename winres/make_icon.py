# Generates winres/icon.ico (16-256px) and winres/icon.png (256px preview). Usage: python3 winres/make_icon.py winres
import io
import struct
import sys

from PIL import Image, ImageDraw, ImageFont

out = sys.argv[1]

def bmp_entry(img):
    """32-bit BMP icon image: header, BGRA rows bottom-up, then the 1-bit AND mask
    (required by Windows even with alpha; without it Explorer shows the default icon)."""
    w, h = img.size
    px = img.convert("RGBA").load()
    header = struct.pack("<IiiHHIIiiII", 40, w, h * 2, 1, 32, 0, 0, 0, 0, 0, 0)
    xor = bytearray()
    for y in range(h - 1, -1, -1):
        for x in range(w):
            r, g, b, a = px[x, y]
            xor += bytes((b, g, r, a))
    row = ((w + 31) // 32) * 4
    mask = bytearray()
    for y in range(h - 1, -1, -1):
        bits = bytearray(row)
        for x in range(w):
            if px[x, y][3] == 0:
                bits[x // 8] |= 0x80 >> (x % 8)
        mask += bits
    return header + bytes(xor) + bytes(mask)


def write_ico(path, images):
    """256px as PNG (standard for Vista+), smaller sizes as BMP."""
    blobs = []
    for img in images:
        if img.size[0] >= 256:
            buf = io.BytesIO()
            img.save(buf, "PNG")
            blobs.append(buf.getvalue())
        else:
            blobs.append(bmp_entry(img))
    data = struct.pack("<HHH", 0, 1, len(images))
    offset = 6 + 16 * len(images)
    for img, blob in zip(images, blobs):
        w = img.size[0]
        data += struct.pack("<BBBBHHII", w % 256, w % 256, 0, 0, 1, 32, len(blob), offset)
        offset += len(blob)
    with open(path, "wb") as f:
        f.write(data + b"".join(blobs))


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

# Small sizes show the speaker only; "SPA" and the lotus are unreadable below 48px.
full, simple = draw_icon(S, True), draw_icon(S, False)
sizes = [256, 128, 64, 48, 32, 24, 16]
images = [(full if n >= 48 else simple).resize((n, n), Image.LANCZOS) for n in sizes]
images[0].save(out + "/icon.png")
write_ico(out + "/icon.ico", images)
