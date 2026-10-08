import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { MfaSettings } from "@/components/account/mfa-settings";
import { Card } from "@/components/ui/card";
import { requireUser, serverGet } from "@/lib/auth/session";
import type { SecurityOverview } from "@/lib/auth/types";

export const metadata: Metadata = { title: "Two-step verification", robots: { index: false, follow: false } };

export default async function MfaSettingsPage() {
  await requireUser("/settings/mfa");
  const security = await serverGet<SecurityOverview>("/api/v1/me/security", "/settings/mfa");
  return (
    <AccountPage title="Two-step verification" intro="Protect your account with a code from an authenticator app.">
      <Card>
        <MfaSettings mfaEnabled={security.mfa_enabled} recoveryCodesRemaining={security.recovery_codes_remaining} />
      </Card>
    </AccountPage>
  );
}
