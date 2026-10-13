import { formatPaymentMoney } from "@/lib/payments/money";

/**
 * Formats a signed minor-unit digit string in its currency (string arithmetic via the payments formatter for the
 * absolute value). Balances can be negative for some accounts (e.g. suspense), so the sign is shown explicitly.
 */
export function formatLedgerAmount(amountMinor: string, currency: string): { text: string; accessibleText: string } {
  const negative = amountMinor.startsWith("-");
  const abs = negative ? amountMinor.slice(1) : amountMinor;
  const f = formatPaymentMoney({ amount_minor: abs, currency });
  return negative ? { text: `−${f.text}`, accessibleText: `minus ${f.accessibleText}` } : f;
}
