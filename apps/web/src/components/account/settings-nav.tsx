import { NavLink } from "@/components/layout/nav-link";

const ITEMS = [
  { href: "/settings/profile", label: "Profile" },
  { href: "/settings/security", label: "Security" },
  { href: "/settings/mfa", label: "Two-step verification" },
  { href: "/settings/sessions", label: "Sessions" },
] as const;

export function SettingsNav() {
  return (
    <nav aria-label="Settings">
      <ul className="flex flex-wrap gap-2 md:flex-col">
        {ITEMS.map((item) => (
          <li key={item.href}>
            <NavLink
              href={item.href}
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
