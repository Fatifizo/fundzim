import type { Metadata } from "next";

import { AdminShell } from "@/components/admin/admin-shell";
import { StepUpProvider } from "@/components/auth/step-up";
import { getSessionOrAnonymous } from "@/lib/auth/session";

export const metadata: Metadata = {
  title: { template: "%s · FundZim staff", default: "FundZim staff" },
  robots: { index: false, follow: false, nocache: true },
};

/**
 * Staff area. Not an access check: every page calls requireStaff() and fetches its data with
 * serverGetOr404, so anyone who is not staff with the needed permission gets a 404. Non-staff visitors get
 * no admin chrome at all. Staff step up with an authenticator code only (the API refuses password step-up).
 */
export default async function AdminLayout({ children }: LayoutProps<"/admin">) {
  const session = await getSessionOrAnonymous();
  if (!session.authenticated || session.user?.account_kind !== "STAFF") return <>{children}</>;
  return (
    <StepUpProvider mfaEnabled codeOnly>
      <AdminShell>{children}</AdminShell>
    </StepUpProvider>
  );
}
