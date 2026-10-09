import { NavLink } from "@/components/layout/nav-link";

const TABS = [
  { sub: "", label: "Overview", exact: true },
  { sub: "/edit", label: "Details" },
  { sub: "/media", label: "Photos" },
  { sub: "/beneficiaries", label: "Beneficiary" },
  { sub: "/updates", label: "Updates" },
  { sub: "/settings", label: "Settings" },
] as const;

/** Section navigation for one campaign (the id is validated by the layout before this renders). */
export function CampaignNav({ campaignId }: { campaignId: string }) {
  const base = `/dashboard/campaigns/${campaignId}`;
  return (
    <nav aria-label="Campaign sections" className="mb-6">
      <ul className="flex flex-wrap gap-2">
        {TABS.map((t) => (
          <li key={t.sub}>
            <NavLink
              href={`${base}${t.sub}`}
              exact={"exact" in t ? t.exact : false}
              className="inline-flex min-h-11 items-center rounded-full border border-line px-4 font-medium text-ink-700 hover:bg-brand-50 hover:text-brand-800 aria-[current=page]:border-brand-700 aria-[current=page]:bg-brand-50 aria-[current=page]:text-brand-800 aria-[current=page]:underline underline-offset-4"
            >
              {t.label}
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  );
}
