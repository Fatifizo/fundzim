import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

import { formatPaymentMoney, minorUnitsFor, parseRefundAmount } from "./money";

describe("payment money display", () => {
  it("formats minor-unit strings exactly, with the currency", () => {
    expect(formatPaymentMoney({ amount_minor: "2550", currency: "USD" })).toEqual({ text: "US$25.50", accessibleText: "25.50 US dollars" });
    expect(formatPaymentMoney({ amount_minor: "125050", currency: "ZWG" }).text).toBe("ZiG 1,250.50");
    expect(formatPaymentMoney({ amount_minor: "5", currency: "USD" }).text).toBe("US$0.05");
    // Beyond 2^53: exact (a float would round this).
    expect(formatPaymentMoney({ amount_minor: "9007199254740993", currency: "USD" }).text).toBe("US$90,071,992,547,409.93");
  });

  it("shows an unknown currency by its code and never throws on bad input", () => {
    expect(formatPaymentMoney({ amount_minor: "100", currency: "EUR" }).text).toBe("EUR 1.00");
    expect(formatPaymentMoney(null).text).toBe("—");
    expect(formatPaymentMoney({ amount_minor: "1e3", currency: "USD" }).text).toBe("—");
  });

  it("knows the minor units of USD and ZWG only", () => {
    expect(minorUnitsFor("USD")).toBe(2);
    expect(minorUnitsFor("ZWG")).toBe(2);
    expect(minorUnitsFor("EUR")).toBeNull();
  });
});

describe("refund amount input → minor units", () => {
  it("converts decimal strings without floating point", () => {
    expect(parseRefundAmount("25", "USD")).toEqual({ ok: true, amountMinor: "2500" });
    expect(parseRefundAmount("25.5", "USD")).toEqual({ ok: true, amountMinor: "2550" });
    expect(parseRefundAmount(" 1,250.05 ", "USD")).toEqual({ ok: true, amountMinor: "125005" });
    expect(parseRefundAmount("0.1", "USD")).toEqual({ ok: true, amountMinor: "10" }); // 0.1 is not exact in binary floating point
    expect(parseRefundAmount("0.29", "USD")).toEqual({ ok: true, amountMinor: "29" }); // 0.29 * 100 = 28.999999999999996 in floats
    expect(parseRefundAmount("90071992547409.93", "USD")).toEqual({ ok: true, amountMinor: "9007199254740993" });
  });

  it("rejects instead of rounding", () => {
    expect(parseRefundAmount("1.234", "USD")).toEqual({ ok: false, message: expect.stringContaining("fewer decimal places") });
    for (const bad of ["", "abc", "-5", "1e3", "1.", ".5", "1,23", "+5", "0x10", "Infinity"]) {
      expect(parseRefundAmount(bad, "USD").ok, bad).toBe(false);
    }
    expect(parseRefundAmount("0", "USD")).toEqual({ ok: false, message: expect.stringContaining("more than zero") });
    expect(parseRefundAmount("99999999999999999999", "USD")).toEqual({ ok: false, message: "This amount is too large." });
  });

  it("refuses more than the payment amount (BigInt, same currency) and unknown currencies", () => {
    expect(parseRefundAmount("1250.50", "USD", "125050")).toEqual({ ok: true, amountMinor: "125050" });
    expect(parseRefundAmount("1250.51", "USD", "125050")).toEqual({ ok: false, message: expect.stringContaining("more than the payment") });
    expect(parseRefundAmount("10", "EUR").ok).toBe(false);
  });

  it("the payments money module never uses Number()/parseFloat on amounts", () => {
    const source = readFileSync(join(process.cwd(), "src/lib/payments/money.ts"), "utf8").replace(/\/\*[\s\S]*?\*\/|\/\/.*$/gm, "");
    expect(source).not.toMatch(/parseFloat|Number\(|\/\s*100\b/);
  });
});
