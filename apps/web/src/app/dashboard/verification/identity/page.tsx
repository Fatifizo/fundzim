import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { KycIdentity } from "@/components/verification/kyc-identity";
import { requireUser, serverGet } from "@/lib/auth/session";
import type { KycStatus } from "@/lib/verification/types";

export const metadata: Metadata = { title: "Verify your identity", robots: { index: false, follow: false, nocache: true } };

export default async function IdentityPage() {
  const me = await requireUser("/dashboard/verification/identity");
  const status = await serverGet<KycStatus>("/api/v1/kyc/status", "/dashboard/verification/identity");
  return (
    <AccountPage title="Your identity" intro="Your personal details for identity verification.">
      <KycIdentity initialCase={status.case} level={status.level} emailVerified={me.email_verified} phoneVerified={me.phone_verified} />
    </AccountPage>
  );
}
