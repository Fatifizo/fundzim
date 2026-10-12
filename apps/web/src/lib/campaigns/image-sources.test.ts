import { describe, expect, it } from "vitest";

import { readVariantWidths } from "./normalise";
import { IMAGE_SIZES, publicImageSources, VARIANT_WIDTHS } from "./paths";

const SLUG = "help-chipo-abc123";
const ID = "00000000-0000-0000-0000-000000000001";
const BASE = `/api/v1/public/campaigns/${SLUG}/media/${ID}`;

describe("publicImageSources (srcset)", () => {
  it("offers every standard width when variants are unknown (cards): the API falls back for a missing width", () => {
    expect(publicImageSources(SLUG, ID, null)).toEqual({
      src: `${BASE}?w=640`,
      srcSet: `${BASE}?w=320 320w, ${BASE}?w=640 640w, ${BASE}?w=1024 1024w`,
    });
  });

  it("uses only the variants the API listed, sorted, and picks the src fallback at or below the preferred width", () => {
    expect(publicImageSources(SLUG, ID, [640, 320], 1024)).toEqual({ src: `${BASE}?w=640`, srcSet: `${BASE}?w=320 320w, ${BASE}?w=640 640w` });
    expect(publicImageSources(SLUG, ID, [1024, 320, 640], 1024)?.src).toBe(`${BASE}?w=1024`);
    expect(publicImageSources(SLUG, ID, [1024], 640)?.src).toBe(`${BASE}?w=1024`);
  });

  it("never offers the full derivative or widths the API does not serve (anything else is 422)", () => {
    const s = publicImageSources(SLUG, ID, [100, 320, 640, 2048, 640], 640)!;
    expect(s.srcSet).toBe(`${BASE}?w=320 320w, ${BASE}?w=640 640w`);
    expect(s.srcSet).not.toMatch(/\d+ \d+w, [^?]+ /);
  });

  it("an image smaller than every variant uses the full image without srcset", () => {
    expect(publicImageSources(SLUG, ID, [])).toEqual({ src: BASE, srcSet: undefined });
  });

  it("refuses malformed identifiers", () => {
    expect(publicImageSources("../x", ID, null)).toBeNull();
    expect(publicImageSources(SLUG, "not-a-uuid", null)).toBeNull();
  });

  it("standard widths and sizes match the layouts", () => {
    expect(VARIANT_WIDTHS).toEqual([320, 640, 1024]);
    expect(IMAGE_SIZES.card.endsWith("calc(100vw - 32px)")).toBe(true);
    expect(IMAGE_SIZES.cover).toContain("(min-width: 1152px) 736px");
  });
});

describe("readVariantWidths", () => {
  it("reads ascending, unique, positive integer widths; null when absent", () => {
    expect(readVariantWidths([{ width: 1024, height: 576 }, { width: 320 }, { width: 320 }, { width: "640" }, { width: -1 }])).toEqual([320, 1024]);
    expect(readVariantWidths(undefined)).toBeNull();
    expect(readVariantWidths([])).toEqual([]);
  });
});
