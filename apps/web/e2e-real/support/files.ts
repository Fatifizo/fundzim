import zlib from "node:zlib";

/** Synthetic upload content built with Node built-ins only (no fixtures on disk). */

function chunk(type: string, data: Buffer): Buffer {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length);
  const td = Buffer.concat([Buffer.from(type, "ascii"), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(zlib.crc32(td) >>> 0);
  return Buffer.concat([len, td, crc]);
}

/** A width×height RGB PNG with a gradient tinted by `seed`, so each campaign gets a distinct image. */
export function pngImage(seed: number, width = 320, height = 180): Buffer {
  const raw = Buffer.alloc((width * 3 + 1) * height);
  const r0 = (seed * 53) % 256;
  const g0 = (seed * 97 + 80) % 256;
  const b0 = (seed * 29 + 160) % 256;
  for (let y = 0; y < height; y++) {
    const row = y * (width * 3 + 1);
    raw[row] = 0; // filter: none
    for (let x = 0; x < width; x++) {
      const i = row + 1 + x * 3;
      raw[i] = (r0 + x) % 256;
      raw[i + 1] = (g0 + y) % 256;
      raw[i + 2] = (b0 + ((x + y) >> 1)) % 256;
    }
  }
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 2; // colour type RGB
  ihdr[10] = 0;
  ihdr[11] = 0;
  ihdr[12] = 0;
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", zlib.deflateSync(raw)),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

/** A minimal, valid PDF (as the Go integration tests use for identity documents). */
export function pdfDocument(extra: string): Buffer {
  return Buffer.from(
    "%PDF-1.4\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n2 0 obj << /Type /Pages /Kids [] /Count 0 >> endobj\n" +
      `% ${extra}\ntrailer << /Root 1 0 R >>\n%%EOF\n`,
  );
}
