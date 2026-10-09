import type { Metadata } from "next";

import { AccountContainer } from "@/components/account/account-shell";

export const metadata: Metadata = {
  title: { template: "%s · Your campaigns · FundZim", default: "Your campaigns" },
  robots: { index: false, follow: false, nocache: true },
};

/** Every page checks the session itself (requireUser) and loads its campaign through the API (404 for others). */
export default function CampaignsLayout({ children }: LayoutProps<"/dashboard/campaigns">) {
  return <AccountContainer>{children}</AccountContainer>;
}
