import { describe, expect, it } from "vitest";

import {
  formatMoney,
  formatMoneyParts,
  minorToDecimalString,
  MoneyFormatError,
  parseAmountMinor,
} from "./money";

describe("minorToDecimalString", () => {
  it.each([
    ["0", 2, "0.00"],
    ["5", 2, "0.05"],
    ["99", 2, "0.99"],
    ["100", 2, "1.00"],
    ["123456", 2, "1234.56"],
    ["-5", 2, "-0.05"],
    ["-123456", 2, "-1234.56"],
    ["7", 0, "7"],
    ["1234", 3, "1.234"],
  ])("%s with %i minor digits → %s", (input, units, expected) => {
    expect(minorToDecimalString(input, units)).toBe(expected);
  });

  it("is exact for int64 extremes (beyond Number.MAX_SAFE_INTEGER)", () => {
    expect(minorToDecimalString("9223372036854775807", 2)).toBe("92233720368547758.07");
    expect(minorToDecimalString("-9223372036854775808", 2)).toBe("-92233720368547758.08");
    // 2^53 + 1 cannot be represented as a JS number; string handling keeps the last digit.
    expect(minorToDecimalString("9007199254740993", 2)).toBe("90071992547409.93");
  });
});

describe("parseAmountMinor", () => {
  it.each(["", " 1", "1 ", "+1", "1.5", "1e3", "0x10", "abc", "01", "00", "-0", "--1", "1,000", "١٢٣"])(
    "rejects %j",
    (input) => {
      expect(() => parseAmountMinor(input)).toThrow(MoneyFormatError);
    },
  );

  it("rejects values outside int64", () => {
    expect(() => parseAmountMinor("9223372036854775808")).toThrow(MoneyFormatError);
    expect(() => parseAmountMinor("-9223372036854775809")).toThrow(MoneyFormatError);
    expect(() => parseAmountMinor("12345678901234567890")).toThrow(MoneyFormatError);
  });

  it("rejects non-string input at runtime", () => {
    expect(() => parseAmountMinor(100 as unknown as string)).toThrow(MoneyFormatError);
  });

  it("accepts valid wire values", () => {
    expect(parseAmountMinor("0")).toBe(BigInt(0));
    expect(parseAmountMinor("10000")).toBe(BigInt(10000));
    expect(parseAmountMinor("-1")).toBe(BigInt(-1));
  });
});

describe("formatMoney", () => {
  it("formats USD as US$ with grouping", () => {
    expect(formatMoney({ amount_minor: "125000", currency: "USD" })).toBe("US$1,250.00");
    expect(formatMoney({ amount_minor: "5", currency: "USD" })).toBe("US$0.05");
  });

  it("formats ZWG as ZiG (code stays ZWG in data)", () => {
    expect(formatMoney({ amount_minor: "125000", currency: "ZWG" })).toBe("ZiG 1,250.00");
  });

  it("formats very large values exactly", () => {
    expect(formatMoney({ amount_minor: "9223372036854775807", currency: "USD" })).toBe(
      "US$92,233,720,368,547,758.07",
    );
  });

  it("formats negative values (e.g. reversals) with a leading sign", () => {
    expect(formatMoney({ amount_minor: "-125000", currency: "USD" })).toBe("-US$1,250.00");
  });

  it("provides accessible text naming the currency", () => {
    expect(formatMoneyParts({ amount_minor: "5000", currency: "USD" }).accessibleText).toBe("50.00 US dollars");
    expect(formatMoneyParts({ amount_minor: "5000", currency: "ZWG" }).accessibleText).toBe("50.00 ZiG");
  });

  it("honours an explicit minor-unit override from API configuration", () => {
    expect(formatMoney({ amount_minor: "125000", currency: "ZWG" }, { minorUnits: 0 })).toBe("ZiG 125,000");
  });

  it("rejects unsupported currencies and malformed amounts", () => {
    expect(() => formatMoney({ amount_minor: "100", currency: "EUR" as "USD" })).toThrow(MoneyFormatError);
    expect(() => formatMoney({ amount_minor: "12.50", currency: "USD" })).toThrow(MoneyFormatError);
    expect(() => formatMoney({ amount_minor: "1e5", currency: "USD" })).toThrow(MoneyFormatError);
  });

  it("matches Intl.NumberFormat grouping for exact decimal strings", () => {
    // Cross-check: Intl formats string input exactly in modern engines (no float rounding).
    const intl = new Intl.NumberFormat("en", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
    const decimal = minorToDecimalString("9007199254740993", 2);
    expect(formatMoney({ amount_minor: "9007199254740993", currency: "USD" })).toBe(
      `US$${intl.format(decimal as `${number}`)}`,
    );
  });
});
