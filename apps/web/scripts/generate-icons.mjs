// Generates the PWA icons and the generic Open Graph image into public/ with no dependencies: the FundZim
// mark (rising sun over a horizon, same geometry as src/app/icon.svg) is rasterised with 4×4 supersampling
// and written as PNG with node:zlib. Run after changing the mark:  node scripts/generate-icons.mjs
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { deflateSync } from "node:zlib";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..", "public");

const GREEN = [0x1b, 0x5e, 0x40, 255];
const DEEP = [0x0e, 0x34, 0x23, 255];
const GOLD = [0xe0, 0xa9, 0x3b, 255];
const WHITE = [0xff, 0xff, 0xff, 255];

// ---- geometry in the 32×32 logo space ----
function segDist(px, py, ax, ay, bx, by) {
  const dx = bx - ax;
  const dy = by - ay;
  const t = Math.max(0, Math.min(1, ((px - ax) * dx + (py - ay) * dy) / (dx * dx + dy * dy)));
  return Math.hypot(px - (ax + t * dx), py - (ay + t * dy));
}
function inRoundRect(x, y, w, h, r) {
  if (x < 0 || y < 0 || x > w || y > h) return false;
  const cx = Math.max(r, Math.min(w - r, x));
  const cy = Math.max(r, Math.min(h - r, y));
  return Math.hypot(x - cx, y - cy) <= r;
}
/** Mark colour at a logo-space point (null = not part of the mark), drawn on top of the background. */
function markAt(x, y) {
  // white horizon
  if (segDist(x, y, 5, 24.5, 27, 24.5) <= 1.2) return WHITE;
  // gold sun arc: centre (16, 21.5), r 9, upper half, stroke 3 with round caps
  const d = Math.hypot(x - 16, y - 21.5);
  if ((y <= 21.5 && Math.abs(d - 9) <= 1.5) || Math.hypot(x - 7, y - 21.5) <= 1.5 || Math.hypot(x - 25, y - 21.5) <= 1.5) return GOLD;
  // rays
  if (segDist(x, y, 16, 8, 16, 14) <= 1.2 || segDist(x, y, 10, 10.5, 12.2, 14.8) <= 1.2 || segDist(x, y, 22, 10.5, 19.8, 14.8) <= 1.2) return GOLD;
  return null;
}

/** Renders `w`×`h` RGBA; `sample(u, v)` returns a colour for a pixel-space point. */
function render(w, h, sample) {
  const out = Buffer.alloc(w * h * 4);
  const S = 4;
  for (let py = 0; py < h; py++) {
    for (let px = 0; px < w; px++) {
      const acc = [0, 0, 0, 0];
      for (let sy = 0; sy < S; sy++) {
        for (let sx = 0; sx < S; sx++) {
          const c = sample(px + (sx + 0.5) / S, py + (sy + 0.5) / S);
          if (!c) continue;
          const a = c[3] / 255;
          acc[0] += c[0] * a;
          acc[1] += c[1] * a;
          acc[2] += c[2] * a;
          acc[3] += a;
        }
      }
      const i = (py * w + px) * 4;
      const cov = acc[3];
      out[i] = cov ? Math.round(acc[0] / cov) : 0;
      out[i + 1] = cov ? Math.round(acc[1] / cov) : 0;
      out[i + 2] = cov ? Math.round(acc[2] / cov) : 0;
      out[i + 3] = Math.round((cov / (S * S)) * 255);
    }
  }
  return out;
}

// ---- PNG encoder ----
const CRC = new Uint32Array(256).map((_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});
function crc32(buf) {
  let c = 0xffffffff;
  for (const b of buf) c = CRC[(c ^ b) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}
function chunk(type, data) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length);
  const body = Buffer.concat([Buffer.from(type, "latin1"), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body));
  return Buffer.concat([len, body, crc]);
}
function png(w, h, rgba) {
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 6; // RGBA
  const raw = Buffer.alloc((w * 4 + 1) * h);
  for (let y = 0; y < h; y++) rgba.copy(raw, y * (w * 4 + 1) + 1, y * w * 4, (y + 1) * w * 4);
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", ihdr), chunk("IDAT", deflateSync(raw, { level: 9 })), chunk("IEND", Buffer.alloc(0))]);
}

function write(rel, w, h, rgba) {
  const path = join(ROOT, rel);
  mkdirSync(dirname(path), { recursive: true });
  const data = png(w, h, rgba);
  writeFileSync(path, data);
  console.log(`${rel} ${w}x${h} ${data.length} bytes`);
}

/** Standard icon: rounded green tile (transparent corners) with the mark. */
function icon(size) {
  return render(size, size, (x, y) => {
    const u = (x / size) * 32;
    const v = (y / size) * 32;
    if (!inRoundRect(u, v, 32, 32, 9)) return null;
    return markAt(u, v) ?? GREEN;
  });
}

/** Maskable icon: full-bleed background, mark inside the central 80% safe zone. */
function maskable(size) {
  return render(size, size, (x, y) => {
    const u = ((x / size - 0.1) / 0.8) * 32;
    const v = ((y / size - 0.1) / 0.8) * 32;
    return markAt(u, v) ?? GREEN;
  });
}

/** Generic Open Graph image (1200×630): deep green field, centred tile with the mark, gold base band. */
function ogImage() {
  const W = 1200;
  const H = 630;
  const tile = 300;
  const ox = (W - tile) / 2;
  const oy = (H - tile) / 2 - 20;
  return render(W, H, (x, y) => {
    if (y >= H - 24) return GOLD;
    const u = ((x - ox) / tile) * 32;
    const v = ((y - oy) / tile) * 32;
    if (inRoundRect(u, v, 32, 32, 9)) return markAt(u, v) ?? GREEN;
    return DEEP;
  });
}

write("icons/icon-192.png", 192, 192, icon(192));
write("icons/icon-512.png", 512, 512, icon(512));
write("icons/apple-touch-icon.png", 180, 180, maskable(180));
write("icons/maskable-512.png", 512, 512, maskable(512));
write("og/fundzim-og.png", 1200, 630, ogImage());
