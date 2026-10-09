import { describe, expect, it } from "vitest";

import { goalFromFields, validateSection, type DraftFields } from "./validate";

const USD = [{ code: "USD", minor_units: 2, display_symbol: "US$" }];
const base: DraftFields = { category: "MEDICAL", title: "Help Chipo finish school", summary: "School fees for the final year of secondary school.", story: "x".repeat(120), currency: "USD", amount: "1500" };

describe("draft validation", () => {
  it("accepts a valid draft", () => {
    for (const s of ["category", "basics", "story", "goal"] as const) expect(validateSection(s, base, USD), s).toEqual({});
  });

  it("enforces lengths and plain text", () => {
    expect(validateSection("basics", { ...base, title: "Short" }, USD).title).toMatch(/at least 10/);
    expect(validateSection("basics", { ...base, summary: "<b>School fees for the final year</b>" }, USD).summary).toMatch(/plain text/);
    expect(validateSection("story", { ...base, story: "too short" }, USD).story).toMatch(/at least 100/);
    expect(validateSection("story", { ...base, story: "x".repeat(20_001) }, USD).story).toMatch(/at most 20,000/);
  });

  it("validates the goal only against available currencies", () => {
    expect(validateSection("goal", { ...base, currency: "ZWG" }, USD).currency).toBe("Choose a currency.");
    expect(validateSection("goal", { ...base, amount: "12.345" }, USD).amount).toMatch(/fewer decimal/);
    expect(goalFromFields({ currency: "USD", amount: "1,500.50" }, USD)).toEqual({ amount_minor: "150050", currency: "USD" });
    expect(goalFromFields({ currency: "USD", amount: "abc" }, USD)).toBeNull();
  });
});
