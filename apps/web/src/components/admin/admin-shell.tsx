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
