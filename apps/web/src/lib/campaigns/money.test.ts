import { describe, expect, it } from "vitest";

import { formatGoal, minorToInput, parseDecimalToMinor } from "./money";

describe("parseDecimalToMinor (string arithmetic only)", () => {
  it("converts decimal strings to minor-unit digit strings", () => {
    expect(parseDecimalToMinor("50", 2)).toEqual({ ok: true, amountMinor: "5000" });
    expect(parseDecimalToMinor("50.5", 2)).toEqual({ ok: true, amountMinor: "5050" });
    expect(parseDecimalToMinor("0.01", 2)).toEqual({ ok: true, amountMinor: "1" });
    expect(parseDecimalToMinor(" 1,250.75 ", 2)).toEqual({ ok: true, amountMinor: "125075" });
    expect(parseDecimalToMinor("1,000,000", 2)).toEqual({ ok: true, amountMinor: "100000000" });
    expect(parseDecimalToMinor("007.10", 2)).toEqual({ ok: true, amountMinor: "710" });
    expect(parseDecimalToMinor("1500", 0)).toEqual({ ok: true, amountMinor: "1500" });
    expect(parseDecimalToMinor("1.234", 3)).toEqual({ ok: true, amountMinor: "1234" });
  });

  it("is exact where floating point is not", () => {
    // 0.1 + 0.2 style traps and 1.005 * 100 = 100.49999… in binary floating point.
    expect(parseDecimalToMinor("1.005", 3)).toEqual({ ok: true, amountMinor: "1005" });
    expect(parseDecimalToMinor("4.35", 2)).toEqual({ ok: true, amountMinor: "435" });
    expect(parseDecimalToMinor("92233720368547758.07", 2)).toEqual({ ok: true, amountMinor: "9223372036854775807" });
  });

  it("rejects instead of rounding or guessing", () => {
    expect(parseDecimalToMinor("", 2)).toEqual({ ok: false, problem: "EMPTY" });
    expect(parseDecimalToMinor("1.234", 2)).toEqual({ ok: false, problem: "TOO_MANY_DECIMALS" });
    expect(parseDecimalToMinor("1.5", 0)).toEqual({ ok: false, problem: "TOO_MANY_DECIMALS" });
    expect(parseDecimalToMinor("0", 2)).toEqual({ ok: false, problem: "NOT_POSITIVE" });
    expect(parseDecimalToMinor("0.00", 2)).toEqual({ ok: false, problem: "NOT_POSITIVE" });
    expect(parseDecimalToMinor("92233720368547758.08", 2)).toEqual({ ok: false, problem: "TOO_LARGE" });
    expect(parseDecimalToMinor("99999999999999999999", 2)).toEqual({ ok: false, problem: "TOO_LARGE" });
    for (const bad of ["-5", "+5", "1e3", "1E3", "1.", ".5", "1,00", "12,34.5", "1 000", "NaN", "Infinity", "0x10", "1.2.3", "US$5", "5,000,00", "١٢"]) {
      expect(parseDecimalToMinor(bad, 2), bad).toEqual({ ok: false, problem: "INVALID_FORMAT" });
    }
  });

  it("round-trips with minorToInput", () => {
    for (const minor of ["1", "99", "100", "125075", "9223372036854775807"]) {
      const parsed = parseDecimalToMinor(minorToInput(minor, 2), 2);
      expect(parsed).toEqual({ ok: true, amountMinor: minor });
    }
  });
});

describe("formatGoal", () => {
  it("formats from minor units with the currency always shown", () => {
    expect(formatGoal({ amount_minor: "150000", currency: "USD" })).toEqual({ text: "US$1,500.00", accessibleText: "1,500.00 US dollars" });
    expect(formatGoal({ amount_minor: "5", currency: "ZWG" }).text).toBe("ZiG 0.05");
    expect(formatGoal({ amount_minor: "9223372036854775807", currency: "USD" }).text).toBe("US$92,233,720,368,547,758.07");
    expect(formatGoal({ amount_minor: "1000", currency: "EUR" }).text).toBe("EUR 10.00");
  });

  it("never throws on malformed data", () => {
    expect(formatGoal(null).text).toBe("—");
    expect(formatGoal({ amount_minor: "12.5", currency: "USD" }).text).toBe("—");
    expect(formatGoal({ amount_minor: "abc", currency: "USD" }).text).toBe("—");
  });
});
