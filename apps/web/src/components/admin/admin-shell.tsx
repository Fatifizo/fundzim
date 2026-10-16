import type { ReactNode } from "react";

import { AccountContainer } from "@/components/account/account-shell";
import { NavLink } from "@/components/layout/nav-link";

const ITEMS = [
  { href: "/admin/verification", label: "All verification", exact: true },
  { href: "/admin/verification/kyc", label: "Identity (KYC)" },
  { href: "/admin/verification/kyb", label: "Organisations (KYB)" },
  { href: "/admin/verification/beneficiaries", label: "Beneficiaries" },
  { href: "/admin/verification/payout-destinations", label: "Payout accounts" },
  { href: "/admin/compliance/cases", label: "Compliance cases" },
  { href: "/admin/campaigns/review", label: "Campaign reviews" },
  { href: "/admin/campaigns", label: "All campaigns", exact: true },
  { href: "/admin/campaigns/updates", label: "Update moderation" },
  { href: "/admin/campaigns/reports", label: "Campaign reports" },
  { href: "/admin/campaigns/featured", label: "Featured campaigns" },
  { href: "/admin/payments", label: "Payments overview", exact: true },
  { href: "/admin/payments/attempts", label: "Payment attempts" },
  { href: "/admin/payments/unknown", label: "Unknown payments" },
  { href: "/admin/payments/intents", label: "Donation intents" },
  { href: "/admin/payments/webhooks", label: "Payment webhooks" },
  { href: "/admin/payments/reconciliation", label: "Reconciliation" },
  { href: "/admin/payments/refunds", label: "Refund requests" },
  { href: "/admin/payments/providers", label: "Payment providers" },
  { href: "/admin/payments/health", label: "Provider health" },
  { href: "/admin/finance/ledger", label: "Ledger", exact: true },
  { href: "/admin/finance/accounts", label: "Ledger accounts" },
  { href: "/admin/finance/journals", label: "Journals" },
  { href: "/admin/finance/trial-balance", label: "Trial balance" },
  { href: "/admin/finance/reconciliation", label: "Ledger reconciliation" },
  { href: "/admin/finance/exceptions", label: "Ledger exceptions" },
  { href: "/admin/finance/payouts", label: "Payouts", exact: true },
  { href: "/admin/finance/withdrawals", label: "Withdrawal requests" },
  { href: "/admin/finance/payouts/approvals", label: "Payout approvals" },
  { href: "/admin/finance/payouts/reservations", label: "Payout reservations" },
  { href: "/admin/finance/payouts/exceptions", label: "Payout exceptions" },
  { href: "/admin/finance/payouts/reconciliation", label: "Payout reconciliation" },
  { href: "/admin/finance/fees", label: "Fees", exact: true },
  { href: "/admin/finance/fees/policies", label: "Fee policies" },
  { href: "/admin/finance/fees/provider-schedules", label: "Provider fee evidence" },
  { href: "/admin/finance/fees/quotes", label: "Fee quotes" },
  { href: "/admin/finance/fees/assessments", label: "Fee assessments" },
  { href: "/admin/finance/fees/adjustments", label: "Fee adjustments" },
  { href: "/admin/finance/fees/reconciliation", label: "Fee reconciliation" },
  { href: "/admin/finance/revenue", label: "Revenue (TEST)" },
  { href: "/admin/risk", label: "Risk", exact: true },
  { href: "/admin/risk/signals", label: "Risk signals" },
  { href: "/admin/risk/evaluations", label: "Risk evaluations" },
  { href: "/admin/risk/rules", label: "Risk rules" },
  { href: "/admin/risk/cases", label: "Risk cases" },
  { href: "/admin/risk/holds", label: "Risk holds" },
  { href: "/admin/risk/screening", label: "Screening (TEST)" },
  { href: "/admin/risk/monitoring", label: "Risk monitoring" },
  { href: "/admin/account/personal-link", label: "Personal account link" },
] as const;

/** Staff area chrome. Rendered only for staff sessions; each page still checks access itself. */
export function AdminShell({ children }: { children: ReactNode }) {
  return (
    <AccountContainer>
      <p className="mb-6 rounded-xl border border-info-700/30 bg-info-50 px-4 py-2 text-sm font-semibold text-info-700">
        Staff area. Everything you open or do here is recorded in the audit log.
      </p>
      <div className="grid gap-8 lg:grid-cols-[14rem_1fr]">
        <nav aria-label="Staff">
          <ul className="flex flex-wrap gap-2 lg:flex-col">
            {ITEMS.map((item) => (
              <li key={item.href}>
                <NavLink
                  href={item.href}
                  exact={"exact" in item ? item.exact : false}
                  className="inline-flex min-h-11 items-center rounded-full px-4 font-medium text-ink-700 hover:bg-brand-50 hover:text-brand-800 aria-[current=page]:bg-brand-50 aria-[current=page]:text-brand-800 aria-[current=page]:underline underline-offset-4"
                >
                  {item.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <div className="min-w-0">{children}</div>
      </div>
    </AccountContainer>
  );
}
