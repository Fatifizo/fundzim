import type { Metadata } from "next";

import { VerificationShell } from "@/components/verification/verification-shell";

export const metadata: Metadata = {
  robots: { index: false, follow: false, nocache: true },
};

/** Every page checks the session itself (requireUser); the layout only provides navigation. */
export default function VerificationLayout({ children }: LayoutProps<"/dashboard/verification">) {
  return <VerificationShell>{children}</VerificationShell>;
}
