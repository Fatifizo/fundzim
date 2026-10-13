import { describe, expect, it } from "vitest";

import { formatLedgerAmount } from "./money";
import { minor, readAccount, readJournal, readJournalDetail, readOverview, readTrialBalance } from "./normalise";

describe("ledger normalisers", () => {
  it("minor units: signed digit strings only; numbers and decimals become 0", () => {
    expect(minor("1050")).toBe("1050");
    expect(minor("-25")).toBe("-25");
    expect(minor(1050)).toBe("0");
    expect(minor("10.50")).toBe("0");
    expect(minor("007")).toBe("0");
  });

  it("overview: production posting is shown enabled unless explicitly false; environment defaults to UNKNOWN", () => {
    expect(readOverview({ production_posting_enabled: false }).production_posting_enabled).toBe(false);
    expect(readOverview({}).production_posting_enabled).toBe(true);
    expect(readOverview({}).environment).toBe("UNKNOWN");
  });

  it("trial balance: balanced only if the flag AND the totals agree", () => {
    const tb = readTrialBalance({ environment: "TEST", currencies: [{ currency: "USD", total_debit_minor: "100", total_credit_minor: "99", difference_minor: "1", balanced: true, rows: [] }] });
    expect(tb.currencies[0]?.balanced).toBe(false);
  });

  it("journals: anything not explicitly PRODUCTION is TEST_ONLY; a journal is balanced only if its lines add up", () => {
    expect(readJournal({ id: "j", environment: "TEST" })?.test_only).toBe(true);
    expect(readJournal({ id: "j" })?.test_only).toBe(true);
    const lines = [
      { account_id: "a", debit_minor: "100", credit_minor: "0" },
      { account_id: "b", debit_minor: "0", credit_minor: "90" },
    ];
    expect(readJournalDetail({ id: "j", environment: "TEST", balanced: true, lines })?.balanced).toBe(false);
    expect(readJournalDetail({ id: "j", environment: "TEST", balanced: true, lines: [lines[0], { account_id: "b", debit_minor: "0", credit_minor: "100" }] })?.balanced).toBe(true);
  });

  it("accounts: malformed balances are 0, never NaN", () => {
    expect(readAccount({ id: "a", balance_minor: "abc" })?.balance_minor).toBe("0");
    expect(readAccount({ nope: 1 })).toBeNull();
  });

  it("money: signed amounts keep their sign visibly and audibly", () => {
    const neg = formatLedgerAmount("-2550", "USD");
    expect(neg.text.startsWith("−")).toBe(true);
    expect(neg.accessibleText.startsWith("minus ")).toBe(true);
    expect(formatLedgerAmount("2550", "USD").text.startsWith("−")).toBe(false);
  });
});
