export interface NavItem {
  href: string;
  label: string;
}

export const PRIMARY_NAV: readonly NavItem[] = [
  { href: "/campaigns", label: "Explore" },
  { href: "/how-it-works", label: "How It Works" },
  { href: "/dashboard/campaigns/new", label: "Start a Fundraiser" },
];

export const FOOTER_NAV: readonly NavItem[] = [
  { href: "/about", label: "About" },
  { href: "/how-it-works", label: "How It Works" },
  { href: "/privacy", label: "Privacy" },
  { href: "/terms", label: "Terms" },
  { href: "/contact", label: "Contact" },
];
