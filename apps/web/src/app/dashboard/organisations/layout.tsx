import type { Metadata } from "next";

import { VerificationShell } from "@/components/verification/verification-shell";

export const metadata: Metadata = {
  robots: { index: false, follow: false, nocache: true },
};

export default function OrganisationsLayout({ children }: LayoutProps<"/dashboard/organisations">) {
  return <VerificationShell>{children}</VerificationShell>;
}
