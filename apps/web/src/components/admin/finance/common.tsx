import { Badge } from "@/components/ui/badge";
import { formatLedgerAmount } from "@/lib/ledger/money";

export const FINANCE_BASE = "/admin/finance";

/** Signed ledger amount: visual "US$12.50" / "−US$12.50" with an accessible reading. */
export function LedgerAmount({ minor, currency }: { minor: string; currency: string }) {
  const f = formatLedgerAmount(minor, currency);
  return (
    <span className="tabular-nums">
      <span aria-hidden="true">{f.text}</span>
      <span className="sr-only">{f.accessibleText}</span>
    </span>
  );
}

/** TEST vs PRODUCTION ledger banner. A TEST ledger never shows real funds; nothing here is withdrawable. */
export function LedgerEnvironmentBanner({ environment, notice }: { environment: string; notice?: string }) {
  const test = environment !== "PRODUCTION";
  return (
    <div className={`rounded-card border-2 p-4 ${test ? "border-gold-500/50 bg-gold-100" : "border-danger-700/50 bg-surface"}`}>
      <p className="text-lg font-semibold text-ink-900">
        {test ? "TEST LEDGER" : "PRODUCTION LEDGER"} <Badge tone={test ? "gold" : "neutral"}>{environment}</Badge>
      </p>
      <p className="text-ink-800">
        {notice ||
          (test
            ? "Synthetic TEST_ONLY data. No real donation, refund, settlement or payout is recorded, and no balance is withdrawable."
            : "Production ledger.")}
      </p>
    </div>
  );
}

/** A TEST_ONLY label for synthetic or test records. */
export function TestOnlyBadge({ testOnly }: { testOnly: boolean }) {
  return testOnly ? <Badge tone="gold">TEST_ONLY</Badge> : null;
}
