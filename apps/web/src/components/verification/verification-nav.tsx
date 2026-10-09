import { NavLink } from "@/components/layout/nav-link";

const ITEMS = [
  { href: "/dashboard/verification", label: "Overview", exact: true },
  { href: "/dashboard/verification/identity", label: "Your identity" },
  { href: "/dashboard/verification/documents", label: "Documents" },
  { href: "/dashboard/verification/beneficiaries", label: "Beneficiaries" },
  { href: "/dashboard/verification/payout-destinations", label: "Payout accounts" },
  { href: "/dashboard/organisations", label: "Organisations" },
] as const;

export function VerificationNav() {
  return (
    <nav aria-label="Verification">
      <ul className="flex flex-wrap gap-2 md:flex-col">
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
  );
}
