import type { Metadata } from "next";

import { StepUpProvider } from "@/components/auth/step-up";
import { getSession } from "@/lib/auth/session";

export const metadata: Metadata = {
  robots: { index: false, follow: false, nocache: true },
};

/**
 * Dashboard area: provides step-up re-authentication to client components (document viewing may require
 * it). Access control is done by EVERY page (`requireUser` with its own path); this layout is not a check.
 */
export default async function DashboardLayout({ children }: LayoutProps<"/dashboard">) {
  const session = await getSession();
  if (!session.authenticated || !session.user) return <>{children}</>;
  const me = session.user;
  return (
    <StepUpProvider mfaEnabled={me.mfa_enabled} codeOnly={me.account_kind === "STAFF" || (me.roles?.length ?? 0) > 0}>
      {children}
    </StepUpProvider>
  );
}
