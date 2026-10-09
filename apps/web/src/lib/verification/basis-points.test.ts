import { describe, expect, it } from "vitest";

import { basisPointsToPercent, formatOwnership, percentToBasisPoints } from "./basis-points";

describe("percentToBasisPoints (decimal string → integer basis points, no floating point)", () => {
  it.each([
    ["0", 0],
    ["100", 10000],
    ["25", 2500],
    ["25.5", 2550],
    ["25.50", 2550],
    ["12,5", 1250],
    ["0.01", 1],
    ["29.3", 2930], // 29.3 * 100 in floating point is 2929.9999999999995
    ["33.33", 3333],
    [" 7.05 % ", 705],
    ["10.", 1000],
    ["007", 700],
  ])("%j → %i", (input, expected) => {
    expect(percentToBasisPoints(input)).toEqual({ ok: true, basisPoints: expected });
  });

  it.each([
    ["", "EMPTY"],
    ["   ", "EMPTY"],
    ["abc", "INVALID_FORMAT"],
    ["-5", "INVALID_FORMAT"],
    ["1e2", "INVALID_FORMAT"],
    [".5", "INVALID_FORMAT"],
    ["1000", "INVALID_FORMAT"],
    ["12.345", "TOO_MANY_DECIMALS"],
    ["100.01", "OUT_OF_RANGE"],
    ["101", "OUT_OF_RANGE"],
    ["1,2,3", "INVALID_FORMAT"],
  ])("rejects %j (%s), never rounds", (input, error) => {
    expect(percentToBasisPoints(input)).toEqual({ ok: false, error });
  });

  it("round-trips every basis-point value 0…10000 exactly", () => {
    for (let bp = 0; bp <= 10000; bp++) {
      const text = basisPointsToPercent(bp);
      expect(percentToBasisPoints(text)).toEqual({ ok: true, basisPoints: bp });
    }
  });
});

describe("basisPointsToPercent / formatOwnership", () => {
  it("formats without trailing zeros", () => {
    expect(basisPointsToPercent(2550)).toBe("25.5");
    expect(basisPointsToPercent(10000)).toBe("100");
    expect(basisPointsToPercent(1)).toBe("0.01");
    expect(basisPointsToPercent(10)).toBe("0.1");
    expect(formatOwnership(3333)).toBe("33.33%");
    expect(formatOwnership(null)).toBe("—");
  });

  it("refuses out-of-range or non-integer input", () => {
    expect(() => basisPointsToPercent(-1)).toThrow(RangeError);
    expect(() => basisPointsToPercent(10001)).toThrow(RangeError);
    expect(() => basisPointsToPercent(12.5)).toThrow(RangeError);
  });
});
