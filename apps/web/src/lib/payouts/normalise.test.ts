import { describe, expect, it } from "vitest";

import { ApiError } from "@/lib/api/errors";

import { describePayoutError } from "./errors";
import { parseWithdrawalAmount } from "./money";
import { minor, readAvailability, readCheck, readDetail, readOverview, readOwnerPayout, readPage, readStaffPayout, reasonMessage } from "./normalise";

describe("payout normalisers", () => {
  it("keeps amounts as digit strings and refuses floats", () => {
    expect(minor("1250")).toBe("1250");
    expect(minor(12.5)).toBe("0");
    expect(minor("12.50")).toBe("0");
    expect(readOwnerPayout({ id: "p", amount_minor: 1000 })?.amount_minor).toBe("0");
  });
  it("never upgrades a payout to real funds", () => {
    const p = readOwnerPayout({ id: "p", amount_minor: "1000", test_payout: "no", funds_label: "REAL" })!;
    expect(p.test_payout).toBe(true);
    expect(p.funds_label).toBe("TEST_ONLY");
    expect(readStaffPayout({ id: "p" })!.test_payout).toBe(true);
    const a = readAvailability({ available_minor: "5000", production_status: "AVAILABLE", payouts_enabled: "yes" });
    expect(a.production_status).toBe("BLOCKED");
    expect(a.funds_label).toBe("TEST_ONLY");
    expect(a.payouts_enabled).toBe(false);
    expect(readOverview({ live_execution: "false" }).live_execution).toBe(true);
    expect(readOverview({ live_execution: false }).live_execution).toBe(false);
  });
  it("drops malformed checks and rows", () => {
    expect(readCheck({ check_id: "EC-01", result: "PASS" })).toEqual({ check_id: "EC-01", result: "PASS", detail_code: "" });
    expect(readCheck({ check_id: "x" })).toBeNull();
    expect(readCheck({ check_id: "EC-02", result: "WHATEVER" })!.result).toBe("FAIL");
    expect(readPage({ items: [{ id: "a" }, 3, null] }, readOwnerPayout).items).toHaveLength(1);
    expect(readDetail({ payout: { nope: 1 } })).toBeNull();
    const d = readDetail({ payout: { id: "p", reservation: { id: "r", amount_minor: "5" } }, eligibility_decisions: [{ id: "d", results: [{ check_id: "EC-09", result: "FAIL" }] }],
      events: [{ a: [1, "x"], b: { nested: true } }] })!;
    expect(d.payout.reservation!.amount_minor).toBe("5");
    expect(d.decisions[0]!.results[0]!.result).toBe("FAIL");
    expect(d.events[0]).toEqual({ a: "x", b: null });
  });
  it("maps refusal categories to plain language without compliance detail", () => {
    expect(reasonMessage("INSUFFICIENT_FUNDS")).toMatch(/settled funds/);
    expect(reasonMessage("NOT_AVAILABLE")).not.toMatch(/compliance|risk|fraud/i);
    const e = new ApiError({ code: "PAYOUT_NOT_ELIGIBLE", status: 422, message: "x", retryable: false, details: [{ field: "reason_category", code: "PAYOUT_IN_PROGRESS" }] });
    expect(describePayoutError(e).message).toMatch(/already in progress/);
  });
  it("parses withdrawal amounts with string arithmetic", () => {
    expect(parseWithdrawalAmount("25.50", "USD")).toEqual({ ok: true, amountMinor: "2550" });
    expect(parseWithdrawalAmount("25.505", "USD").ok).toBe(false);
    expect(parseWithdrawalAmount("100", "USD", "5000")).toEqual({ ok: false, message: "This is more than the available test balance." });
    expect(parseWithdrawalAmount("10", "XXX").ok).toBe(false);
  });
});
